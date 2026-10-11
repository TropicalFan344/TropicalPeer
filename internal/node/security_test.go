package node

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net"
	"testing"
	"time"

	"TropicalPeer/internal/identity"
	"TropicalPeer/internal/protocol"
	"TropicalPeer/internal/rendezvous"
)

// Anyone can register a key with the server and ask to be introduced to a
// device. Unless that device is paired with them, they learn nothing: not
// its addresses, not even that it is online.
func TestStrangerLearnsNothing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	addr := startServer(t, ctx)
	alice := startNode(t, ctx, addr, "Alice")
	bob := startNode(t, ctx, addr, "Bob")
	mallory := startNode(t, ctx, addr, "Mallory")
	pair(t, ctx, alice, bob)

	presence := func(n *testNode) *rendezvous.Presence {
		p, _ := n.presence.Load().(*rendezvous.Presence)
		if p == nil {
			t.Fatal("not registered")
		}
		return p
	}

	m, err := presence(mallory).Connect(ctx, bob.Identity.ID(), false)
	if !errors.Is(err, rendezvous.ErrPeerOffline) || len(m.Candidates) != 0 || m.Session != "" {
		t.Fatalf("stranger got %v, %+v; want offline and nothing else", err, m)
	}

	m, err = presence(alice).Connect(ctx, bob.Identity.ID(), false)
	if err != nil || len(m.Candidates) == 0 {
		t.Fatalf("paired device got %v, %+v; want bob's addresses", err, m)
	}

	// A device on an older version (no answers) is introduced as before.
	pub, priv, _ := ed25519.GenerateKey(nil)
	old := &identity.Identity{Name: "Old", PublicKey: pub, PrivateKey: priv}
	nc, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	conn := rendezvous.NewConn(nc)
	now := time.Now().Unix()
	addrs := []string{"192.0.2.7:4000"}
	conn.Send(rendezvous.Msg{Type: rendezvous.TypeRegister, ID: old.ID(), PublicKey: pub, Addrs: addrs, Port: 4000, Time: now,
		Sig: ed25519.Sign(priv, rendezvous.RegisterPayload(old.ID(), now, 4000, addrs))})
	if _, err := conn.RecvMsg(); err != nil {
		t.Fatal(err)
	}
	m, err = presence(alice).Connect(ctx, old.ID(), false)
	if err != nil || len(m.Candidates) == 0 {
		t.Fatalf("older device: %v, %+v; want it introduced right away", err, m)
	}
}

// A transfer that was accepted continues without asking only as the very
// same offer: a different one reusing its ID is asked about like any other.
func TestResumeAutoAcceptNeedsTheSameOffer(t *testing.T) {
	p := loadPartials(t.TempDir())
	accepted := protocol.Offer{TransferID: "t1", Files: []protocol.FileInfo{{Name: "photo.jpg", Size: 100}}}
	p.forPeer("PEER").Started("t1", accepted.Fingerprint(), 0, accepted.Files[0], "photo.jpg.part", "photo.jpg")

	if !p.accepted("PEER", accepted) {
		t.Fatal("the same offer should continue without asking")
	}
	swapped := protocol.Offer{TransferID: "t1", Files: []protocol.FileInfo{{Name: "setup.exe", Size: 100}}}
	bigger := protocol.Offer{TransferID: "t1", Files: []protocol.FileInfo{{Name: "photo.jpg", Size: 100}, {Name: "more.bin", Size: 50 << 30}}}
	for _, o := range []protocol.Offer{swapped, bigger} {
		if p.accepted("PEER", o) {
			t.Fatalf("offer %v reused the transfer ID and skipped the question", o.Files)
		}
	}
	if p.accepted("SOMEONE-ELSE", accepted) {
		t.Fatal("another device's offer skipped the question")
	}
}
