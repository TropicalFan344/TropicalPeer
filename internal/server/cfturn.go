package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"TropicalPeer/internal/rendezvous"
)

// CloudflareTURN hands out credentials for Cloudflare's TURN relay. The key
// ID and API token never leave this server: devices and browsers get short
// lived credentials minted for one session, and a session's credentials are
// revoked once it ends.
type CloudflareTURN struct {
	KeyID string
	Token string
	TTL   time.Duration // how long minted credentials last

	// API is Cloudflare's endpoint, for tests.
	API string

	mu     sync.Mutex
	issued map[string][]string // usernames minted, by session
}

const cloudflareRTCAPI = "https://rtc.live.cloudflare.com/v1"

// mintTimeout bounds one credentials request: an introduction or a link
// admission waits for it.
const mintTimeout = 3 * time.Second

func (c *CloudflareTURN) api() string {
	if c.API != "" {
		return c.API
	}
	return cloudflareRTCAPI
}

// ICE mints credentials for session: Cloudflare's STUN and TURN servers in
// the shape browsers take. Nil when Cloudflare can't be reached, so callers
// fall back to whatever else they have.
func (c *CloudflareTURN) ICE(session string) []rendezvous.ICEServer {
	ctx, cancel := context.WithTimeout(context.Background(), mintTimeout)
	defer cancel()
	servers, user, err := c.mint(ctx)
	if err != nil {
		return nil
	}
	c.mu.Lock()
	if c.issued == nil {
		c.issued = map[string][]string{}
	}
	c.issued[session] = append(c.issued[session], user)
	c.mu.Unlock()
	return servers
}

func (c *CloudflareTURN) mint(ctx context.Context) ([]rendezvous.ICEServer, string, error) {
	body, _ := json.Marshal(map[string]int64{"ttl": int64(c.TTL / time.Second)})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.api()+"/turn/keys/"+url.PathEscape(c.KeyID)+"/credentials/generate-ice-servers", bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("cloudflare turn: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return nil, "", fmt.Errorf("cloudflare turn: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var out struct {
		ICEServers []struct {
			URLs       any    `json:"urls"` // a string or a list
			Username   string `json:"username"`
			Credential string `json:"credential"`
		} `json:"iceServers"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, "", fmt.Errorf("cloudflare turn: %w", err)
	}
	var servers []rendezvous.ICEServer
	user := ""
	for _, s := range out.ICEServers {
		var urls []string
		switch u := s.URLs.(type) {
		case string:
			urls = []string{u}
		case []any:
			for _, v := range u {
				if str, ok := v.(string); ok {
					urls = append(urls, str)
				}
			}
		}
		// Port 53 is blocked by browsers and many networks; trying it only
		// slows gathering down.
		urls = slices.DeleteFunc(urls, func(u string) bool { return strings.Contains(u, ":53?") || strings.HasSuffix(u, ":53") })
		if len(urls) == 0 {
			continue
		}
		servers = append(servers, rendezvous.ICEServer{URLs: urls, Username: s.Username, Credential: s.Credential})
		if s.Username != "" {
			user = s.Username
		}
	}
	if user == "" {
		return nil, "", fmt.Errorf("cloudflare turn: no TURN server in the answer")
	}
	return servers, user, nil
}

// Revoke ends the credentials minted for session.
func (c *CloudflareTURN) Revoke(session string) {
	c.mu.Lock()
	users := c.issued[session]
	delete(c.issued, session)
	c.mu.Unlock()
	for _, u := range users {
		c.revoke(u)
	}
}

func (c *CloudflareTURN) revoke(user string) error {
	ctx, cancel := context.WithTimeout(context.Background(), mintTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.api()+"/turn/keys/"+url.PathEscape(c.KeyID)+"/credentials/"+url.PathEscape(user)+"/revoke", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// RevokeEnded revokes the credentials of every session live no longer
// reports, every relaySweep until stop closes.
func (c *CloudflareTURN) RevokeEnded(live func(session string) bool, stop <-chan struct{}) {
	t := time.NewTicker(relaySweep)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		c.mu.Lock()
		var ended []string
		for s := range c.issued {
			if !live(s) {
				ended = append(ended, s)
			}
		}
		c.mu.Unlock()
		for _, s := range ended {
			c.Revoke(s)
		}
	}
}

// Check mints and revokes one credential, to tell at startup whether the
// key ID and token work.
func (c *CloudflareTURN) Check(ctx context.Context) error {
	_, user, err := c.mint(ctx)
	if err != nil {
		return err
	}
	return c.revoke(user)
}
