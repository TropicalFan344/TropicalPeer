package node

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"TropicalPeer/internal/netsim"
	"TropicalPeer/internal/peers"
	"TropicalPeer/internal/protocol"
	"TropicalPeer/internal/transfer"
)

// Pausing a transfer that runs through the relay: the other side must see
// "paused by the other device", not a raw connection error.
func TestPauseOverRelay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	w := newSimWorld(t, ctx, true)
	w.srv.RelayRate = 4 << 20 // slow, so there's time to pause

	received := make(chan error, 4)
	pauses := make(chan func(), 4)
	sender, _ := w.device(t, ctx, "Sender", placement{name: "symmetric", kind: netsim.Symmetric})
	receiver, _ := w.device(t, ctx, "Receiver", placement{name: "symmetric", kind: netsim.Symmetric}, Events{
		OnAccepted: func(_ peers.Peer, _ protocol.Offer, _ string, _ bool, _, pause func()) { pauses <- pause },
		OnReceived: func(_ peers.Peer, _ protocol.Offer, _ []transfer.Result, err error) { received <- err },
	})
	pairDirect(t, ctx, sender, receiver)
	receiver.Peers.Update(sender.Identity.ID(), func(p *peers.Peer) { p.Policy = peers.PolicyAutoAccept })

	file := filepath.Join(t.TempDir(), "driver.exe")
	data := make([]byte, 40<<20)
	rand.Read(data)
	os.WriteFile(file, data, 0o644)

	t.Run("sender pauses", func(t *testing.T) {
		sendCtx, stop := context.WithCancelCause(ctx)
		var relayedSeen bool
		errc := make(chan error, 1)
		go func() {
			errc <- sender.Send(sendCtx, receiver.Identity.ID(), transfer.NewTransferID(), []string{file}, SendOptions{
				OnConnected: func(relayed bool) { relayedSeen = relayed },
				OnProgress: func(p transfer.Progress) {
					if p.Done > 2<<20 {
						stop(transfer.ErrPaused)
					}
				},
			})
		}()
		<-errc
		<-pauses
		if !relayedSeen {
			t.Fatal("expected the transfer to run through the relay")
		}
		select {
		case err := <-received:
			if !transfer.IsPausedByPeer(err) {
				t.Fatalf("receiver got %v (%T), want paused by peer", err, err)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("receiver never finished")
		}
	})

	t.Run("receiver pauses", func(t *testing.T) {
		errc := make(chan error, 1)
		paused := false
		go func() {
			errc <- sender.Send(ctx, receiver.Identity.ID(), transfer.NewTransferID(), []string{file}, SendOptions{
				OnProgress: func(p transfer.Progress) {
					if p.Done > 2<<20 && !paused {
						paused = true
						go (<-pauses)()
					}
				},
			})
		}()
		select {
		case err := <-errc:
			if !transfer.IsPausedByPeer(err) {
				t.Fatalf("sender got %v (%T), want paused by peer", err, err)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("sender never finished")
		}
		<-received
	})
}

// The same after the transfer moved from the relay to a direct path.
func TestPauseAfterUpgradeFromRelay(t *testing.T) {
	oldRetry, oldHopeless := upgradeRetry, upgradeRetryHopeless
	upgradeRetry, upgradeRetryHopeless = 300*time.Millisecond, 300*time.Millisecond
	defer func() { upgradeRetry, upgradeRetryHopeless = oldRetry, oldHopeless }()

	for _, who := range []string{"sender", "receiver"} {
		t.Run(who+" pauses", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			w := newSimWorld(t, ctx, true)
			w.srv.RelayRate = 4 << 20
			received := make(chan error, 4)
			pauses := make(chan func(), 4)
			sender, _ := w.device(t, ctx, "Sender", placement{name: "public", public: true})
			receiver, _ := w.device(t, ctx, "Receiver", placement{name: "symmetric", kind: netsim.Symmetric}, Events{
				OnAccepted: func(_ peers.Peer, _ protocol.Offer, _ string, _ bool, _, pause func()) { pauses <- pause },
				OnReceived: func(_ peers.Peer, _ protocol.Offer, _ []transfer.Result, err error) { received <- err },
			})
			router := w.lastNAT
			pairDirect(t, ctx, sender, receiver)
			receiver.Peers.Update(sender.Identity.ID(), func(p *peers.Peer) { p.Policy = peers.PolicyAutoAccept })
			file := filepath.Join(t.TempDir(), "driver.exe")
			data := make([]byte, 80<<20)
			rand.Read(data)
			os.WriteFile(file, data, 0o644)

			sendCtx, stop := context.WithCancelCause(ctx)
			var path []bool
			opened, pausedOnce := false, false
			var pause func()
			errc := make(chan error, 1)
			go func() {
				errc <- sender.Send(sendCtx, receiver.Identity.ID(), transfer.NewTransferID(), []string{file}, SendOptions{
					OnConnected: func(relayed bool) { path = append(path, relayed) },
					OnProgress: func(p transfer.Progress) {
						if p.Done > 2<<20 && !opened {
							opened = true
							router.SetKind(netsim.FullCone)
						}
						if len(path) == 2 && !path[1] && !pausedOnce {
							pausedOnce = true
							if who == "sender" {
								stop(transfer.ErrPaused)
							} else {
								go pause()
							}
						}
					},
				})
			}()
			pause = <-pauses
			sendErr := <-errc
			recvErr := <-received
			if len(path) != 2 || path[1] {
				t.Fatalf("path %v: never moved to direct", path)
			}
			if who == "sender" && !transfer.IsPausedByPeer(recvErr) {
				t.Fatalf("receiver got %v (%T), want paused by peer", recvErr, recvErr)
			}
			if who == "receiver" && !transfer.IsPausedByPeer(sendErr) {
				t.Fatalf("sender got %v (%T), want paused by peer", sendErr, sendErr)
			}
		})
	}
}
