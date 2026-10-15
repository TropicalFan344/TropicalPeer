package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeCloudflare is the part of Cloudflare's API the server uses: one zone,
// its DNS records, and the address trace.
type fakeCloudflare struct {
	t     *testing.T
	token string
	zone  string // zone name, e.g. example.win
	ip    string // what the trace says

	mu      sync.Mutex
	records map[string]cfRecord
	next    int
}

func (f *fakeCloudflare) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/cdn-cgi/trace" {
		fmt.Fprintf(w, "fl=1\nip=%s\nts=1\n", f.ip)
		return
	}
	reply := func(status int, result any) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{"success": status < 300, "result": result,
			"errors": []map[string]any{}})
	}
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"success":false,"errors":[{"code":9109,"message":"Invalid access token"}]}`)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/client/v4")
	switch {
	case path == "/zones":
		if r.URL.Query().Get("name") == f.zone {
			reply(200, []map[string]string{{"id": "z1"}})
		} else {
			reply(200, []any{})
		}
	case path == "/zones/z1/dns_records" && r.Method == http.MethodGet:
		out := []cfRecord{}
		for _, rec := range f.records {
			if rec.Type == r.URL.Query().Get("type") && rec.Name == r.URL.Query().Get("name") {
				out = append(out, rec)
			}
		}
		reply(200, out)
	case path == "/zones/z1/dns_records" && r.Method == http.MethodPost:
		var rec cfRecord
		json.NewDecoder(r.Body).Decode(&rec)
		f.next++
		rec.ID = fmt.Sprint("r", f.next)
		f.records[rec.ID] = rec
		reply(200, rec)
	case strings.HasPrefix(path, "/zones/z1/dns_records/"):
		id := strings.TrimPrefix(path, "/zones/z1/dns_records/")
		rec, ok := f.records[id]
		if !ok {
			reply(404, nil)
			return
		}
		switch r.Method {
		case http.MethodPatch:
			json.NewDecoder(r.Body).Decode(&rec)
			rec.ID = id
			f.records[id] = rec
			reply(200, rec)
		case http.MethodDelete:
			delete(f.records, id)
			reply(200, map[string]string{"id": id})
		}
	default:
		f.t.Errorf("unexpected %s %s", r.Method, r.URL)
		reply(404, nil)
	}
}

func (f *fakeCloudflare) find(typ, name string) []cfRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []cfRecord
	for _, r := range f.records {
		if r.Type == typ && r.Name == name {
			out = append(out, r)
		}
	}
	return out
}

func newFakeCloudflare(t *testing.T) *fakeCloudflare {
	f := &fakeCloudflare{t: t, token: "tok", zone: "example.win", ip: "203.0.113.7", records: map[string]cfRecord{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	oldAPI, oldIP := cloudflareAPI, publicIPURL
	cloudflareAPI, publicIPURL = srv.URL+"/client/v4", srv.URL+"/cdn-cgi/trace"
	t.Cleanup(func() { srv.Close(); cloudflareAPI, publicIPURL = oldAPI, oldIP })
	return f
}

func TestCloudflareKeepsAddress(t *testing.T) {
	f := newFakeCloudflare(t)
	ctx := context.Background()
	c := &cloudflare{domain: "share.example.win", token: "tok"}

	// No record yet: one is made, out of the proxy.
	if err := c.updateIP(ctx); err != nil {
		t.Fatal(err)
	}
	recs := f.find("A", "share.example.win")
	if len(recs) != 1 || recs[0].Content != "203.0.113.7" || recs[0].Proxied == nil || *recs[0].Proxied {
		t.Fatalf("after first update: %+v", recs)
	}

	// The address changes, and someone turned the proxy on: both put right.
	f.mu.Lock()
	on := true
	r := f.records[recs[0].ID]
	r.Proxied = &on
	f.records[r.ID] = r
	f.ip = "198.51.100.9"
	f.mu.Unlock()
	if err := c.updateIP(ctx); err != nil {
		t.Fatal(err)
	}
	recs = f.find("A", "share.example.win")
	if len(recs) != 1 || recs[0].Content != "198.51.100.9" || *recs[0].Proxied {
		t.Fatalf("after change: %+v", recs)
	}
}

func TestCloudflareChallenge(t *testing.T) {
	f := newFakeCloudflare(t)
	ctx := context.Background()
	c := &cloudflare{domain: "share.example.win", token: "tok"}
	name := "_acme-challenge.share.example.win"

	// A leftover from an earlier try goes; only the new value is there.
	for _, v := range []string{"old", "new"} {
		if err := c.setTXT(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	if recs := f.find("TXT", name); len(recs) != 1 || recs[0].Content != "new" {
		t.Fatalf("challenge records: %+v", recs)
	}
	if err := c.clearTXT(ctx); err != nil {
		t.Fatal(err)
	}
	if recs := f.find("TXT", name); len(recs) != 0 {
		t.Fatalf("left after clearing: %+v", recs)
	}
}

func TestCloudflareErrors(t *testing.T) {
	newFakeCloudflare(t)
	ctx := context.Background()

	err := (&cloudflare{domain: "share.example.win", token: "wrong"}).updateIP(ctx)
	if err == nil || !strings.Contains(err.Error(), "may edit DNS") {
		t.Fatalf("wrong token: %v", err)
	}
	err = (&cloudflare{domain: "share.other.win", token: "tok"}).updateIP(ctx)
	if err == nil || !strings.Contains(err.Error(), "no zone") {
		t.Fatalf("other zone: %v", err)
	}
}
