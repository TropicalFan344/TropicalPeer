package node

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"TropicalPeer/internal/netsim"
	"TropicalPeer/internal/peers"
	"TropicalPeer/internal/transfer"
)

// Two symmetric routers that keep a socket's own port, and a server that
// doesn't relay: port prediction is the only way through, and it is direct.
func TestPredictBetweenPortKeepingSymmetricRouters(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	w := newSimWorld(t, ctx, false)
	keeping := placement{name: "symmetric-keeps-ports", kind: netsim.Symmetric, keepPorts: true}
	sender, _ := w.device(t, ctx, "Sender", keeping)
	receiver, downloads := w.device(t, ctx, "Receiver", keeping)
	pairDirect(t, ctx, sender, receiver)
	receiver.Peers.Update(sender.Identity.ID(), func(p *peers.Peer) { p.Policy = peers.PolicyAutoAccept })

	file := filepath.Join(t.TempDir(), "photos.zip")
	data := make([]byte, 3<<20)
	rand.Read(data)
	os.WriteFile(file, data, 0o644)

	relayed := true
	start := time.Now()
	var connectedIn time.Duration
	err := sender.Send(ctx, receiver.Identity.ID(), transfer.NewTransferID(), []string{file}, SendOptions{
		OnConnected: func(r bool) { relayed, connectedIn = r, time.Since(start) },
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(downloads, "photos.zip"))
	if !bytes.Equal(got, data) {
		t.Fatal("received file differs")
	}
	if relayed {
		t.Fatal("reported relayed, but the server doesn't relay")
	}
	t.Logf("direct through two symmetric routers, connected in %v", connectedIn.Round(time.Millisecond))
}

// Where the routers don't keep ports, prediction fails quietly and the
// relay still carries the transfer, soon enough.
func TestPredictFailsOverToRelay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	w := newSimWorld(t, ctx, true)
	sym := placement{name: "symmetric", kind: netsim.Symmetric}
	sender, _ := w.device(t, ctx, "Sender", sym)
	receiver, downloads := w.device(t, ctx, "Receiver", sym)
	pairDirect(t, ctx, sender, receiver)
	receiver.Peers.Update(sender.Identity.ID(), func(p *peers.Peer) { p.Policy = peers.PolicyAutoAccept })

	file := filepath.Join(t.TempDir(), "a.bin")
	data := make([]byte, 1<<20)
	rand.Read(data)
	os.WriteFile(file, data, 0o644)

	var relayed bool
	start := time.Now()
	var connectedIn time.Duration
	err := sender.Send(ctx, receiver.Identity.ID(), transfer.NewTransferID(), []string{file}, SendOptions{
		OnConnected: func(r bool) { relayed, connectedIn = r, time.Since(start) },
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(downloads, "a.bin")); !bytes.Equal(got, data) {
		t.Fatal("received file differs")
	}
	if !relayed {
		t.Fatal("connected directly through routers that don't keep ports?")
	}
	if connectedIn > relayDelayPredicted+time.Second {
		t.Errorf("relay took %v; prediction shouldn't hold it up past %v", connectedIn, relayDelayPredicted)
	}
}
