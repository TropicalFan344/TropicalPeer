package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"TropicalPeer/internal/portmap"
	"TropicalPeer/internal/server"
)

type linkFlags struct {
	domain   *string
	web      *string
	cert     *string
	key      *string
	plain    *bool
	webURL   *string
	publicIP *string
	turnPort *int
	turnHost *string
	relay    *string
	allowLAN *bool
	origins  *string
	acmeDir  *string
	duck     *string

	// set by start
	foundIP net.IP
	checks  []reachCheck
}

func (f *linkFlags) register() {
	f.domain = flag.String("domain", "", "public name of this server (enables share by link; gets a Let's Encrypt certificate unless -cert or -web-plain)")
	f.web = flag.String("web", ":443", "address for the HTTPS receiver page and signaling")
	f.cert = flag.String("cert", "", "TLS certificate file (instead of Let's Encrypt)")
	f.key = flag.String("key", "", "TLS key file, with -cert")
	f.plain = flag.Bool("web-plain", false, "serve plain HTTP on -web, for running behind a reverse proxy that does HTTPS")
	f.webURL = flag.String("web-url", "", "where links point (default https://<domain>/r/)")
	f.publicIP = flag.String("public-ip", "", "this server's public IPv4 address, for the TURN relay (default: from the router with -upnp, else from DNS)")
	f.turnPort = flag.Int("turn-port", 3478, "TURN/STUN port (UDP and TCP)")
	f.turnHost = flag.String("turn-host", "", "host name or IP browsers use for TURN/STUN (default -domain)")
	f.relay = flag.String("turn-relay", "40100-40119", "UDP port range for TURN relay allocations")
	f.allowLAN = flag.Bool("turn-allow-lan", true, "let the TURN relay reach private addresses (senders on this server's own network)")
	f.origins = flag.String("web-origins", "", "comma-separated extra origins allowed to use signaling (a separately hosted receiver page)")
	f.duck = flag.String("duckdns-token", os.Getenv("DUCKDNS_TOKEN"), "DuckDNS token (default $DUCKDNS_TOKEN): with a .duckdns.org -domain, keeps its address current and gets the certificate through DNS, so port 443 needn't be reachable")
	dir, _ := os.UserConfigDir()
	f.acmeDir = flag.String("acme-dir", filepath.Join(dir, "TropicalPeer", "acme"), "where Let's Encrypt certificates are kept")
}

func (f *linkFlags) enabled() bool { return *f.domain != "" }

// start serves the receiver page and signaling, and the TURN relay. It
// returns the extra ports to forward.
func (f *linkFlags) start(ctx context.Context, logger *log.Logger, srv *server.Server, upnp bool) ([]portmap.Spec, error) {
	_, webPortStr, err := net.SplitHostPort(*f.web)
	if err != nil {
		return nil, fmt.Errorf("bad -web %q: %v", *f.web, err)
	}
	webPort, _ := strconv.Atoi(webPortStr)
	var lo, hi int
	if _, err := fmt.Sscanf(*f.relay, "%d-%d", &lo, &hi); err != nil || lo <= 0 || hi < lo || hi > 65535 {
		return nil, fmt.Errorf("bad -turn-relay %q", *f.relay)
	}

	webURL := *f.webURL
	if webURL == "" {
		scheme, defPort := "https://", 443
		if *f.plain {
			scheme, defPort = "http://", 80
		}
		webURL = scheme + *f.domain
		if webPort != defPort {
			webURL += ":" + webPortStr
		}
		webURL += "/r/"
	}
	if !strings.HasSuffix(webURL, "/") {
		webURL += "/"
	}

	ip, err := f.findPublicIP(ctx, upnp)
	if err != nil {
		return nil, err
	}
	turnHost := *f.turnHost
	if turnHost == "" {
		turnHost = *f.domain
	}
	t, err := server.StartTURN(server.TURNConfig{
		Port: *f.turnPort, Host: turnHost, PublicIP: ip,
		RelayMin: lo, RelayMax: hi, AllowLAN: *f.allowLAN,
	})
	if err != nil {
		return nil, err
	}
	go func() { <-ctx.Done(); t.Close() }()

	links := &server.Links{WebURL: webURL, ICE: t.ICE}
	for _, o := range strings.Split(*f.origins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			links.Origins = append(links.Origins, o)
		}
	}
	srv.Links = links

	hs := &http.Server{Addr: *f.web, Handler: srv.WebHandler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { <-ctx.Done(); hs.Close() }()
	switch {
	case *f.plain:
		go func() { logErr(logger, hs.ListenAndServe()) }()
	case *f.cert != "":
		go func() { logErr(logger, hs.ListenAndServeTLS(*f.cert, *f.key)) }()
	case isDuckDNS(*f.domain) && *f.duck != "":
		d := &duckDNS{domain: *f.domain, token: *f.duck, dir: *f.acmeDir, logger: logger}
		go d.keepIP(ctx)
		if err := d.start(ctx); err != nil {
			return nil, fmt.Errorf("certificate: %w", err)
		}
		hs.TLSConfig = &tls.Config{GetCertificate: d.getCertificate, MinVersion: tls.VersionTLS12}
		go func() { logErr(logger, hs.ListenAndServeTLS("", "")) }()
	default:
		m := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(*f.domain),
			Cache:      autocert.DirCache(*f.acmeDir),
		}
		hs.TLSConfig = m.TLSConfig()
		hs.TLSConfig.MinVersion = tls.VersionTLS12
		go func() { logErr(logger, hs.ListenAndServeTLS("", "")) }()
	}
	logger.Printf("links: receiver page at %s, TURN on %s:%d (relay %s, public %s)", webURL, turnHost, *f.turnPort, *f.relay, ip)

	f.foundIP = ip
	f.checks = []reachCheck{{"TCP", webPort}, {"TCP", *f.turnPort}, {"STUN", *f.turnPort}}
	specs := []portmap.Spec{{Protocol: "TCP", Port: webPort}, {Protocol: "UDP", Port: *f.turnPort}, {Protocol: "TCP", Port: *f.turnPort}}
	for p := lo; p <= hi; p++ {
		specs = append(specs, portmap.Spec{Protocol: "UDP", Port: p})
	}
	return specs, nil
}

func (f *linkFlags) findPublicIP(ctx context.Context, upnp bool) (net.IP, error) {
	if *f.publicIP != "" {
		ip := net.ParseIP(*f.publicIP).To4()
		if ip == nil {
			return nil, fmt.Errorf("bad -public-ip %q", *f.publicIP)
		}
		return ip, nil
	}
	if upnp {
		dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		r, err := portmap.Discover(dctx)
		cancel()
		if err == nil {
			return net.IP(r.ExternalIP.AsSlice()).To4(), nil
		}
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", *f.domain)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("can't find this server's public address: pass -public-ip (%v)", err)
	}
	return ips[0], nil
}

func logErr(logger *log.Logger, err error) {
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Printf("links: web: %v", err)
	}
}
