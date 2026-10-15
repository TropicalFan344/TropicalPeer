package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// Cloudflare: a name in a zone on Cloudflare, through its API with a token
// that may edit the zone's DNS. The server keeps the name's A record on this
// network's public address (DNS only: Cloudflare's proxy can't carry the
// TURN relay) and sets _acme-challenge.<name> for the certificate.

var (
	cloudflareAPI = "https://api.cloudflare.com/client/v4"
	// publicIPURL answers with "ip=<the caller's address>" among other lines.
	publicIPURL = "https://1.1.1.1/cdn-cgi/trace"
)

const cloudflareTTL = 120 // seconds; short, as the address can change

type cloudflare struct {
	domain string // e.g. share.example.com
	token  string

	mu   sync.Mutex
	zone string // zone ID, once found
}

func (c *cloudflare) label() string { return "Cloudflare" }

type cfRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl,omitempty"`
	Proxied *bool  `json:"proxied,omitempty"`
}

// call makes one API request and decodes its result into out (if not nil).
func (c *cloudflare) call(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, cloudflareAPI+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare: %w", err)
	}
	defer resp.Body.Close()
	var r struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return fmt.Errorf("cloudflare: %s: %s", resp.Status, err)
	}
	if !r.Success {
		var msgs []string
		for _, e := range r.Errors {
			msgs = append(msgs, fmt.Sprintf("%s (%d)", e.Message, e.Code))
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			msgs = append(msgs, "check that the token may edit DNS in this zone")
		}
		return fmt.Errorf("cloudflare: %s %s: %s", method, strings.SplitN(path, "?", 2)[0], strings.Join(msgs, "; "))
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

// zoneID finds the zone the name is in: the name itself or a parent.
func (c *cloudflare) zoneID(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.zone != "" {
		return c.zone, nil
	}
	labels := strings.Split(strings.ToLower(strings.TrimSuffix(c.domain, ".")), ".")
	for i := 0; i < len(labels)-1; i++ {
		var zones []struct {
			ID string `json:"id"`
		}
		name := strings.Join(labels[i:], ".")
		if err := c.call(ctx, http.MethodGet, "/zones?name="+url.QueryEscape(name), nil, &zones); err != nil {
			return "", err
		}
		if len(zones) > 0 {
			c.zone = zones[0].ID
			return c.zone, nil
		}
	}
	return "", fmt.Errorf("cloudflare: no zone for %s that this token can see", c.domain)
}

func (c *cloudflare) records(ctx context.Context, zone, typ, name string) ([]cfRecord, error) {
	var recs []cfRecord
	q := url.Values{"type": {typ}, "name": {name}}
	err := c.call(ctx, http.MethodGet, "/zones/"+zone+"/dns_records?"+q.Encode(), nil, &recs)
	return recs, err
}

// updateIP sets the A record to this network's public address, creating it
// if needed, and keeps it out of Cloudflare's proxy.
func (c *cloudflare) updateIP(ctx context.Context) error {
	ip, err := publicIPv4(ctx)
	if err != nil {
		return err
	}
	zone, err := c.zoneID(ctx)
	if err != nil {
		return err
	}
	recs, err := c.records(ctx, zone, "A", c.domain)
	if err != nil {
		return err
	}
	off := false
	want := cfRecord{Type: "A", Name: c.domain, Content: ip.String(), TTL: cloudflareTTL, Proxied: &off}
	if len(recs) == 0 {
		return c.call(ctx, http.MethodPost, "/zones/"+zone+"/dns_records", want, nil)
	}
	r := recs[0]
	if r.Content == want.Content && r.Proxied != nil && !*r.Proxied {
		return nil
	}
	return c.call(ctx, http.MethodPatch, "/zones/"+zone+"/dns_records/"+r.ID, want, nil)
}

func (c *cloudflare) challengeName() string { return "_acme-challenge." + c.domain }

func (c *cloudflare) setTXT(ctx context.Context, value string) error {
	if err := c.clearTXT(ctx); err != nil {
		return err
	}
	zone, err := c.zoneID(ctx)
	if err != nil {
		return err
	}
	return c.call(ctx, http.MethodPost, "/zones/"+zone+"/dns_records",
		cfRecord{Type: "TXT", Name: c.challengeName(), Content: value, TTL: cloudflareTTL}, nil)
}

// clearTXT removes every _acme-challenge TXT record for the name.
func (c *cloudflare) clearTXT(ctx context.Context) error {
	zone, err := c.zoneID(ctx)
	if err != nil {
		return err
	}
	recs, err := c.records(ctx, zone, "TXT", c.challengeName())
	if err != nil {
		return err
	}
	for _, r := range recs {
		if err := c.call(ctx, http.MethodDelete, "/zones/"+zone+"/dns_records/"+r.ID, nil, nil); err != nil {
			return err
		}
	}
	return nil
}

// publicIPv4 asks Cloudflare which address this network reaches it from.
func publicIPv4(ctx context.Context) (net.IP, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, publicIPURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("finding the public address: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	for _, line := range strings.Split(string(body), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "ip="); ok {
			if ip := net.ParseIP(v).To4(); ip != nil {
				return ip, nil
			}
		}
	}
	return nil, errors.New("finding the public address: no IPv4 address in the answer")
}
