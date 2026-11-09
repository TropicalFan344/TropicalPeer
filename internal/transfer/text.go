package transfer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/quic-go/quic-go"

	"TropicalPeer/internal/protocol"
	"TropicalPeer/internal/wire"
)

// NewTextID makes a protocol.Text ID: 128 random bits, since the receiver
// uses it to recognise a text it was sent before.
func NewTextID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

var (
	// ErrPeerOutdated: the other device's app doesn't know the request
	// (it is older than this one).
	ErrPeerOutdated = errors.New("the other device needs a newer version of TropicalPeer")
	// errUnknownHello: AcceptControl answered a Hello it doesn't know.
	errUnknownHello = errors.New("control stream opened with a request this version doesn't know")
)

// IsUnknownHello reports whether AcceptControl failed because the Hello
// asked for something this version doesn't know. It has already answered
// so; the caller should let the answer go out before hanging up.
func IsUnknownHello(err error) bool { return errors.Is(err, errUnknownHello) }

// TextRefusedError: the other device didn't show the text (it blocks this
// device, or too many arrived at once). Reason may be empty.
type TextRefusedError struct{ Reason string }

func (e *TextRefusedError) Error() string {
	if e.Reason == "" {
		return "the other device didn't take the text"
	}
	return "the other device didn't take the text: " + e.Reason
}

// SendText delivers a text to the peer on conn and waits until it was
// shown. A device too old to know texts closes the connection without an
// answer; that is reported as ErrPeerOutdated.
func SendText(ctx context.Context, conn *quic.Conn, text protocol.Text) error {
	st, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return err
	}
	defer st.Close()
	stop := context.AfterFunc(ctx, func() { st.CancelRead(0); st.CancelWrite(0) })
	defer stop()
	w := wire.NewConnMax(st, maxControlLine)
	if err := w.Send(protocol.Hello{Text: &text}); err != nil {
		return err
	}
	var ack protocol.JoinAck
	if err := w.Recv(&ack); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Versions before texts treat the Hello as empty: they drop the
		// stream and close the connection, with code 0 and no answer.
		if code, ok := PeerCloseCode(err); ok && code == 0 {
			return ErrPeerOutdated
		}
		return fmt.Errorf("waiting for an answer: %w", err)
	}
	switch {
	case ack.OK:
		return nil
	case ack.Error == protocol.Unsupported:
		return ErrPeerOutdated
	}
	return &TextRefusedError{Reason: ack.Error}
}
