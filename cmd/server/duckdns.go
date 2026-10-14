package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
)

// DuckDNS: keeps the name pointing at this server and gets a Let's Encrypt
// certificate through a DNS TXT record (DNS-01), which works when port 443
// can't be reached from the internet, as on many home connections.

const (
	duckdnsSuffix   = ".duckdns.org"
	renewBefore     = 30 * 24 * time.Hour
	ipRefreshEvery  = 10 * time.Minute
	txtPropagation  = 20 * time.Second
	certCheckEvery  = 12 * time.Hour
	certIssueBudget = 5 * time.Minute
)

type duckDNS struct {
	domain string // e.g. tropicalpeer.duckdns.org
	token  string
	dir    string
	logger *log.Logger

	mu   sync.Mutex
	cert *tls.Certificate
}

func isDuckDNS(domain string) bool { return strings.HasSuffix(strings.ToLower(domain), duckdnsSuffix) }

func (d *duckDNS) sub() string {
	name := strings.TrimSuffix(strings.ToLower(d.domain), duckdnsSuffix)
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	return name
}

// update calls DuckDNS's update API; extra are added query parameters.
func (d *duckDNS) update(ctx context.Context, extra url.Values) error {
	q := url.Values{"domains": {d.sub()}, "token": {d.token}}
	for k, v := range extra {
		q[k] = v
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.duckdns.org/update?"+q.Encode(), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("duckdns: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if !strings.HasPrefix(string(body), "OK") {
		return errors.New("duckdns refused the update; check the token and the domain name")
	}
	return nil
}

// keepIP points the name at this network's public address (DuckDNS sees it)
// now and every few minutes.
func (d *duckDNS) keepIP(ctx context.Context) {
	for {
		if err := d.update(ctx, nil); err != nil && ctx.Err() == nil {
			d.logger.Printf("duckdns: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(ipRefreshEvery):
		}
	}
}

func (d *duckDNS) getCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cert == nil {
		return nil, errors.New("no certificate yet")
	}
	return d.cert, nil
}

// start loads or gets the certificate, then renews it in the background.
func (d *duckDNS) start(ctx context.Context) error {
	if err := os.MkdirAll(d.dir, 0o700); err != nil {
		return err
	}
	if err := d.ensure(ctx); err != nil {
		return err
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(certCheckEvery):
			}
			if err := d.ensure(ctx); err != nil && ctx.Err() == nil {
				d.logger.Printf("certificate: renewing failed, will retry: %v", err)
			}
		}
	}()
	return nil
}

func (d *duckDNS) certPath() string { return filepath.Join(d.dir, d.domain+".pem") }

// ensure has a certificate valid for at least renewBefore, getting one if not.
func (d *duckDNS) ensure(ctx context.Context) error {
	if c, err := tls.LoadX509KeyPair(d.certPath(), d.certPath()); err == nil && time.Until(c.Leaf.NotAfter) > renewBefore {
		d.mu.Lock()
		d.cert = &c
		d.mu.Unlock()
		return nil
	}
	d.logger.Printf("certificate: asking Let's Encrypt for %s (DNS check through DuckDNS)...", d.domain)
	ctx, cancel := context.WithTimeout(ctx, certIssueBudget)
	defer cancel()
	pemBytes, err := d.issue(ctx)
	if err != nil {
		return err
	}
	if err := os.WriteFile(d.certPath(), pemBytes, 0o600); err != nil {
		return err
	}
	c, err := tls.X509KeyPair(pemBytes, pemBytes)
	if err != nil {
		return err
	}
	d.mu.Lock()
	d.cert = &c
	d.mu.Unlock()
	d.logger.Printf("certificate: got one for %s, valid until %s", d.domain, c.Leaf.NotAfter.Format("2006-01-02"))
	return nil
}

func (d *duckDNS) accountKey() (crypto.Signer, error) {
	path := filepath.Join(d.dir, "account.key")
	if b, err := os.ReadFile(path); err == nil {
		if blk, _ := pem.Decode(b); blk != nil {
			return x509.ParseECPrivateKey(blk.Bytes)
		}
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, _ := x509.MarshalECPrivateKey(k)
	return k, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600)
}

// issue runs the ACME DNS-01 flow and returns the key and chain as PEM.
func (d *duckDNS) issue(ctx context.Context) ([]byte, error) {
	akey, err := d.accountKey()
	if err != nil {
		return nil, err
	}
	client := &acme.Client{Key: akey, DirectoryURL: acme.LetsEncryptURL}
	if _, err := client.Register(ctx, &acme.Account{}, acme.AcceptTOS); err != nil && !errors.Is(err, acme.ErrAccountAlreadyExists) {
		return nil, fmt.Errorf("acme register: %w", err)
	}
	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(d.domain))
	if err != nil {
		return nil, fmt.Errorf("acme order: %w", err)
	}
	defer d.update(context.Background(), url.Values{"txt": {"cleared"}, "clear": {"true"}})
	for _, u := range order.AuthzURLs {
		authz, err := client.GetAuthorization(ctx, u)
		if err != nil {
			return nil, err
		}
		if authz.Status == acme.StatusValid {
			continue
		}
		var chal *acme.Challenge
		for _, c := range authz.Challenges {
			if c.Type == "dns-01" {
				chal = c
			}
		}
		if chal == nil {
			return nil, errors.New("acme: no dns-01 challenge offered")
		}
		rec, err := client.DNS01ChallengeRecord(chal.Token)
		if err != nil {
			return nil, err
		}
		if err := d.update(ctx, url.Values{"txt": {rec}}); err != nil {
			return nil, err
		}
		select {
		case <-time.After(txtPropagation):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if _, err := client.Accept(ctx, chal); err != nil {
			return nil, fmt.Errorf("acme accept: %w", err)
		}
		if _, err := client.WaitAuthorization(ctx, authz.URI); err != nil {
			return nil, fmt.Errorf("acme: the DNS check failed: %w", err)
		}
	}
	order, err = client.WaitOrder(ctx, order.URI)
	if err != nil {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{DNSNames: []string{d.domain}}, key)
	if err != nil {
		return nil, err
	}
	chain, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return nil, fmt.Errorf("acme finalize: %w", err)
	}
	kder, _ := x509.MarshalECPrivateKey(key)
	out := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder})
	for _, c := range chain {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c})...)
	}
	return out, nil
}
