package server

import (
	"context"
	"io"
	"log"
	"net"
	"testing"
	"time"

	"TropicalPeer/internal/identity"
	"TropicalPeer/internal/rendezvous"
)

type watcher struct {
	id    string
	ident *identity.Identity
	p     *rendezvous.Presence
	ev    chan [2]any // {id, online}
}

func register(t *testing.T, ctx context.Context, addr string) *watcher {
	t.Helper()
	id, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return registerAs(t, ctx, addr, id)
}

// reregister connects w's device again, as a new registration.
func reregister(t *testing.T, ctx context.Context, addr string, w *watcher) *watcher {
	t.Helper()
	return registerAs(t, ctx, addr, w.ident)
}

func registerAs(t *testing.T, ctx context.Context, addr string, id *identity.Identity) *watcher {
	t.Helper()
	c := &rendezvous.Client{Addr: addr, ID: id}
	p, err := c.StartPresence(ctx, 4000, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Watches {
		t.Fatal("server doesn't offer presence")
	}
	return &watcher{id: id.ID(), ident: id, p: p, ev: make(chan [2]any, 16)}
}

func (w *watcher) watch(t *testing.T, ids ...string) {
	t.Helper()
	if err := w.p.Watch(ids, func(id string, online bool) { w.ev <- [2]any{id, online} }); err != nil {
		t.Fatal(err)
	}
}

func (w *watcher) expect(t *testing.T, id string, online bool) {
	t.Helper()
	select {
	case e := <-w.ev:
		if e[0] != id || e[1] != online {
			t.Fatalf("got %v online=%v, want %s online=%v", e[0], e[1], id, online)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("no presence for %s (want online=%v)", id, online)
	}
}

func (w *watcher) quiet(t *testing.T) {
	t.Helper()
	select {
	case e := <-w.ev:
		t.Fatalf("unexpected presence %v online=%v", e[0], e[1])
	case <-time.After(200 * time.Millisecond):
	}
}

func startWatchServer(t *testing.T, ctx context.Context, grace time.Duration) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := New(log.New(io.Discard, "", 0))
	s.OfflineGrace = grace
	go s.Serve(ctx, ln)
	return ln.Addr().String()
}

func TestPresenceOnlyBetweenDevicesListingEachOther(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr := startWatchServer(t, ctx, 100*time.Millisecond)

	a, b, stranger := register(t, ctx, addr), register(t, ctx, addr), register(t, ctx, addr)

	// A lists B first: B hasn't listed A, so A reads it as offline.
	a.watch(t, b.id)
	a.expect(t, b.id, false)

	// Once B lists A too, both hear the other is online.
	b.watch(t, a.id)
	b.expect(t, a.id, true)
	a.expect(t, b.id, true)

	// A stranger who knows A's ID reads it as offline, and A hears nothing.
	stranger.watch(t, a.id)
	stranger.expect(t, a.id, false)
	a.quiet(t)

	// B going away reaches A after the grace period.
	b.p.Close()
	a.expect(t, b.id, false)
}

func TestPresenceGraceRidesOutAReconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr := startWatchServer(t, ctx, 500*time.Millisecond)

	a, b := register(t, ctx, addr), register(t, ctx, addr)
	a.watch(t, b.id)
	a.expect(t, b.id, false)
	b.watch(t, a.id)
	b.expect(t, a.id, true)
	a.expect(t, b.id, true)

	// B drops and comes straight back (same identity, new connection).
	b.p.Close()
	b2 := reregister(t, ctx, addr, b)
	b2.watch(t, a.id)
	b2.expect(t, a.id, true)
	a.expect(t, b.id, true) // a repeat of what it knew, never "offline"
	time.Sleep(700 * time.Millisecond)
	a.quiet(t)
}

func TestPresenceUnlistingTellsTheOtherSide(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr := startWatchServer(t, ctx, 100*time.Millisecond)

	a, b := register(t, ctx, addr), register(t, ctx, addr)
	a.watch(t, b.id)
	a.expect(t, b.id, false)
	b.watch(t, a.id)
	b.expect(t, a.id, true)
	a.expect(t, b.id, true)

	// B removes A: A sees B go offline, and B's new list is empty.
	b.watch(t)
	a.expect(t, b.id, false)
	b.quiet(t)
}
