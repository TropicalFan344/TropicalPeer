package node

import (
	"context"
	"errors"
	"testing"
	"time"

	"TropicalPeer/internal/peers"
	"TropicalPeer/internal/protocol"
	"TropicalPeer/internal/transfer"
)

func TestSendText(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	addr := startServer(t, ctx)
	alice := startNode(t, ctx, addr, "Alice")
	bob := startNode(t, ctx, addr, "Bob")
	pair(t, ctx, alice, bob)
	bobID := bob.Identity.ID()
	aliceID := alice.Identity.ID()

	expect := func(t *testing.T, want string) {
		t.Helper()
		select {
		case got := <-bob.texts:
			if got.Body != want {
				t.Fatalf("Bob was shown %q, want %q", got.Body, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("Bob was never shown %q", want)
		}
	}
	none := func(t *testing.T) {
		t.Helper()
		select {
		case got := <-bob.texts:
			t.Fatalf("Bob was shown %q, want nothing", got.Body)
		default:
		}
	}

	t.Run("delivered and cleaned", func(t *testing.T) {
		_, err := alice.SendText(ctx, bobID, protocol.Text{ID: transfer.NewTextID(), Body: "\r\nhttps://example.com/\u202Egnp.exe\r\n"})
		if err != nil {
			t.Fatal(err)
		}
		expect(t, "https://example.com/gnp.exe")
	})

	t.Run("sent again is shown once", func(t *testing.T) {
		text := protocol.Text{ID: transfer.NewTextID(), Body: "once"}
		for i := 0; i < 2; i++ {
			if _, err := alice.SendText(ctx, bobID, text); err != nil {
				t.Fatal(err)
			}
		}
		expect(t, "once")
		none(t)
	})

	t.Run("empty is refused before sending", func(t *testing.T) {
		_, err := alice.SendText(ctx, bobID, protocol.Text{ID: transfer.NewTextID(), Body: " \n "})
		if !errors.Is(err, protocol.ErrTextEmpty) {
			t.Fatalf("SendText = %v, want ErrTextEmpty", err)
		}
	})

	t.Run("blocked peer", func(t *testing.T) {
		setPolicy(t, bob, aliceID, peers.PolicyBlock)
		defer setPolicy(t, bob, aliceID, peers.PolicyAsk)
		_, err := alice.SendText(ctx, bobID, protocol.Text{ID: transfer.NewTextID(), Body: "hello?"})
		var refused *transfer.TextRefusedError
		if !errors.As(err, &refused) || refused.Reason != "" {
			t.Fatalf("SendText = %v, want a refusal without a reason", err)
		}
		none(t)
	})

	t.Run("too many at once", func(t *testing.T) {
		var err error
		sent := 0
		for i := 0; i < textsPerMinute+1 && err == nil; i++ {
			_, err = alice.SendText(ctx, bobID, protocol.Text{ID: transfer.NewTextID(), Body: "spam"})
			if err == nil {
				sent++
			}
		}
		var refused *transfer.TextRefusedError
		if !errors.As(err, &refused) || refused.Reason != TextBusy {
			t.Fatalf("after %d texts: SendText = %v, want refused as busy", sent, err)
		}
		// The earlier subtests count too: they arrived within the minute.
		if sent > textsPerMinute {
			t.Fatalf("%d texts were shown, at most %d allowed", sent, textsPerMinute)
		}
	})
}

func setPolicy(t *testing.T, n *testNode, peerID string, p peers.Policy) {
	t.Helper()
	if err := n.Peers.Update(peerID, func(x *peers.Peer) { x.Policy = p }); err != nil {
		t.Fatal(err)
	}
}

func TestTextLog(t *testing.T) {
	var l textLog
	now := time.Now()
	if seen, busy := l.admit("p", "a", now); seen || busy {
		t.Fatal("first text not admitted")
	}
	if seen, _ := l.admit("p", "a", now); !seen {
		t.Fatal("repeat not recognised")
	}
	if seen, _ := l.admit("q", "a", now); seen {
		t.Fatal("another peer's ID counted as a repeat")
	}
	for i := 1; i < textsPerMinute; i++ {
		if _, busy := l.admit("p", transfer.NewTextID(), now); busy {
			t.Fatalf("text %d refused", i+1)
		}
	}
	if _, busy := l.admit("p", "over", now); !busy {
		t.Fatal("text over the limit admitted")
	}
	if _, busy := l.admit("p", "later", now.Add(time.Minute)); busy {
		t.Fatal("a minute later, still refused")
	}
}
