package appcore

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"TropicalPeer/internal/node"
	"TropicalPeer/internal/peers"
	"TropicalPeer/internal/server"
	"TropicalPeer/internal/transfer"
)

// testCore is a whole App (Core and node, wired as startNode wires them)
// reporting into a recorder.
func testCore(t *testing.T, ctx context.Context, addr, name string) (*Core, *recorder, string) {
	t.Helper()
	rec := &recorder{}
	c := New(t.TempDir(), nil)
	c.ctx = ctx
	c.emitFn = rec.emit
	var downloads string
	c.node, downloads = startNode(t, ctx, addr, name, node.Events{
		OnOffer:         c.onOffer,
		OnAccepted:      c.onAccepted,
		OnProgress:      c.onIncomingProgress,
		OnReceived:      c.onReceived,
		OnPathChanged:   c.onPathChanged,
		OnResumeRequest: c.onResumeRequest,
		OnCancelNotice:  c.onCancelNotice,
	})
	return c, rec, downloads
}

// mark is where the recorder's history stands now; waitSince only looks at
// events after it (a resumed row's history also holds its earlier states).
func (r *recorder) mark() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

func (r *recorder) waitSince(t *testing.T, mark int, id string, cond func(TransferDTO) bool) TransferDTO {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		r.mu.Lock()
		for i := len(r.events) - 1; i >= mark; i-- {
			if e := r.events[i]; e.ID == id && cond(e) {
				r.mu.Unlock()
				return e
			}
		}
		r.mu.Unlock()
	}
	t.Fatalf("transfer %s: condition not reached; history %v", id, r.history(id))
	return TransferDTO{}
}

// slowSource yields data in small pieces with pauses, so a person has time
// to press Pause mid-file.
func slowSource(name string, data []byte) transfer.Source {
	return transfer.Source{Name: name, Size: int64(len(data)), Open: func() (io.ReadCloser, error) {
		return io.NopCloser(&slowReader{r: bytes.NewReader(data)}), nil
	}}
}

type slowReader struct{ r io.Reader }

func (s *slowReader) Read(p []byte) (int, error) {
	time.Sleep(2 * time.Millisecond)
	if len(p) > 64<<10 {
		p = p[:64<<10]
	}
	return s.r.Read(p)
}

func TestPauseAndResume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.New(log.New(io.Discard, "", 0)).Serve(ctx, ln)
	addr := ln.Addr().String()

	me, myRec, _ := testCore(t, ctx, addr, "Me")
	friend, friendRec, downloads := testCore(t, ctx, addr, "Friend")
	pairNodes(t, ctx, me.node, friend.node)
	friend.node.Peers.Update(me.node.Identity.ID(), func(p *peers.Peer) { p.Policy = peers.PolicyAutoAccept })

	data := make([]byte, 24<<20)
	rand.Read(data)
	part := filepath.Join(downloads, "film.mov.part")
	isDone := func(d TransferDTO) bool {
		return d.Status == StatusDone || d.Status == StatusFailed || d.Status == StatusCancelled
	}

	sent, err := me.SendSources(friend.node.Identity.ID(), []transfer.Source{slowSource("film.mov", data)})
	if err != nil {
		t.Fatal(err)
	}
	in := friendRec.latestIncoming(t, 0)

	t.Run("the sender pauses; the friend sees who paused and keeps the part", func(t *testing.T) {
		myRec.waitFor(t, sent.ID, func(d TransferDTO) bool { return d.Done > 2<<20 })
		me.PauseTransfer(sent.ID)
		mine := myRec.waitFor(t, sent.ID, func(d TransferDTO) bool { return d.Status == StatusPaused })
		if mine.PausedByPeer || mine.Resuming {
			t.Fatalf("sender row %+v", mine)
		}
		theirs := friendRec.waitFor(t, in.ID, func(d TransferDTO) bool { return d.Status == StatusPaused })
		if !theirs.PausedByPeer {
			t.Fatal("the friend's row doesn't say the other side paused")
		}
		time.Sleep(3 * time.Second) // a paused send must not reconnect by itself
		if d := myRec.waitFor(t, sent.ID, func(TransferDTO) bool { return true }); d.Status != StatusPaused {
			t.Fatalf("paused send moved on to %q", d.Status)
		}
		if st, err := os.Stat(part); err != nil || st.Size() == 0 {
			t.Fatalf("part not kept: %v", err)
		}
	})

	t.Run("the friend resumes it: asks the sender, which carries on", func(t *testing.T) {
		fm, mm := friendRec.mark(), myRec.mark()
		if _, err := friend.ResumeTransfer(in.ID); err != nil {
			t.Fatal(err)
		}
		friendRec.waitSince(t, fm, in.ID, func(d TransferDTO) bool { return d.Status == StatusReceiving && d.Done > 0 })
		myRec.waitSince(t, mm, sent.ID, func(d TransferDTO) bool { return d.Status == StatusSending })
	})

	t.Run("the friend pauses; the sender sees it and doesn't reconnect", func(t *testing.T) {
		fm, mm := friendRec.mark(), myRec.mark()
		friend.PauseTransfer(in.ID)
		myRec.waitSince(t, mm, sent.ID, func(d TransferDTO) bool { return d.Status == StatusPaused && d.PausedByPeer })
		friendRec.waitSince(t, fm, in.ID, func(d TransferDTO) bool { return d.Status == StatusPaused && !d.PausedByPeer })
	})

	t.Run("the sender resumes it and it arrives intact, in the same rows", func(t *testing.T) {
		fm, mm := friendRec.mark(), myRec.mark()
		if _, err := me.ResumeTransfer(sent.ID); err != nil {
			t.Fatal(err)
		}
		mine := myRec.waitSince(t, mm, sent.ID, isDone)
		if mine.Status != StatusDone {
			t.Fatalf("sender: %q %s (history %v)", mine.Status, mine.Error, myRec.history(sent.ID))
		}
		theirs := friendRec.waitSince(t, fm, in.ID, func(d TransferDTO) bool { return d.Status == StatusDone })
		if len(theirs.Saved) != 1 {
			t.Fatalf("friend saved %v", theirs.Saved)
		}
		got, _ := os.ReadFile(theirs.Saved[0])
		if !bytes.Equal(got, data) {
			t.Fatal("the file differs after pausing and resuming")
		}
		if len(friend.ListTransfers()) != 1 || len(me.ListTransfers()) != 1 {
			t.Fatalf("rows: friend %d, me %d; want one each", len(friend.ListTransfers()), len(me.ListTransfers()))
		}
	})

	t.Run("the sender cancels while paused: the friend hears and drops the part", func(t *testing.T) {
		d, err := me.SendSources(friend.node.Identity.ID(), []transfer.Source{slowSource("third.mov", data)})
		if err != nil {
			t.Fatal(err)
		}
		in3 := friendRec.latestIncoming(t, 1)
		myRec.waitFor(t, d.ID, func(d TransferDTO) bool { return d.Done > 1<<20 })
		me.PauseTransfer(d.ID)
		friendRec.waitFor(t, in3.ID, func(d TransferDTO) bool { return d.Status == StatusPaused })
		part3 := filepath.Join(downloads, "third.mov.part")
		if _, err := os.Stat(part3); err != nil {
			t.Fatalf("part not kept while paused: %v", err)
		}
		me.CancelTransfer(d.ID)
		myRec.waitFor(t, d.ID, func(d TransferDTO) bool { return d.Status == StatusCancelled })
		theirs := friendRec.waitFor(t, in3.ID, func(d TransferDTO) bool { return d.Status == StatusFailed })
		if theirs.Error != "Me cancelled the transfer." {
			t.Fatalf("friend's row says %q", theirs.Error)
		}
		if _, err := os.Stat(part3); err == nil {
			t.Fatal("the friend kept the part after the sender cancelled")
		}
	})

	t.Run("the friend cancels while paused: the sender hears", func(t *testing.T) {
		d, err := me.SendSources(friend.node.Identity.ID(), []transfer.Source{slowSource("fourth.mov", data)})
		if err != nil {
			t.Fatal(err)
		}
		in4 := friendRec.latestIncoming(t, 2)
		myRec.waitFor(t, d.ID, func(d TransferDTO) bool { return d.Done > 1<<20 })
		friend.PauseTransfer(in4.ID)
		myRec.waitFor(t, d.ID, func(d TransferDTO) bool { return d.Status == StatusPaused })
		friend.CancelTransfer(in4.ID)
		mine := myRec.waitFor(t, d.ID, func(d TransferDTO) bool { return d.Status == StatusFailed })
		if mine.Error != "Friend cancelled the transfer." {
			t.Fatalf("sender's row says %q", mine.Error)
		}
		if _, err := os.Stat(filepath.Join(downloads, "fourth.mov.part")); err == nil {
			t.Fatal("the friend kept its own part after cancelling")
		}
	})

	t.Run("cancelling a paused incoming transfer deletes the part", func(t *testing.T) {
		d, err := me.SendSources(friend.node.Identity.ID(), []transfer.Source{slowSource("second.mov", data)})
		if err != nil {
			t.Fatal(err)
		}
		in2 := friendRec.latestIncoming(t, 3)
		myRec.waitFor(t, d.ID, func(d TransferDTO) bool { return d.Done > 1<<20 })
		friend.PauseTransfer(in2.ID)
		friendRec.waitFor(t, in2.ID, func(d TransferDTO) bool { return d.Status == StatusPaused })
		part2 := filepath.Join(downloads, "second.mov.part")
		if _, err := os.Stat(part2); err != nil {
			t.Fatalf("part not kept while paused: %v", err)
		}
		friend.CancelTransfer(in2.ID)
		friendRec.waitFor(t, in2.ID, func(d TransferDTO) bool { return d.Status == StatusCancelled })
		if _, err := os.Stat(part2); err == nil {
			t.Fatal("cancelling kept the part")
		}
	})
}
