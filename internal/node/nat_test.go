package node

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"TropicalPeer/internal/netsim"
	"TropicalPeer/internal/peers"
	"TropicalPeer/internal/rendezvous"
	"TropicalPeer/internal/server"
	"TropicalPeer/internal/transfer"
)

// placement is where a simulated device sits: on the open internet or
// behind a home router of some kind.
type placement struct {
	name   string
	public bool
	kind   netsim.Kind
}

var placements = []placement{
	{name: "public", public: true},
	{name: "full-cone", kind: netsim.FullCone},
	{name: "addr-restricted", kind: netsim.AddressRestricted},
	{name: "port-restricted", kind: netsim.PortRestricted},
	{name: "symmetric", kind: netsim.Symmetric},
}

// simWorld is one rendezvous server plus a fake internet.
type simWorld struct {
	net       *netsim.Network
	tcpAddr   string
	udpAddr   net.Addr
	srv       *server.Server
	nextIP    int
	serverCtx context.Context
	lastNAT   *netsim.NAT // router of the most recent device (nil if public)
}

func newSimWorld(t *testing.T, ctx context.Context, relay bool) *simWorld {
	t.Helper()
	w := &simWorld{net: netsim.New(), nextIP: 10}
	w.srv = server.New(log.New(io.Discard, "", 0))
	w.srv.Relay = relay
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go w.srv.Serve(ctx, ln)
	w.tcpAddr = ln.Addr().String()
	udp, err := w.net.ListenPublic("1.0.0.1:7777")
	if err != nil {
		t.Fatal(err)
	}
	go w.srv.ServeUDP(ctx, udp)
	w.udpAddr = udp.LocalAddr()
	alt, err := w.net.ListenPublic("1.0.0.1:7877")
	if err != nil {
		t.Fatal(err)
	}
	w.srv.EnableAlt(alt)
	go w.srv.ServeUDPAlt(ctx, alt)
	return w
}

// device starts a node placed in the fake internet and waits until the
// server knows its public UDP address.
func (w *simWorld) device(t *testing.T, ctx context.Context, name string, pl placement, ev ...Events) (*Node, string) {
	t.Helper()
	w.nextIP++
	ip := fmt.Sprintf("2.0.0.%d", w.nextIP)
	var nat *netsim.NAT
	if !pl.public {
		nat = w.net.NewNAT(ip, pl.kind)
	}
	w.lastNAT = nat
	port := 7000
	dir := t.TempDir()
	events := Events{}
	if len(ev) > 0 {
		events = ev[0]
	}
	n, err := New(dir, events)
	if err != nil {
		t.Fatal(err)
	}
	n.ListenPacket = func(int) (net.PacketConn, error) {
		if nat != nil {
			return nat.Listen(), nil
		}
		port++
		return w.net.ListenPublic(fmt.Sprintf("%s:%d", ip, port))
	}
	n.ServerUDPAddr = func() (net.Addr, error) { return w.udpAddr, nil }
	n.SetName(name)
	downloads := filepath.Join(dir, "downloads")
	n.UpdateSettings(Settings{ServerAddr: w.tcpAddr, Port: 0, DownloadDir: downloads})
	go n.Run(ctx)

	deadline := time.Now().Add(10 * time.Second)
	for !n.Network().NAT.Known {
		if time.Now().After(deadline) {
			t.Fatalf("%s never finished router detection (public address %q)", name, n.PublicAddr())
		}
		time.Sleep(20 * time.Millisecond)
	}
	return n, downloads
}

func pairDirect(t *testing.T, ctx context.Context, a, b *Node) {
	t.Helper()
	codes := make(chan string, 1)
	errc := make(chan error, 1)
	go func() {
		_, err := a.Invite(ctx, func(c string) { codes <- c })
		errc <- err
	}()
	if _, err := b.Join(ctx, <-codes); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func TestNATMatrix(t *testing.T) {
	src := t.TempDir()
	file := filepath.Join(src, "photos.zip")
	data := make([]byte, 5<<20)
	rand.Read(data)
	os.WriteFile(file, data, 0o644)

	for _, from := range placements {
		for _, to := range placements {
			t.Run(from.name+"_to_"+to.name, func(t *testing.T) {
				t.Parallel()
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				w := newSimWorld(t, ctx, true)
				sender, _ := w.device(t, ctx, "Sender", from)
				receiver, downloads := w.device(t, ctx, "Receiver", to)
				pairDirect(t, ctx, sender, receiver)
				receiver.Peers.Update(sender.Identity.ID(), func(p *peers.Peer) { p.Policy = peers.PolicyAutoAccept })

				var relayed bool
				var connectedIn time.Duration
				start := time.Now()
				err := sender.Send(ctx, receiver.Identity.ID(), transfer.NewTransferID(), []string{file}, SendOptions{
					OnConnected: func(r bool) { relayed = r; connectedIn = time.Since(start) },
				})
				if err != nil {
					t.Fatalf("send failed: %v", err)
				}
				got, _ := os.ReadFile(filepath.Join(downloads, "photos.zip"))
				if !bytes.Equal(got, data) {
					t.Fatal("received file differs")
				}
				path := "direct"
				if relayed {
					path = "relayed"
				}
				t.Logf("%s → %s: %s, connected in %v, done in %v", from.name, to.name, path,
					connectedIn.Round(time.Millisecond), time.Since(start).Round(time.Millisecond))

				hard := func(p placement) bool { return !p.public && p.kind == netsim.Symmetric }
				if !hard(from) && !hard(to) && relayed {
					t.Errorf("expected a direct path between %s and %s, got relayed", from.name, to.name)
				}
				if hard(from) && hard(to) && !relayed {
					t.Errorf("two symmetric NATs cannot connect directly; got direct?")
				}
				// Router detection must spot every pair that can't go direct, so
				// relayed pairs skip the 2 s head start for direct paths.
				hopeless := rendezvous.DirectHopeless(ptr(sender.Network().NAT), ptr(receiver.Network().NAT))
				if relayed != hopeless {
					t.Errorf("prediction %v but the transfer was relayed=%v", hopeless, relayed)
				}
				if relayed && connectedIn > 1500*time.Millisecond {
					t.Errorf("relayed connection took %v; detection should have skipped the 2 s wait", connectedIn)
				}
			})
		}
	}
}

func ptr[T any](v T) *T { return &v }

func TestRouterDetection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	w := newSimWorld(t, ctx, true)
	want := map[string]rendezvous.NATProfile{
		"public":          {Known: true, Symmetric: false, Lenient: true},
		"full-cone":       {Known: true, Symmetric: false, Lenient: true},
		"addr-restricted": {Known: true, Symmetric: false, Lenient: true},
		"port-restricted": {Known: true, Symmetric: false, Lenient: false},
		"symmetric":       {Known: true, Symmetric: true, Lenient: false},
	}
	for _, pl := range placements {
		n, _ := w.device(t, ctx, pl.name, pl)
		if got := n.Network().NAT; got != want[pl.name] {
			t.Errorf("%s: detected %+v, want %+v", pl.name, got, want[pl.name])
		}
		if n.Network().ServerRTT <= 0 {
			t.Errorf("%s: no server round-trip measured", pl.name)
		}
		// "Check again" must give the same answer, even though the filter
		// test can't be repeated once the second port has been contacted.
		n.Recheck()
		time.Sleep(50 * time.Millisecond)
		for n.Network().Checking {
			time.Sleep(20 * time.Millisecond)
		}
		if got := n.Network().NAT; got != want[pl.name] {
			t.Errorf("%s: re-check gave %+v, want %+v", pl.name, got, want[pl.name])
		}
	}
}

func TestNoRelayFailsClearly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	w := newSimWorld(t, ctx, false)
	sym := placement{name: "symmetric", kind: netsim.Symmetric}
	sender, _ := w.device(t, ctx, "Sender", sym)
	receiver, _ := w.device(t, ctx, "Receiver", sym)
	pairDirect(t, ctx, sender, receiver)
	receiver.Peers.Update(sender.Identity.ID(), func(p *peers.Peer) { p.Policy = peers.PolicyAutoAccept })

	src := filepath.Join(t.TempDir(), "a.txt")
	os.WriteFile(src, []byte("hi"), 0o644)
	err := sender.Send(ctx, receiver.Identity.ID(), transfer.NewTransferID(), []string{src}, SendOptions{})
	if err == nil || !strings.Contains(err.Error(), "doesn't relay") {
		t.Fatalf("want a clear no-relay error, got %v", err)
	}
}

// A transfer that has to start on the relay moves to a direct connection,
// mid-file, as soon as one becomes possible.
func TestUpgradeRelayToDirect(t *testing.T) {
	oldRetry, oldHopeless := upgradeRetry, upgradeRetryHopeless
	upgradeRetry, upgradeRetryHopeless = 300*time.Millisecond, 300*time.Millisecond
	defer func() { upgradeRetry, upgradeRetryHopeless = oldRetry, oldHopeless }()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	w := newSimWorld(t, ctx, true)
	w.srv.RelayRate = 8 << 20 // slow relay, so the transfer is still running when the path opens

	sender, _ := w.device(t, ctx, "Sender", placement{name: "public", public: true})
	pathChanges := make(chan bool, 4)
	receiver, downloads := w.device(t, ctx, "Receiver", placement{name: "symmetric", kind: netsim.Symmetric}, Events{
		OnPathChanged: func(_ peers.Peer, _ string, relayed bool) { pathChanges <- relayed },
	})
	router := w.lastNAT
	pairDirect(t, ctx, sender, receiver)
	receiver.Peers.Update(sender.Identity.ID(), func(p *peers.Peer) { p.Policy = peers.PolicyAutoAccept })

	file := filepath.Join(t.TempDir(), "holiday.mkv")
	data := make([]byte, 48<<20)
	rand.Read(data)
	os.WriteFile(file, data, 0o644)

	var paths []bool
	var bytesAtSwitch int64
	var lastDone int64
	opened := make(chan struct{})
	start := time.Now()
	err := sender.Send(ctx, receiver.Identity.ID(), transfer.NewTransferID(), []string{file}, SendOptions{
		OnConnected: func(relayed bool) {
			paths = append(paths, relayed)
			if !relayed {
				bytesAtSwitch = lastDone
			}
		},
		OnProgress: func(p transfer.Progress) {
			lastDone = p.Done
			// Once a few MB went over the relay, the receiver's router opens up
			// (as when a port forward appears).
			if p.Done > 4<<20 {
				select {
				case <-opened:
				default:
					close(opened)
					router.SetKind(netsim.FullCone)
				}
			}
		},
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(paths) != 2 || !paths[0] || paths[1] {
		t.Fatalf("path sequence = %v, want [relayed, direct]", paths)
	}
	select {
	case relayed := <-pathChanges:
		if relayed {
			t.Fatal("receiver was told the new path is relayed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("receiver never heard about the path change")
	}
	got, _ := os.ReadFile(filepath.Join(downloads, "holiday.mkv"))
	if !bytes.Equal(got, data) {
		t.Fatal("file differs after switching paths mid-file")
	}
	if bytesAtSwitch <= 0 || bytesAtSwitch >= int64(len(data)) {
		t.Fatalf("switched at %d bytes; want mid-file", bytesAtSwitch)
	}
	t.Logf("switched to direct after %d MB of %d MB; total %v (relay alone would take ~%v)",
		bytesAtSwitch>>20, len(data)>>20, time.Since(start).Round(time.Millisecond), time.Duration(len(data)/(8<<20))*time.Second)
}
