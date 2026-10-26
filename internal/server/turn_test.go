package server

import (
	"net"
	"strconv"
	"strings"
	"sync"
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

// Credentials only work for sessions the server says are live, and a
// session's relay reaches private addresses only where its own two ends
// are; ended sessions lose their credentials.
func TestTURNSessionGate(t *testing.T) {
	lan := net.ParseIP(lanIP(t))
	old := relaySweep
	relaySweep = 50 * time.Millisecond
	defer func() { relaySweep = old }()
	var mu sync.Mutex
	live := map[string][]net.IP{"mine": {lan}, "other": {net.IPv4(10, 1, 2, 3)}}
	tn, err := StartTURN(TURNConfig{Port: 34782, Host: "127.0.0.1", PublicIP: net.IPv4(127, 0, 0, 1), RelayMin: 34820, RelayMax: 34829,
		Session: func(s string) ([]net.IP, bool) {
			mu.Lock()
			defer mu.Unlock()
			ips, ok := live[s]
			return ips, ok
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer tn.Close()
	allocate := func(session string) (*turn.Client, net.PacketConn, error) {
		creds := tn.ICE(session)[1]
		addr := strings.TrimPrefix(strings.Split(creds.URLs[0], "?")[0], "turn:")
		c := turnClient(t, addr, creds.Username, creds.Credential)
		r, err := c.Allocate()
		return c, r, err
	}

	if _, _, err := allocate("stranger"); err == nil {
		t.Fatal("credentials for a session nobody admitted worked")
	}

	peer, err := net.ListenPacket("udp4", net.JoinHostPort(lan.String(), "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	c, relay, err := allocate("mine")
	if err != nil {
		t.Fatalf("an admitted session: %v", err)
	}
	if err := c.CreatePermission(peer.LocalAddr()); err != nil {
		t.Fatalf("the session's own private address: %v", err)
	}
	if err := c.CreatePermission(&net.UDPAddr{IP: net.IPv4(192, 168, 77, 1), Port: 5351}); err == nil {
		t.Fatal("reached another private address (a router's NAT-PMP port, say)")
	}
	for _, ip := range []net.IP{net.IPv4(100, 64, 0, 1), net.IPv4(127, 0, 0, 1), net.IPv4(255, 255, 255, 255), net.IPv4(224, 0, 0, 251)} {
		if err := c.CreatePermission(&net.UDPAddr{IP: ip, Port: 9}); err == nil {
			t.Fatalf("reached %v", ip)
		}
	}
	if err := c.CreatePermission(&net.UDPAddr{IP: net.IPv4(8, 8, 8, 8), Port: 9}); err != nil {
		t.Fatalf("a public address: %v", err)
	}

	c2, _, err := allocate("other")
	if err != nil {
		t.Fatal(err)
	}
	if err := c2.CreatePermission(peer.LocalAddr()); err == nil {
		t.Fatal("another session reached this session's private address")
	}

	// The sender is done with the session: its credentials stop working.
	mu.Lock()
	delete(live, "mine")
	mu.Unlock()
	if err := c.CreatePermission(&net.UDPAddr{IP: net.IPv4(8, 8, 4, 4), Port: 9}); err == nil {
		t.Fatal("an ended session's credentials still worked")
	}

	// And its relay port is free again soon, not at the end of the
	// allocation's lifetime: a home server may have only ten.
	port := relay.LocalAddr().(*net.UDPAddr).Port
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if pc, err := net.ListenPacket("udp4", net.JoinHostPort("0.0.0.0", strconv.Itoa(port))); err == nil {
			pc.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("relay port %d still taken after the session ended", port)
		}
	}
}

// After the public address changes, new allocations announce the new one.
func TestTURNPublicIPChanges(t *testing.T) {
	tn, err := StartTURN(TURNConfig{Port: 34880, Host: "127.0.0.1", PublicIP: net.IPv4(127, 0, 0, 1), RelayMin: 34890, RelayMax: 34899, AllowLAN: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tn.Close()
	allocate := func(session string) net.IP {
		creds := tn.ICE(session)[1]
		addr := strings.TrimPrefix(strings.Split(creds.URLs[0], "?")[0], "turn:")
		relay, err := turnClient(t, addr, creds.Username, creds.Credential).Allocate()
		if err != nil {
			t.Fatalf("allocate: %v", err)
		}
		defer relay.Close()
		return relay.LocalAddr().(*net.UDPAddr).IP
	}
	if ip := allocate("before"); !ip.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Fatalf("relay announced %v, want 127.0.0.1", ip)
	}
	tn.SetPublicIP(net.IPv4(203, 0, 113, 7))
	if ip := allocate("after"); !ip.Equal(net.IPv4(203, 0, 113, 7)) {
		t.Fatalf("relay announced %v after the change, want 203.0.113.7", ip)
	}
}
