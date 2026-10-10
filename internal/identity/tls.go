package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"math/big"
	"time"
)

// ALPN is the application protocol negotiated on every TropicalPeer QUIC connection.
const ALPN = "tropicalpeer/1"

// Certificate builds a self-signed TLS certificate from the identity key.
// Trust never comes from a CA: peers pin each other's Ed25519 public keys.
func (i *Identity) Certificate() (tls.Certificate, error) {
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, i.PublicKey, i.PrivateKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: i.PrivateKey}, nil
}

// PeerKey extracts the Ed25519 public key from a peer's presented certificate chain.
func PeerKey(rawCerts [][]byte) (ed25519.PublicKey, error) {
	if len(rawCerts) == 0 {
		return nil, errors.New("peer presented no certificate")
	}
	cert, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return nil, err
	}
	pub, ok := cert.PublicKey.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("peer certificate is not Ed25519")
	}
	// The certificate must be signed by its own key. (TLS's CertificateVerify
	// separately proves the peer holds the key during the handshake.)
	if !ed25519.Verify(pub, cert.RawTBSCertificate, cert.Signature) {
		return nil, errors.New("peer certificate has a bad self-signature")
	}
	return pub, nil
}

// ServerTLS returns a TLS config for accepting connections. isTrusted decides
// whether a connecting device (by public key) may talk to us.
func (i *Identity) ServerTLS(isTrusted func(ed25519.PublicKey) bool) (*tls.Config, error) {
	cert, err := i.Certificate()
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates:          []tls.Certificate{cert},
		ClientAuth:            tls.RequireAnyClientCert,
		NextProtos:            []string{ALPN},
		MinVersion:            tls.VersionTLS13,
		VerifyPeerCertificate: verifyWith(isTrusted),
	}, nil
}

// ClientTLS returns a TLS config for dialing a peer whose key we expect.
func (i *Identity) ClientTLS(expected ed25519.PublicKey) (*tls.Config, error) {
	cert, err := i.Certificate()
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: true, // replaced by key pinning below
		NextProtos:         []string{ALPN},
		MinVersion:         tls.VersionTLS13,
		VerifyPeerCertificate: verifyWith(func(pub ed25519.PublicKey) bool {
			return pub.Equal(expected)
		}),
	}, nil
}

func verifyWith(isTrusted func(ed25519.PublicKey) bool) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		pub, err := PeerKey(rawCerts)
		if err != nil {
			return err
		}
		if !isTrusted(pub) {
			return errors.New("device is not a paired peer")
		}
		return nil
	}
}
