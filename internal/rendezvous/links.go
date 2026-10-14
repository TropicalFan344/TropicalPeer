package rendezvous

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
)

// CapLinks: the server routes browsers to links (share by link).
const CapLinks = "links"

// LinkIDLen is the length of a link ID in bytes before encoding.
const LinkIDLen = 10

var linkEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewLinkID returns a random link ID: 16 characters, A-Z and 2-7.
func NewLinkID() string {
	b := make([]byte, LinkIDLen)
	rand.Read(b)
	return linkEncoding.EncodeToString(b)
}

// ValidLinkID reports whether s looks like a link ID from NewLinkID.
func ValidLinkID(s string) bool {
	b, err := linkEncoding.DecodeString(s)
	return err == nil && len(b) == LinkIDLen && strings.ToUpper(s) == s
}

// LinkEvent is something the server tells a link's host: a browser opened
// the link (Visitor), sent signaling (Signal), or left (Drop).
type LinkEvent struct {
	Type    string // TypeLinkVisitor, TypeLinkSignal or TypeLinkDrop
	Session string
	ICE     []ICEServer     // Visitor: STUN/TURN servers to use for this session
	Data    json.RawMessage // Signal
}

// LinkHost is an open link: while it lasts the server routes browsers that
// open the link to us. It uses its own connection, separate from presence.
type LinkHost struct {
	Link   string
	WebURL string // where the server serves the receiver page, e.g. https://host/r/

	nc   net.Conn
	conn *Conn
	wmu  sync.Mutex

	Events <-chan LinkEvent // closed when the connection ends
}

// HostLink asks the server to route link to us.
func (c *Client) HostLink(ctx context.Context, link string) (*LinkHost, error) {
	nc, conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { nc.Close() })
	defer stop()
	if err := conn.Send(Msg{Type: TypeLinkHost, Link: link}); err != nil {
		nc.Close()
		return nil, err
	}
	m, err := conn.RecvMsg()
	if err != nil {
		nc.Close()
		var ne net.Error
		if errors.As(err, &ne) || ctx.Err() != nil {
			return nil, errors.New("lost the connection to the rendezvous server")
		}
		return nil, err
	}
	if m.Type != TypeLinkHosted {
		nc.Close()
		return nil, errors.New("this rendezvous server doesn't support links; it needs an update")
	}
	events := make(chan LinkEvent, 64)
	h := &LinkHost{Link: link, WebURL: m.WebURL, nc: nc, conn: conn, Events: events}
	go func() {
		defer close(events)
		for {
			var m Msg
			if err := conn.Recv(&m); err != nil {
				return
			}
			switch m.Type {
			case TypeLinkVisitor, TypeLinkSignal, TypeLinkDrop:
				events <- LinkEvent{Type: m.Type, Session: m.Session, ICE: m.ICE, Data: m.Data}
			}
		}
	}()
	return h, nil
}

// Signal sends WebRTC signaling to the browser of session.
func (h *LinkHost) Signal(session string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	h.wmu.Lock()
	defer h.wmu.Unlock()
	return h.conn.Send(Msg{Type: TypeLinkSignal, Session: session, Data: b})
}

// Drop disconnects the browser of session from the server.
func (h *LinkHost) Drop(session string) error {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	return h.conn.Send(Msg{Type: TypeLinkDrop, Session: session})
}

// Close stops routing the link; browsers still signaling are dropped.
func (h *LinkHost) Close() error { return h.nc.Close() }
