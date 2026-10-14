package server

import (
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/turn/v5"
)

// A client with the credentials ICE hands out gets a relay allocation.
func TestTURNAllocates(t *testing.T) {
	tn, err := StartTURN(TURNConfig{Port: 34780, Host: "127.0.0.1", PublicIP: net.IPv4(127, 0, 0, 1), RelayMin: 34790, RelayMax: 34799, AllowLAN: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tn.Close()
	ice := tn.ICE("session1")
	creds := ice[1]

	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	addr := strings.TrimPrefix(strings.Split(creds.URLs[0], "?")[0], "turn:")
	c, err := turn.NewClient(&turn.ClientConfig{
		STUNServerAddr: addr, TURNServerAddr: addr, Conn: conn,
		Username: creds.Username, Password: creds.Credential, Realm: "tropicalpeer",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Listen(); err != nil {
		t.Fatal(err)
	}
	relay, err := c.Allocate()
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	defer relay.Close()
	t.Logf("relay %v", relay.LocalAddr())
}

func turnClient(t *testing.T, addr, user, pass string) *turn.Client {
	t.Helper()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	c, err := turn.NewClient(&turn.ClientConfig{STUNServerAddr: addr, TURNServerAddr: addr, Conn: conn, Username: user, Password: pass, Realm: "tropicalpeer"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	if err := c.Listen(); err != nil {
		t.Fatal(err)
	}
	return c
}

// Relayed traffic is held to the rate; a session gets a few allocations, not more.
func TestTURNRateAndQuota(t *testing.T) {
	const rate = 1 << 20
	tn, err := StartTURN(TURNConfig{Port: 34781, Host: "127.0.0.1", PublicIP: net.IPv4(127, 0, 0, 1), RelayMin: 34800, RelayMax: 34819, AllowLAN: true, Rate: rate})
	if err != nil {
		t.Fatal(err)
	}
	defer tn.Close()
	creds := tn.ICE("s1")[1]
	addr := strings.TrimPrefix(strings.Split(creds.URLs[0], "?")[0], "turn:")

	c := turnClient(t, addr, creds.Username, creds.Credential)
	relay, err := c.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	// The relay won't reach loopback, so the peer listens on a LAN address.
	peer, err := net.ListenPacket("udp4", net.JoinHostPort(lanIP(t), "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	// The peer must speak first for the relay to permit it.
	peer.WriteTo([]byte("hi"), relay.LocalAddr())
	relay.WriteTo([]byte("hi"), peer.LocalAddr())

	var got atomic.Int64
	go func() {
		buf := make([]byte, 2048)
		for {
			n, _, err := peer.ReadFrom(buf)
			if err != nil {
				return
			}
			got.Add(int64(n))
		}
	}()
	chunk := make([]byte, 1000)
	start := time.Now()
	for sent := 0; sent < 5*rate; sent += len(chunk) {
		relay.WriteTo(chunk, peer.LocalAddr())
		if sent%(64*1000) == 0 {
			time.Sleep(10 * time.Millisecond) // don't just overflow local buffers
		}
	}
	time.Sleep(300 * time.Millisecond)
	elapsed := time.Since(start).Seconds()
	limit := float64(rate)*(1+elapsed) + 64*1024 // a second's burst, the rate since, and slack
	if g := float64(got.Load()); g > limit || g < rate/2 {
		t.Fatalf("relayed %.0f bytes in %.1fs; want about %.0f at most", g, elapsed, limit)
	}

	// Five more for this session are fine (six in all); the next is refused.
	for i := 0; i < maxAllocationsPerSession-1; i++ {
		r, err := turnClient(t, addr, creds.Username, creds.Credential).Allocate()
		if err != nil {
			t.Fatalf("allocation %d: %v", i+2, err)
		}
		defer r.Close()
	}
	if _, err := turnClient(t, addr, creds.Username, creds.Credential).Allocate(); err == nil {
		t.Fatal("a seventh allocation for one session was allowed")
	}
}

func lanIP(t *testing.T) string {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
			return n.IP.String()
		}
	}
	t.Skip("no LAN address to relay to")
	return ""
}
