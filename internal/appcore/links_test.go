package appcore

import (
	"context"
	"io"
	"log"
	"net"
	"sync"
	"testing"
	"time"

	"TropicalPeer/internal/node"
	"TropicalPeer/internal/rendezvous"
	"TropicalPeer/internal/server"
	"TropicalPeer/internal/transfer"
)

// A link stopped right after it was made stays gone: its own shutdown sent
// one more update, which could reach the page after link:removed and bring
// the card back as "stopped", with no way to remove it.
func TestStopLinkRightAway(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srv := server.New(log.New(io.Discard, "", 0))
	srv.Links = &server.Links{WebURL: "http://test/r/", ICE: func(string) []rendezvous.ICEServer { return nil }}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ctx, ln)

	var mu sync.Mutex
	var events []string // "<name> <link id>"
	c := New(t.TempDir(), nil)
	c.ctx = ctx
	c.emitFn = func(name string, data any) {
		var id string
		switch d := data.(type) {
		case LinkDTO:
			id = d.ID
		case string:
			id = d
		default:
			return
		}
		mu.Lock()
		events = append(events, name+" "+id)
		mu.Unlock()
	}
	c.node, _ = startNode(t, ctx, ln.Addr().String(), "Me", node.Events{})
	for deadline := time.Now().Add(10 * time.Second); c.node.LinksURL() == ""; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the server never offered links")
		}
	}

	for range 5 {
		l, err := c.CreateLinkSources([]transfer.Source{slowSource("a.txt", []byte("hello"))}, false)
		if err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		mark := len(events)
		mu.Unlock()
		c.StopLink(l.ID)
		time.Sleep(300 * time.Millisecond) // let the link's goroutines wind down

		// Nothing but the removal may follow Stop: on Android events can
		// reach the page out of order, so any update could land after it.
		mu.Lock()
		after := append([]string{}, events[mark:]...)
		mu.Unlock()
		if len(after) != 1 || after[0] != "link:removed "+l.ID {
			t.Fatalf("events after StopLink = %v, want only link:removed", after)
		}
	}
	if got := c.ListLinks(); len(got) != 0 {
		t.Fatalf("ListLinks = %d links after stopping them all", len(got))
	}
}
