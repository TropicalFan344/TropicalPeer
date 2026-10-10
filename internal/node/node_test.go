package node

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"TropicalPeer/internal/pairing"
	"TropicalPeer/internal/peers"
	"TropicalPeer/internal/protocol"
	"TropicalPeer/internal/server"
	"TropicalPeer/internal/transfer"
)

func startServer(t *testing.T, ctx context.Context) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.New(log.New(io.Discard, "", 0)).Serve(ctx, ln)
	return ln.Addr().String()
}

type testNode struct {
	*Node
	downloads string
	asked     atomic.Int32
	answer    atomic.Bool
	online    chan struct{}
}

func startNode(t *testing.T, ctx context.Context, serverAddr, name string) *testNode {
	t.Helper()
	dir := t.TempDir()
	tn := &testNode{downloads: filepath.Join(dir, "downloads"), online: make(chan struct{}, 1)}
	n, err := New(dir, Events{
		OnServerStatus: func(online bool, err error) {
			if online {
				select {
				case tn.online <- struct{}{}:
				default:
				}
			}
		},
		OnOffer: func(ctx context.Context, p peers.Peer, o protocol.Offer) bool {
			tn.asked.Add(1)
			return tn.answer.Load()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tn.Node = n
	if err := n.SetName(name); err != nil {
		t.Fatal(err)
	}
	if err := n.UpdateSettings(Settings{ServerAddr: serverAddr, Port: 0, DownloadDir: tn.downloads}); err != nil {
		t.Fatal(err)
	}
	go n.Run(ctx)
	select {
	case <-tn.online:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s never came online", name)
	}
	return tn
}

func pair(t *testing.T, ctx context.Context, a, b *testNode) {
	t.Helper()
	codes := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		_, err := a.Invite(ctx, func(code string) { codes <- code })
		done <- err
	}()
	pb, err := b.Join(ctx, <-codes)
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("invite: %v", err)
	}
	if pb.Name != "Alice" || pb.ID != a.Identity.ID() {
		t.Fatalf("bob paired with %+v, want Alice", pb)
	}
	if _, err := a.Peers.Get(b.Identity.ID()); err != nil {
		t.Fatalf("alice did not store bob: %v", err)
	}
}

func writeRandomFile(t *testing.T, dir, name string, size int) (string, []byte) {
	t.Helper()
	data := make([]byte, size)
	rand.Read(data)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, data
}

func TestPairAndTransfer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	addr := startServer(t, ctx)
	alice := startNode(t, ctx, addr, "Alice")
	bob := startNode(t, ctx, addr, "Bob")
	pair(t, ctx, alice, bob)

	src := t.TempDir()
	big, bigData := writeRandomFile(t, src, "photo.jpg", 5<<20+123)
	small, smallData := writeRandomFile(t, src, "notes.txt", 10)
	aliceID := alice.Identity.ID()
	bobID := bob.Identity.ID()

	t.Run("ask policy, declined", func(t *testing.T) {
		bob.answer.Store(false)
		err := alice.Send(ctx, bobID, transfer.NewTransferID(), []string{small}, SendOptions{})
		var rej *transfer.RejectedError
		if !errors.As(err, &rej) {
			t.Fatalf("want RejectedError, got %v", err)
		}
		if bob.asked.Load() != 1 {
			t.Fatalf("bob was asked %d times, want 1", bob.asked.Load())
		}
	})

	t.Run("ask policy, accepted", func(t *testing.T) {
		bob.answer.Store(true)
		if err := alice.Send(ctx, bobID, transfer.NewTransferID(), []string{small}, SendOptions{}); err != nil {
			t.Fatal(err)
		}
		assertFile(t, filepath.Join(bob.downloads, "notes.txt"), smallData)
	})

	t.Run("auto accept, multiple files, name collision", func(t *testing.T) {
		bob.Peers.Update(aliceID, func(p *peers.Peer) { p.Policy = peers.PolicyAutoAccept })
		before := bob.asked.Load()
		if err := alice.Send(ctx, bobID, transfer.NewTransferID(), []string{big, small}, SendOptions{}); err != nil {
			t.Fatal(err)
		}
		if bob.asked.Load() != before {
			t.Fatal("bob was asked despite auto-accept")
		}
		assertFile(t, filepath.Join(bob.downloads, "photo.jpg"), bigData)
		assertFile(t, filepath.Join(bob.downloads, "notes (1).txt"), smallData)
	})

	t.Run("auto accept over size limit asks", func(t *testing.T) {
		bob.Peers.Update(aliceID, func(p *peers.Peer) { p.MaxAutoSize = 1 << 20 })
		bob.answer.Store(false)
		before := bob.asked.Load()
		if err := alice.Send(ctx, bobID, transfer.NewTransferID(), []string{big}, SendOptions{}); err == nil {
			t.Fatal("expected decline")
		}
		if bob.asked.Load() != before+1 {
			t.Fatal("bob should have been asked for an offer over the limit")
		}
	})

	t.Run("blocked", func(t *testing.T) {
		bob.Peers.Update(aliceID, func(p *peers.Peer) { p.Policy = peers.PolicyBlock })
		var rej *transfer.RejectedError
		if err := alice.Send(ctx, bobID, transfer.NewTransferID(), []string{small}, SendOptions{}); !errors.As(err, &rej) {
			t.Fatalf("want RejectedError, got %v", err)
		}
	})

	t.Run("reverse direction", func(t *testing.T) {
		alice.Peers.Update(bobID, func(p *peers.Peer) { p.Policy = peers.PolicyAutoAccept })
		if err := bob.Send(ctx, aliceID, transfer.NewTransferID(), []string{small}, SendOptions{}); err != nil {
			t.Fatal(err)
		}
		assertFile(t, filepath.Join(alice.downloads, "notes.txt"), smallData)
	})

	t.Run("cancel mid-transfer leaves nothing behind", func(t *testing.T) {
		huge, _ := writeRandomFile(t, src, "huge.bin", 200<<20)
		sendCtx, cancelSend := context.WithCancelCause(ctx)
		var phases []SendPhase
		// Alice auto-accepts from Bob (set in "reverse direction").
		err := bob.Send(sendCtx, aliceID, transfer.NewTransferID(), []string{huge}, SendOptions{
			OnPhase: func(p SendPhase) { phases = append(phases, p) },
			OnProgress: func(p transfer.Progress) {
				if p.Done > 0 {
					cancelSend(transfer.ErrCancelled) // a person cancelled, so nothing is kept
				}
			},
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled, got %v", err)
		}
		if len(phases) != 2 || phases[0] != PhaseConnecting || phases[1] != PhaseWaiting {
			t.Fatalf("phases = %v, want [connecting waiting]", phases)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			_, errFinal := os.Stat(filepath.Join(alice.downloads, "huge.bin"))
			_, errPart := os.Stat(filepath.Join(alice.downloads, "huge.bin.part"))
			if os.IsNotExist(errFinal) && os.IsNotExist(errPart) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("receiver kept huge.bin or huge.bin.part after cancel")
			}
			time.Sleep(50 * time.Millisecond)
		}
	})

	t.Run("unpaired device cannot connect", func(t *testing.T) {
		mallory := startNode(t, ctx, addr, "Mallory")
		// Mallory somehow knows Bob's key but Bob never paired with Mallory.
		b, _ := alice.Peers.Get(bobID)
		mallory.Peers.Add(b)
		if err := mallory.Send(ctx, bobID, transfer.NewTransferID(), []string{small}, SendOptions{}); err == nil {
			t.Fatal("unpaired device was able to send")
		}
	})
}

func TestWrongCode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	addr := startServer(t, ctx)
	alice := startNode(t, ctx, addr, "Alice")
	bob := startNode(t, ctx, addr, "Bob")

	codes := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		_, err := alice.Invite(ctx, func(code string) { codes <- code })
		done <- err
	}()
	nameplate, _, _ := pairing.ParseCode(<-codes)
	if _, err := bob.Join(ctx, nameplate+"-wrong-guess"); !errors.Is(err, pairing.ErrWrongCode) {
		t.Fatalf("bob: want ErrWrongCode, got %v", err)
	}
	if err := <-done; !errors.Is(err, pairing.ErrWrongCode) {
		t.Fatalf("alice: want ErrWrongCode, got %v", err)
	}
	if len(alice.Peers.List())+len(bob.Peers.List()) != 0 {
		t.Fatal("a peer was stored despite the wrong code")
	}
}

func assertFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s: content mismatch (%d bytes, want %d)", path, len(got), len(want))
	}
}
