package appcore

import (
	"context"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"TropicalPeer/internal/node"
	"TropicalPeer/internal/peers"
	"TropicalPeer/internal/protocol"
	"TropicalPeer/internal/server"
	"TropicalPeer/internal/transfer"
)

func TestAppTexts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.New(log.New(io.Discard, "", 0)).Serve(ctx, ln)
	addr := ln.Addr().String()

	got := make(chan protocol.Text, 8)
	friend, _ := startNode(t, ctx, addr, "Friend", node.Events{
		OnText: func(_ peers.Peer, x protocol.Text, _ bool) { got <- x },
	})
	rec := &recorder{}
	app := New(t.TempDir(), nil)
	app.ctx = ctx
	app.emitFn = rec.emit
	app.SetLanguage("en")
	app.node, _ = startNode(t, ctx, addr, "Me", node.Events{OnText: app.onText})
	pairNodes(t, ctx, app.node, friend)
	myID, friendID := app.node.Identity.ID(), friend.Identity.ID()

	t.Run("sent", func(t *testing.T) {
		d, err := app.SendText(friendID, "  https://example.com  ")
		if err != nil {
			t.Fatal(err)
		}
		if d.Direction != "send" || d.Unread {
			t.Fatalf("sent text %+v", d)
		}
		select {
		case x := <-got:
			if x.Body != "  https://example.com  " {
				t.Fatalf("friend got %q", x.Body)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("friend got nothing")
		}
	})

	t.Run("received is unread until seen, and holds off restarts", func(t *testing.T) {
		if _, err := friend.SendText(ctx, myID, protocol.Text{ID: transfer.NewTextID(), Body: "code 4711"}); err != nil {
			t.Fatal(err)
		}
		list := app.ListTexts()
		if len(list) != 2 || list[0].Body != "code 4711" || !list[0].Unread || list[0].PeerName != "Friend" {
			t.Fatalf("texts %+v", list)
		}
		if !app.busy() {
			t.Fatal("an unread text doesn't keep the app from restarting")
		}
		app.MarkTextRead(list[0].ID)
		if app.busy() {
			t.Fatal("still busy after the text was read")
		}
		app.ClearFinishedTransfers()
		if n := len(app.ListTexts()); n != 0 {
			t.Fatalf("%d texts left after clearing", n)
		}
	})

	t.Run("refused by a blocking friend", func(t *testing.T) {
		friend.Peers.Update(myID, func(p *peers.Peer) { p.Policy = peers.PolicyBlock })
		defer friend.Peers.Update(myID, func(p *peers.Peer) { p.Policy = peers.PolicyAsk })
		_, err := app.SendText(friendID, "hello?")
		if err == nil || err.Error() != app.T("text.refused", "name", "Friend") {
			t.Fatalf("SendText = %v", err)
		}
		if n := len(app.ListTexts()); n != 0 {
			t.Fatalf("a refused text was kept (%d texts)", n)
		}
	})

	t.Run("files with a note", func(t *testing.T) {
		friend.Peers.Update(myID, func(p *peers.Peer) { p.Policy = peers.PolicyAutoAccept })
		defer friend.Peers.Update(myID, func(p *peers.Peer) { p.Policy = peers.PolicyAsk })
		path := filepath.Join(t.TempDir(), "photo.jpg")
		os.WriteFile(path, []byte("jpeg"), 0o644)
		d, err := app.SendFilesNote(friendID, []string{path}, "  the one from yesterday  ")
		if err != nil {
			t.Fatal(err)
		}
		final := rec.waitFinal(t, d.ID)
		if final.Status != StatusDone || final.Note != "  the one from yesterday  " || final.NoteLost {
			t.Fatalf("transfer %+v", final)
		}
		long := strings.Repeat("x", protocol.MaxNoteBytes+1)
		if _, err := app.SendFilesNote(friendID, []string{path}, long); err == nil || err.Error() != app.T("text.noteTooLong") {
			t.Fatalf("long note: %v", err)
		}
	})

	t.Run("too long", func(t *testing.T) {
		long := make([]byte, protocol.MaxTextBytes+1)
		for i := range long {
			long[i] = 'x'
		}
		if _, err := app.SendText(friendID, string(long)); err == nil || err.Error() != app.T("text.tooLong") {
			t.Fatalf("SendText = %v", err)
		}
	})
}
