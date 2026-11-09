package transfer

import (
	"context"
	"errors"
	"testing"
	"time"

	"TropicalPeer/internal/protocol"
	"TropicalPeer/internal/wire"
)

// 0.2.4 and earlier, given a Hello with only a text, find it empty: they
// drop the stream and close the connection with code 0, answering nothing.
func TestOldReceiverHangsUpOnAText(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, server := quicPair(t, ctx)
	go func() {
		st, err := server.AcceptStream(ctx)
		if err != nil {
			return
		}
		var hello struct {
			Offer  *protocol.Offer `json:"offer"`
			Join   string          `json:"join"`
			Resume string          `json:"resume"`
		}
		wire.NewConn(st).Recv(&hello)
		st.CancelRead(0)
		server.CloseWithError(0, "")
	}()
	err := SendText(ctx, client, protocol.Text{ID: NewTextID(), Body: "hi"})
	if !errors.Is(err, ErrPeerOutdated) {
		t.Fatalf("SendText = %v, want ErrPeerOutdated", err)
	}
}

// A request this version doesn't know is answered "unsupported", so a newer
// sender can tell its user why.
func TestUnknownHelloIsAnswered(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, server := quicPair(t, ctx)
	accepted := make(chan error, 1)
	go func() {
		_, err := AcceptControl(ctx, server)
		accepted <- err
	}()
	st, err := client.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	w := wire.NewConn(st)
	if err := w.Send(map[string]any{"somethingNew": true}); err != nil {
		t.Fatal(err)
	}
	var ack protocol.JoinAck
	if err := w.Recv(&ack); err != nil {
		t.Fatal(err)
	}
	if ack.OK || ack.Error != protocol.Unsupported {
		t.Fatalf("ack = %+v, want unsupported", ack)
	}
	if err := <-accepted; !IsUnknownHello(err) {
		t.Fatalf("AcceptControl = %v, want an unknown Hello", err)
	}
}

func TestTextAnswers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, tc := range []struct {
		ok     bool
		reason string
		check  func(error) bool
	}{
		{ok: true, check: func(err error) bool { return err == nil }},
		{reason: "busy", check: func(err error) bool {
			var r *TextRefusedError
			return errors.As(err, &r) && r.Reason == "busy"
		}},
	} {
		client, server := quicPair(t, ctx)
		got := make(chan protocol.Text, 1)
		go func() {
			c, err := AcceptControl(ctx, server)
			if err != nil {
				return
			}
			got <- *c.Hello.Text
			c.Answer(tc.ok, tc.reason)
		}()
		sent := protocol.Text{ID: NewTextID(), Body: "https://example.com"}
		if err := SendText(ctx, client, sent); !tc.check(err) {
			t.Errorf("answer %v %q: SendText = %v", tc.ok, tc.reason, err)
		}
		if g := <-got; g != sent {
			t.Errorf("received %+v, want %+v", g, sent)
		}
	}
}

// A receiver from before notes ignores the offer's note and accepts without
// NoteShown: the sender hears the note didn't get there.
func TestOldReceiverDropsTheNote(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, server := quicPair(t, ctx)
	go func() {
		st, err := server.AcceptStream(ctx)
		if err != nil {
			return
		}
		w := wire.NewConn(st)
		var hello struct {
			Offer *struct {
				TransferID string `json:"transferId"`
			} `json:"offer"`
		}
		w.Recv(&hello)
		w.Send(protocol.Response{Accept: true})
	}()
	shown := make(chan bool, 1)
	sctx, stop := context.WithCancel(ctx)
	Send(sctx, client, NewTransferID(), manyFiles(1), SendOptions{
		Note:       "photos from yesterday",
		OnAccepted: func(noteShown bool) { shown <- noteShown; stop() },
	})
	if <-shown {
		t.Fatal("an old receiver's answer counted as showing the note")
	}
}

// A current receiver gets the note cleaned, and says it showed it.
func TestNoteIsShown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, server := quicPair(t, ctx)
	got := make(chan string, 1)
	go func() {
		c, err := AcceptControl(ctx, server)
		if err != nil {
			return
		}
		in := NewIncoming(c)
		in.Receive(ctx, ctx, t.TempDir(), func(_ context.Context, o protocol.Offer) (bool, string) {
			got <- o.Note
			return true, ""
		}, nil)
	}()
	shown := make(chan bool, 1)
	err := Send(ctx, client, NewTransferID(), manyFiles(1), SendOptions{
		Note:       "\u202Ephotos\r\nfrom yesterday",
		OnAccepted: func(noteShown bool) { shown <- noteShown },
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := <-got; n != "photos\nfrom yesterday" {
		t.Fatalf("receiver got note %q", n)
	}
	if !<-shown {
		t.Fatal("sender didn't hear the note was shown")
	}
}
