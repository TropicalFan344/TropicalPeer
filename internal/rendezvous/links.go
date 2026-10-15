package rendezvous

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CapLinks: the server routes browsers to links (share by link).
const CapLinks = "links"

// LinkIDLen is the length of a link ID in bytes before encoding.
const LinkIDLen = 10

var linkEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// LinkIDFor returns the ID of the link whose key is pub: 16 characters,
// A-Z and 2-7, from a hash of the key. Only the holder of the private key
// can host that ID on a server, and a browser checks the sender's signature
// against it, so having had the link doesn't let anyone pose as its sender.
func LinkIDFor(pub ed25519.PublicKey) string {
	h := sha256.Sum256(append([]byte("tropicalpeer-link-id\n"), pub...))
	return linkEncoding.EncodeToString(h[:LinkIDLen])
}

// LinkHostPayload is what a sender signs, with the link's key, to host it.
func LinkHostPayload(link string, unixTime int64) []byte {
	return []byte("tropicalpeer-link-host\n" + link + "\n" + strconv.FormatInt(unixTime, 10))
}

// ValidLinkID reports whether s looks like a link ID from LinkIDFor.
func ValidLinkID(s string) bool {
	b, err := linkEncoding.DecodeString(s)
	return err == nil && len(b) == LinkIDLen && strings.ToUpper(s) == s
}

// LinkEvent is something the server tells a link's host: a browser opened
// the link (Visitor), sent its key check or signaling (Signal), may now
// connect (Admitted), or left (Drop).
type LinkEvent struct {
	Type    string // TypeLinkVisitor, TypeLinkSignal, TypeLinkAdmitted or TypeLinkDrop
	Session string
	ICE     []ICEServer     // Admitted: STUN/TURN servers to use for this session
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

// HostLink asks the server to route the link of key to us.
func (c *Client) HostLink(ctx context.Context, key ed25519.PrivateKey) (*LinkHost, error) {
	pub := key.Public().(ed25519.PublicKey)
	link := LinkIDFor(pub)
	nc, conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { nc.Close() })
	defer stop()
	now := time.Now().Unix()
	req := Msg{Type: TypeLinkHost, Link: link, PublicKey: pub, Time: now, Sig: ed25519.Sign(key, LinkHostPayload(link, now))}
	if err := conn.Send(req); err != nil {
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
			case TypeLinkVisitor, TypeLinkSignal, TypeLinkAdmitted, TypeLinkDrop:
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

// Admit tells the server that session proved it has the link's secret: it
// gets STUN/TURN servers and may connect.
func (h *LinkHost) Admit(session string) error {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	return h.conn.Send(Msg{Type: TypeLinkAdmit, Session: session})
}

// Drop disconnects the browser of session from the server, telling the page
// why when code is set (an error code it knows, e.g. "auth" or "used").
func (h *LinkHost) Drop(session, code string) error {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	return h.conn.Send(Msg{Type: TypeLinkDrop, Session: session, Error: code})
}

// Close stops routing the link; browsers still signaling are dropped.
func (h *LinkHost) Close() error { return h.nc.Close() }
