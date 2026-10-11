package appcore

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log"
	"net"
	"path/filepath"
	"testing"
	"time"

	"TropicalPeer/internal/netsim"
	"TropicalPeer/internal/node"
	"TropicalPeer/internal/peers"
	"TropicalPeer/internal/server"
	"TropicalPeer/internal/transfer"
)

// relayedCore is a whole App whose node sits behind a symmetric router in a
// simulated internet, so its transfers go through the server's relay.
func relayedCore(t *testing.T, ctx context.Context, sim *netsim.Network, tcpAddr string, udpAddr net.Addr, ip, name string) (*Core, *recorder) {
	t.Helper()
	rec := &recorder{}
	c := New(t.TempDir(), nil)
	c.ctx = ctx
	c.emitFn = rec.emit
	router := sim.NewNAT(ip, netsim.Symmetric)
	dir := t.TempDir()
	online := make(chan struct{}, 1)
	n, err := node.New(dir, node.Events{
		OnServerStatus: func(ok bool, _ error) {
			if ok {
				select {
				case online <- struct{}{}:
				default:
				}
			}
		},
		OnOffer: c.onOffer, OnAccepted: c.onAccepted, OnProgress: c.onIncomingProgress,
		OnReceived: c.onReceived, OnPathChanged: c.onPathChanged,
		OnResumeRequest: c.onResumeRequest, OnCancelNotice: c.onCancelNotice,
	})
	if err != nil {
		t.Fatal(err)
	}
	n.ListenPacket = func(int) (net.PacketConn, error) { return router.Listen(), nil }
	n.ServerUDPAddr = func() (net.Addr, error) { return udpAddr, nil }
	n.SetName(name)
	n.UpdateSettings(node.Settings{ServerAddr: tcpAddr, Port: 0, DownloadDir: filepath.Join(dir, "downloads")})
	c.node = n
	go n.Run(ctx)
	<-online
	for deadline := time.Now().Add(10 * time.Second); !n.Network().NAT.Known; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("router detection never finished")
		}
	}
	return c, rec
}

func TestPauseOverRelayInTheApp(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	sim := netsim.New()
	srv := server.New(log.New(io.Discard, "", 0))
	srv.RelayRate = 4 << 20
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go srv.Serve(ctx, ln)
	udp, _ := sim.ListenPublic("1.0.0.1:7777")
	go srv.ServeUDP(ctx, udp)
	alt, _ := sim.ListenPublic("1.0.0.1:7877")
	srv.EnableAlt(alt)
	go srv.ServeUDPAlt(ctx, alt)

	me, myRec := relayedCore(t, ctx, sim, ln.Addr().String(), udp.LocalAddr(), "2.0.0.11", "Me")
	friend, friendRec := relayedCore(t, ctx, sim, ln.Addr().String(), udp.LocalAddr(), "2.0.0.12", "Friend")
	pairNodes(t, ctx, me.node, friend.node)
	friend.node.Peers.Update(me.node.Identity.ID(), func(p *peers.Peer) { p.Policy = peers.PolicyAutoAccept })

	data := make([]byte, 40<<20)
	rand.Read(data)
	sent, err := me.SendSources(friend.node.Identity.ID(), []transfer.Source{slowSource("driver.exe", data)})
	if err != nil {
		t.Fatal(err)
	}
	in := friendRec.latestIncoming(t, 0)
	myRec.waitFor(t, sent.ID, func(d TransferDTO) bool { return d.Done > 1<<20 && d.Relayed })

	check := func(step string, d TransferDTO) {
		t.Helper()
		if d.Status != StatusPaused {
			t.Fatalf("%s: row is %q %q, want paused", step, d.Status, d.Error)
		}
	}
	for round := 1; round <= 2; round++ {
		fm, mm := friendRec.mark(), myRec.mark()
		if round == 1 {
			friend.PauseTransfer(in.ID) // the receiver pauses
		} else {
			me.PauseTransfer(sent.ID) // the sender pauses
		}
		settled := func(d TransferDTO) bool { return !d.active() }
		check(fmt.Sprintf("round %d, sender", round), myRec.waitSince(t, mm, sent.ID, settled))
		check(fmt.Sprintf("round %d, receiver", round), friendRec.waitSince(t, fm, in.ID, settled))
		time.Sleep(time.Second) // nothing may turn it into a failure afterwards
		check(fmt.Sprintf("round %d, sender later", round), myRec.waitSince(t, mm, sent.ID, func(TransferDTO) bool { return true }))
		check(fmt.Sprintf("round %d, receiver later", round), friendRec.waitSince(t, fm, in.ID, func(TransferDTO) bool { return true }))

		fm, mm = friendRec.mark(), myRec.mark()
		if _, err := me.ResumeTransfer(sent.ID); err != nil {
			t.Fatal(err)
		}
		myRec.waitSince(t, mm, sent.ID, func(d TransferDTO) bool { return d.Status == StatusSending })
		friendRec.waitSince(t, fm, in.ID, func(d TransferDTO) bool { return d.Status == StatusReceiving })
		time.Sleep(500 * time.Millisecond)
	}
}
