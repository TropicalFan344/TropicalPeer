package appcore

import (
	"context"
	"io"
	"log"
	"net"
	"testing"
	"time"

	"TropicalPeer/internal/node"
	"TropicalPeer/internal/server"
	"TropicalPeer/internal/transfer"
)

func coreEvents(c *Core) node.Events {
	return node.Events{
		OnOffer:         c.onOffer,
		OnAccepted:      c.onAccepted,
		OnProgress:      c.onIncomingProgress,
		OnReceived:      c.onReceived,
		OnPathChanged:   c.onPathChanged,
		OnResumeRequest: c.onResumeRequest,
		OnCancelNotice:  c.onCancelNotice,
		OnRemovedUs:     c.onRemovedUs,
	}
}

func testServer(t *testing.T, ctx context.Context) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.New(log.New(io.Discard, "", 0)).Serve(ctx, ln)
	return ln.Addr().String()
}

func waitRemovedUs(t *testing.T, c *Core, peerID string) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if p, err := c.node.Peers.Get(peerID); err == nil && p.RemovedUs {
			return
		}
	}
	t.Fatal("never learned it was removed")
}

// The other device removes us while both are online: we hear at once, and
// sending says why it can't instead of "offline".
func TestRemovedWhileOnline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	addr := testServer(t, ctx)
	me, myRec, _ := testCore(t, ctx, addr, "Me")
	friend, _, _ := testCore(t, ctx, addr, "Friend")
	pairNodes(t, ctx, me.node, friend.node)
	friendID := friend.node.Identity.ID()

	if err := friend.RemovePeer(me.node.Identity.ID()); err != nil {
		t.Fatal(err)
	}
	waitRemovedUs(t, me, friendID)
	if _, ok := friend.node.Peers.RemovedPeer(me.node.Identity.ID()); ok {
		t.Fatal("the notice was delivered but is still pending")
	}
	if !myRec.saw("peer:removedUs") {
		t.Fatal("no peer:removedUs event for the toast")
	}
	d, err := me.SendSources(friendID, []transfer.Source{slowSource("a.txt", []byte("hello"))})
	if err != nil {
		t.Fatal(err)
	}
	final := myRec.waitFinal(t, d.ID)
	if want := me.T("transfer.removedYou", "name", "Friend"); final.Status != StatusFailed || final.Error != want {
		t.Fatalf("final = %q %q, want failed %q", final.Status, final.Error, want)
	}

	// Pairing again clears it, on both sides.
	pairNodes(t, ctx, me.node, friend.node)
	if p, _ := me.node.Peers.Get(friendID); p.RemovedUs {
		t.Fatal("still marked removed after pairing again")
	}
}

// We were offline when the other device removed us: the first time we try
// to send, it tells us, and the failed row says why.
func TestRemovedWhileOffline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	addr := testServer(t, ctx)
	friend, _, _ := testCore(t, ctx, addr, "Friend")
	friendID := friend.node.Identity.ID()

	myRec := &recorder{}
	me := New(t.TempDir(), nil)
	me.ctx = ctx
	me.emitFn = myRec.emit
	myDir := t.TempDir()
	firstCtx, stopFirst := context.WithCancel(ctx)
	me.node, _ = startNodeIn(t, firstCtx, myDir, addr, "Me", coreEvents(me))
	myID := me.node.Identity.ID()
	pairNodes(t, ctx, me.node, friend.node)
	stopFirst()
	time.Sleep(500 * time.Millisecond) // let the profile lock go

	if err := friend.RemovePeer(myID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, ok := friend.node.Peers.RemovedPeer(myID); !ok {
		t.Fatal("the notice should still be pending while we're offline")
	}

	me.node, _ = startNodeIn(t, ctx, myDir, addr, "Me", coreEvents(me))
	d, err := me.SendSources(friendID, []transfer.Source{slowSource("a.txt", []byte("hello"))})
	if err != nil {
		t.Fatal(err)
	}
	want := me.T("transfer.removedYou", "name", "Friend")
	got := myRec.waitFor(t, d.ID, func(d TransferDTO) bool { return d.Error == want })
	if got.Status != StatusFailed {
		t.Fatalf("row = %+v", got)
	}
	waitRemovedUs(t, me, friendID)
}

// saw reports whether an event of that name was emitted.
func (r *recorder) saw(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, n := range r.names {
		if n == name {
			return true
		}
	}
	return false
}
