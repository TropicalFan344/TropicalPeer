package node

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pion/turn/v5"

	"TropicalPeer/internal/netsim"
	"TropicalPeer/internal/peers"
	"TropicalPeer/internal/rendezvous"
	"TropicalPeer/internal/transfer"
)

// simTURN is a TURN relay on the fake internet, standing in for
// Cloudflare's, and the server's provider of credentials for it.
type simTURN struct {
	addr string

	mu      sync.Mutex
	minted  []string
	revoked []string
}

const simTURNRealm, simTURNUser, simTURNPass = "sim", "user", "pass"

func startSimTURN(t *testing.T, w *simWorld) *simTURN {
	t.Helper()
	const ip = "1.0.0.2"
	pc, err := w.net.ListenPublic(ip + ":3478")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := turn.NewServer(turn.ServerConfig{
		Realm: simTURNRealm,
		AuthHandler: func(ra *turn.RequestAttributes) (string, []byte, bool) {
			return ra.Username, turn.GenerateAuthKey(ra.Username, ra.Realm, simTURNPass), ra.Username == simTURNUser
		},
		PacketConnConfigs: []turn.PacketConnConfig{{PacketConn: pc, RelayAddressGenerator: &simRelayGen{net: w.net, ip: ip}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	return &simTURN{addr: pc.LocalAddr().String()}
}

func (s *simTURN) ICE(session string) []rendezvous.ICEServer {
	s.mu.Lock()
	s.minted = append(s.minted, session)
	s.mu.Unlock()
	return []rendezvous.ICEServer{
		{URLs: []string{"stun:" + s.addr}},
		{URLs: []string{"turn:" + s.addr + "?transport=udp", "turns:" + s.addr + "?transport=tcp"}, Username: simTURNUser, Credential: simTURNPass},
	}
}

func (s *simTURN) Revoke(session string) {
	s.mu.Lock()
	s.revoked = append(s.revoked, session)
	s.mu.Unlock()
}

// simRelayGen hands out relay addresses on the fake internet.
type simRelayGen struct {
	net  *netsim.Network
	ip   string
	mu   sync.Mutex
	next int
}

func (g *simRelayGen) Validate() error { return nil }

func (g *simRelayGen) AllocatePacketConn(turn.AllocateListenerConfig) (net.PacketConn, net.Addr, error) {
	g.mu.Lock()
	g.next++
	port := 40000 + g.next
	g.mu.Unlock()
	c, err := g.net.ListenPublic(fmt.Sprintf("%s:%d", g.ip, port))
	if err != nil {
		return nil, nil, err
	}
	return c, c.LocalAddr(), nil
}

func (g *simRelayGen) AllocateListener(turn.AllocateListenerConfig) (net.Listener, net.Addr, error) {
	return nil, nil, errors.New("no TCP relay")
}

func (g *simRelayGen) AllocateConn(turn.AllocateConnConfig) (net.Conn, error) {
	return nil, errors.New("no TCP relay")
}

// Two symmetric routers and a server that doesn't relay itself: only the
// TURN relay the server hands out can connect them.
func TestTURNPathBetweenSymmetricRouters(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	w := newSimWorld(t, ctx, false)
	relay := startSimTURN(t, w)
	w.srv.AppTURN = relay

	sym := placement{name: "symmetric", kind: netsim.Symmetric}
	sender, _ := w.device(t, ctx, "Sender", sym)
	receiver, downloads := w.device(t, ctx, "Receiver", sym)
	pairDirect(t, ctx, sender, receiver)
	receiver.Peers.Update(sender.Identity.ID(), func(p *peers.Peer) { p.Policy = peers.PolicyAutoAccept })

	file := filepath.Join(t.TempDir(), "photos.zip")
	data := make([]byte, 3<<20)
	rand.Read(data)
	os.WriteFile(file, data, 0o644)

	var relayed bool
	start := time.Now()
	err := sender.Send(ctx, receiver.Identity.ID(), transfer.NewTransferID(), []string{file}, SendOptions{
		OnConnected: func(r bool) { relayed = r },
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(downloads, "photos.zip"))
	if !bytes.Equal(got, data) {
		t.Fatal("received file differs")
	}
	if !relayed {
		t.Error("two symmetric routers connected directly?")
	}
	t.Logf("through the TURN relay in %v", time.Since(start).Round(time.Millisecond))

	relay.mu.Lock()
	minted := len(relay.minted)
	relay.mu.Unlock()
	if minted == 0 {
		t.Fatal("the server never minted TURN credentials")
	}
	if !w.srv.AppTURNLive(relay.minted[0]) {
		t.Error("session ended while both devices are online")
	}
}

// A device that isn't paired gets no answer, so the credentials minted for
// the offer are revoked straight away.
func TestTURNRevokedWhenRefused(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	w := newSimWorld(t, ctx, false)
	relay := startSimTURN(t, w)
	w.srv.AppTURN = relay

	pub := placement{name: "public", public: true}
	stranger, _ := w.device(t, ctx, "Stranger", pub)
	target, _ := w.device(t, ctx, "Target", pub)
	p, err := stranger.waitPresence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Connect(ctx, target.Identity.ID(), false); !errors.Is(err, rendezvous.ErrPeerOffline) {
		t.Fatalf("connect to a device we're not paired with: %v", err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		relay.mu.Lock()
		minted, revoked := len(relay.minted), len(relay.revoked)
		relay.mu.Unlock()
		if minted == 1 && revoked == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("minted %d, revoked %d; want 1 and 1", minted, revoked)
		}
	}
}

func TestPickTURN(t *testing.T) {
	servers := []rendezvous.ICEServer{
		{URLs: []string{"stun:stun.example.com:3478"}},
		{URLs: []string{"turns:turn.example.com:5349?transport=tcp", "turn:turn.example.com:3478?transport=tcp", "turn:turn.example.com:443?transport=udp"}, Username: "u", Credential: "p"},
	}
	addr, user, pass := pickTURN(servers)
	if addr != "turn.example.com:443" || user != "u" || pass != "p" {
		t.Fatalf("pickTURN = %q %q %q", addr, user, pass)
	}
	if addr, _, _ := pickTURN(servers[:1]); addr != "" {
		t.Fatalf("picked %q from STUN only", addr)
	}
}
