package appcore

import (
	"context"
	"crypto/rand"
	"io"
	"log"
	"net"
	"testing"
	"time"

	"TropicalPeer/internal/node"
	"TropicalPeer/internal/peers"
	"TropicalPeer/internal/server"
	"TropicalPeer/internal/transfer"
)

// Pause and resume through a real relay (real sockets, the server's UDP
// relay), as between two devices that can't reach each other directly.
func TestPauseThroughRealRelay(t *testing.T) {
	node.DevRelayOnly = true
	defer func() { node.DevRelayOnly = false }()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	srv := server.New(log.New(io.Discard, "", 0))
	srv.RelayRate = 8 << 20
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+portOf(udp.LocalAddr()))
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ctx, ln)
	go srv.ServeUDP(ctx, udp)
	addr := ln.Addr().String()

	me, myRec, _ := testCore(t, ctx, addr, "Me")
	friend, friendRec, _ := testCore(t, ctx, addr, "Friend")
	pairNodes(t, ctx, me.node, friend.node)
	friend.node.Peers.Update(me.node.Identity.ID(), func(p *peers.Peer) { p.Policy = peers.PolicyAutoAccept })

	data := make([]byte, 64<<20)
	rand.Read(data)
	sent, err := me.SendSources(friend.node.Identity.ID(), []transfer.Source{slowSource("driver.exe", data)})
	if err != nil {
		t.Fatal(err)
	}
	in := friendRec.latestIncoming(t, 0)
	myRec.waitFor(t, sent.ID, func(d TransferDTO) bool { return d.Done > 1<<20 })

	settled := func(d TransferDTO) bool { return !d.active() }
	for round, receiverPauses := range []bool{true, false, true} {
		fm, mm := friendRec.mark(), myRec.mark()
		if receiverPauses {
			friend.PauseTransfer(in.ID)
		} else {
			me.PauseTransfer(sent.ID)
		}
		mine := myRec.waitSince(t, mm, sent.ID, settled)
		theirs := friendRec.waitSince(t, fm, in.ID, settled)
		t.Logf("round %d (receiver pauses=%v): sender %s relayed=%v %q, receiver %s %q", round+1, receiverPauses, mine.Status, mine.Relayed, mine.Error, theirs.Status, theirs.Error)
		if mine.Status != StatusPaused || theirs.Status != StatusPaused {
			t.Fatalf("round %d: sender %q %q, receiver %q %q; want both paused", round+1, mine.Status, mine.Error, theirs.Status, theirs.Error)
		}
		time.Sleep(time.Second)
		fm, mm = friendRec.mark(), myRec.mark()
		if _, err := friend.ResumeTransfer(in.ID); err != nil {
			t.Fatal(err)
		}
		myRec.waitSince(t, mm, sent.ID, func(d TransferDTO) bool { return d.Status == StatusSending })
		friendRec.waitSince(t, fm, in.ID, func(d TransferDTO) bool { return d.Status == StatusReceiving })
		time.Sleep(700 * time.Millisecond)
	}
}

func portOf(a net.Addr) string {
	_, p, _ := net.SplitHostPort(a.String())
	return p
}
