package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// DuckDNS: one API call sets the address (DuckDNS takes the caller's) or the
// TXT record, which DuckDNS serves for every name under the domain.

const duckdnsSuffix = ".duckdns.org"

type duckDNS struct {
	domain string // e.g. tropicalpeer.duckdns.org
	token  string
}

func isDuckDNS(domain string) bool { return strings.HasSuffix(strings.ToLower(domain), duckdnsSuffix) }

func (d *duckDNS) label() string { return "DuckDNS" }

func (d *duckDNS) sub() string {
	name := strings.TrimSuffix(strings.ToLower(d.domain), duckdnsSuffix)
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	return name
}

func (d *duckDNS) updateIP(ctx context.Context) error { return d.update(ctx, nil) }

func (d *duckDNS) setTXT(ctx context.Context, value string) error {
	return d.update(ctx, url.Values{"txt": {value}})
}

func (d *duckDNS) clearTXT(ctx context.Context) error {
	return d.update(ctx, url.Values{"txt": {"cleared"}, "clear": {"true"}})
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
