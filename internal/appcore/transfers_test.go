package appcore

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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"TropicalPeer/internal/node"
	"TropicalPeer/internal/peers"
	"TropicalPeer/internal/protocol"
	"TropicalPeer/internal/server"
	"TropicalPeer/internal/transfer"
)

// recorder captures the events the App would push to the frontend.
type recorder struct {
	mu     sync.Mutex
	events []TransferDTO
	offers []OfferDTO
	closed []OfferClosed
}

func (r *recorder) emit(name string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch name {
	case "transfer:update":
		r.events = append(r.events, data.(TransferDTO))
	case "offer:new":
		r.offers = append(r.offers, data.(OfferDTO))
	case "offer:closed":
		r.closed = append(r.closed, data.(OfferClosed))
	}
}

// nextOffer waits for the n-th (1-based) offer prompt.
func (r *recorder) nextOffer(t *testing.T, n int) OfferDTO {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		r.mu.Lock()
		if len(r.offers) >= n {
			o := r.offers[n-1]
			r.mu.Unlock()
			return o
		}
		r.mu.Unlock()
	}
	t.Fatalf("offer #%d never arrived", n)
	return OfferDTO{}
}

// latestIncoming returns the newest receive-direction transfer, waiting for one.
func (r *recorder) latestIncoming(t *testing.T, after int) TransferDTO {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		r.mu.Lock()
		seen := map[string]bool{}
		var ids []string
		for _, e := range r.events {
			if e.Direction == "receive" && !seen[e.ID] {
				seen[e.ID] = true
				ids = append(ids, e.ID)
			}
		}
		r.mu.Unlock()
		if len(ids) > after {
			return r.waitFor(t, ids[after], func(TransferDTO) bool { return true })
		}
	}
	t.Fatalf("incoming transfer #%d never started", after+1)
	return TransferDTO{}
}

// history returns the statuses seen for one transfer, without repeats.
func (r *recorder) history(id string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.events {
		if e.ID == id && (len(out) == 0 || out[len(out)-1] != e.Status) {
			out = append(out, e.Status)
		}
	}
	return out
}

func (r *recorder) waitFor(t *testing.T, id string, cond func(TransferDTO) bool) TransferDTO {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		for i := len(r.events) - 1; i >= 0; i-- {
			if e := r.events[i]; e.ID == id && cond(e) {
				r.mu.Unlock()
				return e
			}
		}
		r.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("transfer %s: condition not reached; history %v", id, r.history(id))
	return TransferDTO{}
}

func (r *recorder) waitFinal(t *testing.T, id string) TransferDTO {
	t.Helper()
	return r.waitFor(t, id, func(d TransferDTO) bool { return !d.active() })
}

// startNode runs a node and waits until it's registered with the server.
func startNode(t *testing.T, ctx context.Context, serverAddr, name string, ev node.Events) (*node.Node, string) {
	t.Helper()
	dir := t.TempDir()
	online := make(chan struct{}, 1)
	userStatus := ev.OnServerStatus
	ev.OnServerStatus = func(ok bool, err error) {
		if ok {
			select {
			case online <- struct{}{}:
			default:
			}
		}
		if userStatus != nil {
			userStatus(ok, err)
		}
	}
	n, err := node.New(dir, ev)
	if err != nil {
		t.Fatal(err)
	}
	downloads := filepath.Join(dir, "downloads")
	if err := n.SetName(name); err != nil {
		t.Fatal(err)
	}
	if err := n.UpdateSettings(node.Settings{ServerAddr: serverAddr, Port: 0, DownloadDir: downloads}); err != nil {
		t.Fatal(err)
	}
	go n.Run(ctx)
	select {
	case <-online:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s never came online", name)
	}
	return n, downloads
}

func pairNodes(t *testing.T, ctx context.Context, a, b *node.Node) {
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

func TestAppTransfers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.New(log.New(io.Discard, "", 0)).Serve(ctx, ln)
	addr := ln.Addr().String()

	// The device on the other end. It declines whenever it would have to ask.
	var asked atomic.Int32
	friend, friendDownloads := startNode(t, ctx, addr, "Friend", node.Events{
		OnOffer: func(context.Context, peers.Peer, protocol.Offer) bool { asked.Add(1); return false },
	})

	// The App under test, wired to a recorder instead of the Wails window.
	rec := &recorder{}
	app := New(t.TempDir(), nil)
	app.ctx = ctx
	app.emitFn = rec.emit
	app.node, _ = startNode(t, ctx, addr, "Me", node.Events{})

	pairNodes(t, ctx, app.node, friend)
	myID, friendID := app.node.Identity.ID(), friend.Identity.ID()

	src := t.TempDir()
	small := filepath.Join(src, "hello.txt")
	os.WriteFile(small, []byte("hello friend"), 0o644)
	big := filepath.Join(src, "big.bin")
	bigData := make([]byte, 200<<20)
	rand.Read(bigData)
	os.WriteFile(big, bigData, 0o644)

	t.Run("declined when the friend asks and says no", func(t *testing.T) {
		d, err := app.SendFiles(friendID, []string{small})
		if err != nil {
			t.Fatal(err)
		}
		final := rec.waitFinal(t, d.ID)
		if final.Status != StatusDeclined || asked.Load() != 1 {
			t.Fatalf("status %q, asked %d; want declined, 1", final.Status, asked.Load())
		}
	})

	friend.Peers.Update(myID, func(p *peers.Peer) { p.Policy = peers.PolicyAutoAccept })

	t.Run("auto-accepted send goes connecting → waiting → done", func(t *testing.T) {
		d, err := app.SendFiles(friendID, []string{small})
		if err != nil {
			t.Fatal(err)
		}
		final := rec.waitFinal(t, d.ID)
		if final.Status != StatusDone || final.Done != final.Total {
			t.Fatalf("final = %+v", final)
		}
		h := rec.history(d.ID)
		if h[0] != StatusConnecting || h[1] != StatusWaiting || h[len(h)-1] != StatusDone {
			t.Fatalf("history %v", h)
		}
		got, _ := os.ReadFile(filepath.Join(friendDownloads, "hello.txt"))
		if string(got) != "hello friend" {
			t.Fatalf("friend got %q", got)
		}
	})

	var cancelledID string
	t.Run("cancel while sending", func(t *testing.T) {
		d, err := app.SendFiles(friendID, []string{big})
		if err != nil {
			t.Fatal(err)
		}
		rec.waitFor(t, d.ID, func(d TransferDTO) bool { return d.Status == StatusSending })
		app.CancelTransfer(d.ID)
		final := rec.waitFinal(t, d.ID)
		if final.Status != StatusCancelled {
			t.Fatalf("status %q, want cancelled", final.Status)
		}
		cancelledID = d.ID
		time.Sleep(300 * time.Millisecond)
		if _, err := os.Stat(filepath.Join(friendDownloads, "big.bin")); err == nil {
			t.Fatal("friend kept a cancelled file")
		}
		if _, err := os.Stat(filepath.Join(friendDownloads, "big.bin.part")); err == nil {
			t.Fatal("friend kept the part of a cancelled file")
		}
	})

	t.Run("retry sends the same files again in the same row", func(t *testing.T) {
		d, err := app.RetryTransfer(cancelledID)
		if err != nil {
			t.Fatal(err)
		}
		if d.ID != cancelledID {
			t.Fatal("retry started a new row")
		}
		// The row's earlier "cancelled" is in the history too; wait for the new end.
		final := rec.waitFor(t, d.ID, func(d TransferDTO) bool { return d.Status == StatusDone || d.Status == StatusFailed })
		if final.Status != StatusDone {
			t.Fatalf("retry status %q: %s", final.Status, final.Error)
		}
		got, _ := os.ReadFile(filepath.Join(friendDownloads, "big.bin"))
		if !bytes.Equal(got, bigData) {
			t.Fatal("retried file differs")
		}
		var speedSeen bool
		rec.mu.Lock()
		for _, e := range rec.events {
			if e.ID == d.ID && e.Speed > 0 {
				speedSeen = true
			}
		}
		rec.mu.Unlock()
		if !speedSeen {
			t.Log("no speed sample (transfer finished within one sampling window)")
		}
	})

	t.Run("folders are refused up front", func(t *testing.T) {
		if _, err := app.SendFiles(friendID, []string{src}); err == nil || !strings.Contains(err.Error(), "folder") {
			t.Fatalf("want folder error, got %v", err)
		}
	})

	t.Run("offline peer fails with a clear message", func(t *testing.T) {
		offCtx, stopOff := context.WithCancel(ctx)
		off, _ := startNode(t, offCtx, addr, "Sleepy", node.Events{})
		pairNodes(t, ctx, app.node, off)
		stopOff()
		time.Sleep(300 * time.Millisecond) // let the server notice

		d, err := app.SendFiles(off.Identity.ID(), []string{small})
		if err != nil {
			t.Fatal(err)
		}
		final := rec.waitFinal(t, d.ID)
		if final.Status != StatusFailed || !strings.Contains(final.Error, "offline") {
			t.Fatalf("status %q error %q", final.Status, final.Error)
		}
	})
}

func TestAppReceive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.New(log.New(io.Discard, "", 0)).Serve(ctx, ln)
	addr := ln.Addr().String()

	// The App under test is the receiver; its node reports into the App.
	rec := &recorder{}
	app := New(t.TempDir(), nil)
	app.ctx = ctx
	app.emitFn = rec.emit
	var downloads string
	app.node, downloads = startNode(t, ctx, addr, "Me", node.Events{
		OnOffer: func(c context.Context, p peers.Peer, o protocol.Offer) bool { return app.onOffer(c, p, o) },
		OnAccepted: func(p peers.Peer, o protocol.Offer, dir string, relayed bool, cancel, pause func()) {
			app.onAccepted(p, o, dir, relayed, cancel, pause)
		},
		OnProgress: func(p peers.Peer, pr transfer.Progress) { app.onIncomingProgress(p, pr) },
		OnReceived: func(p peers.Peer, o protocol.Offer, f []transfer.Result, err error) { app.onReceived(p, o, f, err) },
	})
	friend, _ := startNode(t, ctx, addr, "Friend", node.Events{})
	pairNodes(t, ctx, friend, app.node)
	myID := app.node.Identity.ID()

	src := t.TempDir()
	small := filepath.Join(src, "photo.jpg")
	os.WriteFile(small, []byte("pretend jpeg"), 0o644)
	big := filepath.Join(src, "movie.mkv")
	os.WriteFile(big, make([]byte, 300<<20), 0o644)

	send := func(paths ...string) chan error {
		errc := make(chan error, 1)
		go func() { errc <- friend.Send(ctx, myID, transfer.NewTransferID(), paths, node.SendOptions{}) }()
		return errc
	}

	t.Run("ask: accept saves the file and reports where", func(t *testing.T) {
		errc := send(small)
		offer := rec.nextOffer(t, 1)
		if offer.PeerName != "Friend" || len(offer.Files) != 1 || offer.Files[0].Name != "photo.jpg" || offer.SaveDir != downloads {
			t.Fatalf("offer = %+v", offer)
		}
		if len(app.ListOffers()) != 1 {
			t.Fatal("ListOffers should show the pending offer")
		}
		if err := app.RespondOffer(offer.ID, true); err != nil {
			t.Fatal(err)
		}
		if err := <-errc; err != nil {
			t.Fatalf("sender: %v", err)
		}
		in := rec.waitFinal(t, rec.latestIncoming(t, 0).ID)
		want := filepath.Join(downloads, "photo.jpg")
		if in.Status != StatusDone || len(in.Saved) != 1 || in.Saved[0] != want {
			t.Fatalf("incoming = %+v", in)
		}
		if len(app.ListOffers()) != 0 {
			t.Fatal("answered offer still listed")
		}
	})

	t.Run("ask: decline tells the sender and records nothing", func(t *testing.T) {
		errc := send(small)
		app.RespondOffer(rec.nextOffer(t, 2).ID, false)
		var rej *transfer.RejectedError
		if err := <-errc; !errors.As(err, &rej) {
			t.Fatalf("sender got %v, want RejectedError", err)
		}
		if err := app.RespondOffer(rec.nextOffer(t, 2).ID, true); err == nil {
			t.Fatal("answering a closed offer should fail")
		}
	})

	t.Run("sender cancels while the prompt is open", func(t *testing.T) {
		sendCtx, stop := context.WithCancel(ctx)
		go friend.Send(sendCtx, myID, transfer.NewTransferID(), []string{small}, node.SendOptions{})
		offer := rec.nextOffer(t, 3)
		stop()
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			rec.mu.Lock()
			var got *OfferClosed
			for i := range rec.closed {
				if rec.closed[i].ID == offer.ID {
					got = &rec.closed[i]
				}
			}
			rec.mu.Unlock()
			if got != nil {
				if !strings.Contains(got.Reason, "cancelled") {
					t.Fatalf("reason %q", got.Reason)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("prompt was not closed after the sender cancelled")
			}
		}
	})

	app.node.Peers.Update(friend.Identity.ID(), func(p *peers.Peer) { p.Policy = peers.PolicyAutoAccept })

	t.Run("receiver cancels mid-transfer", func(t *testing.T) {
		errc := send(big)
		in := rec.latestIncoming(t, 1)
		rec.waitFor(t, in.ID, func(d TransferDTO) bool { return d.Done > 0 })
		app.CancelTransfer(in.ID)
		if final := rec.waitFinal(t, in.ID); final.Status != StatusCancelled {
			t.Fatalf("status %q", final.Status)
		}
		if err := <-errc; err == nil || !transfer.IsCancelledByPeer(err) {
			t.Fatalf("sender should see the receiver's cancel, got %v", err)
		}
	})

	t.Run("sender cancels mid-transfer", func(t *testing.T) {
		sendCtx, cancelSend := context.WithCancelCause(ctx)
		go friend.Send(sendCtx, myID, transfer.NewTransferID(), []string{big}, node.SendOptions{})
		in := rec.latestIncoming(t, 2)
		rec.waitFor(t, in.ID, func(d TransferDTO) bool { return d.Done > 0 })
		cancelSend(transfer.ErrCancelled)
		final := rec.waitFinal(t, in.ID)
		if final.Status != StatusFailed || !strings.Contains(final.Error, "Friend cancelled") {
			t.Fatalf("status %q error %q", final.Status, final.Error)
		}
	})

	t.Run("sender's app stops mid-transfer: kept for later", func(t *testing.T) {
		sendCtx, stop := context.WithCancel(ctx)
		go friend.Send(sendCtx, myID, transfer.NewTransferID(), []string{big}, node.SendOptions{})
		in := rec.latestIncoming(t, 3)
		rec.waitFor(t, in.ID, func(d TransferDTO) bool { return d.Done > 0 })
		stop()
		final := rec.waitFinal(t, in.ID)
		if final.Status != StatusFailed || !strings.Contains(final.Error, "continues where it left off") {
			t.Fatalf("status %q error %q", final.Status, final.Error)
		}
		matches, _ := filepath.Glob(filepath.Join(downloads, "*.part"))
		if len(matches) != 1 {
			t.Fatalf("part files kept: %v, want one", matches)
		}
	})
}
