package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCloudflare answers the two TURN key calls the way Cloudflare does.
type fakeCloudflare struct {
	mu      sync.Mutex
	minted  int
	revoked []string
	ttl     float64
}

func (f *fakeCloudflare) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer secret" {
		http.Error(w, `{"error":"bad token"}`, http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/turn/keys/key1/credentials/generate-ice-servers":
		var body map[string]float64
		json.NewDecoder(r.Body).Decode(&body)
		f.ttl = body["ttl"]
		f.minted++
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"iceServers": []map[string]any{
			{"urls": []string{"stun:stun.cloudflare.com:3478", "stun:stun.cloudflare.com:53"}},
			{"urls": []string{
				"turn:turn.cloudflare.com:3478?transport=udp",
				"turn:turn.cloudflare.com:53?transport=udp",
				"turns:turn.cloudflare.com:443?transport=tcp",
			}, "username": "user" + string(rune('0'+f.minted)), "credential": "pw"},
		}})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/revoke"):
		parts := strings.Split(r.URL.Path, "/")
		f.revoked = append(f.revoked, parts[len(parts)-2])
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func TestCloudflareTURN(t *testing.T) {
	fake := &fakeCloudflare{}
	api := httptest.NewServer(fake)
	defer api.Close()
	cf := &CloudflareTURN{KeyID: "key1", Token: "secret", TTL: 2 * time.Hour, API: api.URL}

	ice := cf.ICE("s1")
	if len(ice) != 2 {
		t.Fatalf("got %d ICE servers, want 2: %+v", len(ice), ice)
	}
	for _, s := range ice {
		for _, u := range s.URLs {
			if strings.Contains(u, ":53") {
				t.Errorf("port 53 kept: %s", u)
			}
		}
	}
	if ice[1].Username != "user1" || ice[1].Credential != "pw" {
		t.Errorf("credentials = %q %q", ice[1].Username, ice[1].Credential)
	}
	if fake.ttl != 7200 {
		t.Errorf("asked for ttl %v, want 7200 seconds", fake.ttl)
	}

	// Ended sessions are revoked by the sweep; live ones are left alone.
	cf.ICE("s2")
	old := relaySweep
	relaySweep = 20 * time.Millisecond
	defer func() { relaySweep = old }()
	stop := make(chan struct{})
	go cf.RevokeEnded(func(s string) bool { return s == "s2" }, stop)
	defer close(stop)
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		fake.mu.Lock()
		revoked := slices.Clone(fake.revoked)
		fake.mu.Unlock()
		if len(revoked) > 0 {
			time.Sleep(100 * time.Millisecond) // a few more sweeps
			fake.mu.Lock()
			revoked = slices.Clone(fake.revoked)
			fake.mu.Unlock()
			if !slices.Equal(revoked, []string{"user1"}) {
				t.Fatalf("revoked %v, want [user1]", revoked)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the ended session was never revoked")
		}
	}
}

func TestCloudflareTURNBadToken(t *testing.T) {
	api := httptest.NewServer(&fakeCloudflare{})
	defer api.Close()
	cf := &CloudflareTURN{KeyID: "key1", Token: "wrong", TTL: time.Hour, API: api.URL}
	if err := cf.Check(t.Context()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("check with a bad token: %v", err)
	}
	if ice := cf.ICE("s1"); ice != nil {
		t.Fatalf("got ICE servers with a bad token: %+v", ice)
	}
}
