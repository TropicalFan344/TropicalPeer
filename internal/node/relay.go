package node

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"sync"
	"time"

	"github.com/quic-go/quic-go"

	"TropicalPeer/internal/identity"
	"TropicalPeer/internal/rendezvous"
)

// relayAddr is the virtual address of the far end of a relay session.
type relayAddr [rendezvous.SessionIDLen]byte

func (a relayAddr) Network() string { return "tropicalpeer-relay" }
func (a relayAddr) String() string  { return "relay:" + hex.EncodeToString(a[:]) }

type relayPacket struct {
	from relayAddr
	data []byte
}

// relayConn is a net.PacketConn whose "addresses" are relay sessions. It
// sends every QUIC packet to the rendezvous server wrapped with the session
// ID, and unwraps what the server forwards back. QUIC runs over it unchanged
// and stays end-to-end encrypted: the server only ever sees ciphertext.
//
// It owns its own UDP socket (not the main QUIC one), so relayed traffic never
// competes with the main socket's small queue for non-QUIC packets.
type relayConn struct {
	sock   net.PacketConn
	server func() (net.Addr, error)
	id     *identity.Identity

	inbox  chan relayPacket
	closed chan struct{}
	once   sync.Once

	mu       sync.Mutex
	acks     map[relayAddr]chan struct{}
	bound    map[relayAddr]bool
	deadline time.Time
	wake     chan struct{}
}

func newRelayConn(sock net.PacketConn, server func() (net.Addr, error), id *identity.Identity) *relayConn {
	r := &relayConn{
		sock:   sock,
		server: server,
		id:     id,
		inbox:  make(chan relayPacket, 1024),
		closed: make(chan struct{}),
		acks:   map[relayAddr]chan struct{}{},
		bound:  map[relayAddr]bool{},
		wake:   make(chan struct{}),
	}
	go r.readLoop()
	return r
}

func (r *relayConn) readLoop() {
	buf := make([]byte, 64*1024)
	for {
		n, _, err := r.sock.ReadFrom(buf)
		if err != nil {
			r.Close()
			return
		}
		kind, body, ok := rendezvous.Decode(buf[:n])
		if !ok {
			continue
		}
		switch kind {
		case rendezvous.KindRelayData:
			sid, payload, err := rendezvous.DecodeRelayData(body)
			if err != nil {
				continue
			}
			select {
			case r.inbox <- relayPacket{from: sid, data: append([]byte(nil), payload...)}:
			default: // full: drop; QUIC retransmits
			}
		case rendezvous.KindBindAck:
			c, err := rendezvous.DecodeControl(body)
			if err != nil {
				continue
			}
			sid, err := rendezvous.ParseSession(c.Session)
			if err != nil {
				continue
			}
			r.mu.Lock()
			if !r.bound[sid] {
				r.bound[sid] = true
				if ch := r.acks[sid]; ch != nil {
					close(ch)
					delete(r.acks, sid)
				}
			}
			r.mu.Unlock()
		}
	}
}

// bind registers this socket as our end of a relay session, retrying until
// the server acknowledges or ctx ends.
func (r *relayConn) bind(ctx context.Context, session string) (relayAddr, error) {
	sid, err := rendezvous.ParseSession(session)
	if err != nil {
		return relayAddr{}, err
	}
	addr := relayAddr(sid)
	r.mu.Lock()
	if r.bound[addr] {
		r.mu.Unlock()
		return addr, nil
	}
	ack, ok := r.acks[addr]
	if !ok {
		ack = make(chan struct{})
		r.acks[addr] = ack
	}
	r.mu.Unlock()

	server, err := r.server()
	if err != nil {
		return addr, err
	}
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		pkt := rendezvous.EncodeControl(rendezvous.KindRelayBind, rendezvous.SignControl(r.id.PrivateKey, rendezvous.KindRelayBind,
			rendezvous.UDPControl{ID: r.id.ID(), Session: session}))
		r.sock.WriteTo(pkt, server)
		select {
		case <-ack:
			return addr, nil
		case <-ctx.Done():
			return addr, ctx.Err()
		case <-r.closed:
			return addr, net.ErrClosed
		case <-t.C:
		}
	}
}

func (r *relayConn) ReadFrom(b []byte) (int, net.Addr, error) {
	for {
		r.mu.Lock()
		deadline, wake := r.deadline, r.wake
		r.mu.Unlock()
		var timeout <-chan time.Time
		if !deadline.IsZero() {
			d := time.Until(deadline)
			if d <= 0 {
				return 0, nil, os.ErrDeadlineExceeded
			}
			timer := time.NewTimer(d)
			timeout = timer.C
			defer timer.Stop()
		}
		select {
		case p := <-r.inbox:
			return copy(b, p.data), p.from, nil
		case <-r.closed:
			return 0, nil, net.ErrClosed
		case <-timeout:
			return 0, nil, os.ErrDeadlineExceeded
		case <-wake:
		}
	}
}

func (r *relayConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	sid, ok := addr.(relayAddr)
	if !ok {
		return 0, errors.New("relay: not a relay address")
	}
	server, err := r.server()
	if err != nil {
		return 0, err
	}
	if _, err := r.sock.WriteTo(rendezvous.EncodeRelayData(sid, b), server); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (r *relayConn) Close() error {
	r.once.Do(func() {
		close(r.closed)
		r.sock.Close()
	})
	return nil
}

func (r *relayConn) LocalAddr() net.Addr { return r.sock.LocalAddr() }

func (r *relayConn) SetDeadline(t time.Time) error { return r.SetReadDeadline(t) }

func (r *relayConn) SetReadDeadline(t time.Time) error {
	r.mu.Lock()
	r.deadline = t
	close(r.wake)
	r.wake = make(chan struct{})
	r.mu.Unlock()
	return nil
}

func (r *relayConn) SetWriteDeadline(time.Time) error { return nil }

// Buffer sizing passes through to the real socket, so quic-go can enlarge it
// for fast relayed transfers like it does for direct ones.
func (r *relayConn) SetReadBuffer(bytes int) error {
	if s, ok := r.sock.(interface{ SetReadBuffer(int) error }); ok {
		return s.SetReadBuffer(bytes)
	}
	return nil
}

func (r *relayConn) SetWriteBuffer(bytes int) error {
	if s, ok := r.sock.(interface{ SetWriteBuffer(int) error }); ok {
		return s.SetWriteBuffer(bytes)
	}
	return nil
}

// dialRelay binds our end of the session and opens QUIC through the relay.
func (n *Node) dialRelay(ctx context.Context, session string, tlsConf *tls.Config) (*quic.Conn, error) {
	addr, err := n.relay.bind(ctx, session)
	if err != nil {
		return nil, err
	}
	return n.relayTransport.Dial(ctx, addr, tlsConf, quicConfig)
}
