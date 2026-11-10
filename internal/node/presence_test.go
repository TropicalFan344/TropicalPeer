package node

import (
	"context"
	"io"
	"log"
	"net"
	"testing"
	"time"

	"TropicalPeer/internal/server"
)

func waitPresence(t *testing.T, n *testNode, id string, online bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if known, on := n.PeerPresence(id); known && on == online {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	known, on := n.PeerPresence(id)
	t.Fatalf("presence of %s: known=%v online=%v, want online=%v", id, known, on, online)
}

func TestPairedDevicesSeeEachOtherOnline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(log.New(io.Discard, "", 0))
	srv.OfflineGrace = 100 * time.Millisecond
	go srv.Serve(ctx, ln)
	addr := ln.Addr().String()

	alice := startNode(t, ctx, addr, "Alice")
	bobCtx, stopBob := context.WithCancel(ctx)
	bob := startNode(t, bobCtx, addr, "Bob")
	pair(t, ctx, alice, bob)
	aliceID, bobID := alice.Identity.ID(), bob.Identity.ID()

	// Pairing re-sends both lists, so each sees the other at once, and
	// remembers having seen it.
	waitPresence(t, alice, bobID, true)
	waitPresence(t, bob, aliceID, true)
	if p, _ := alice.Peers.Get(bobID); p.LastSeen == 0 {
		t.Fatal("alice didn't record seeing bob")
	}

	// Bob quits: after the server's grace period Alice reads him as offline.
	stopBob()
	waitPresence(t, alice, bobID, false)

	// Alice removes Bob: he's no longer hers to know about.
	if err := alice.RemovePeer(bobID); err != nil {
		t.Fatal(err)
	}
	if known, _ := alice.PeerPresence(bobID); known {
		t.Fatal("presence still known for a removed peer")
	}
}
