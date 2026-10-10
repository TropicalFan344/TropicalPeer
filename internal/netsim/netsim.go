// Package netsim is an in-memory UDP "internet" with simulated home routers
// (NATs), used to test hole punching and relaying deterministically on one
// machine. Each socket is a net.PacketConn, so QUIC runs over it unchanged.
//
// NAT behaviour follows RFC 4787's two axes:
//   - mapping: whether one inside socket keeps the same public port for every
//     destination (endpoint-independent) or gets a new one per destination
//     (address-and-port-dependent, i.e. "symmetric");
//   - filtering: which outside senders may reach a mapping.
package netsim

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"
)

// Kind names the four classic NAT types.
type Kind int

const (
	FullCone          Kind = iota // anyone can reach a mapping
	AddressRestricted             // only IPs the inside host has sent to
	PortRestricted                // only IP:ports the inside host has sent to
	Symmetric                     // new mapping per destination, port-restricted
)

func (k Kind) String() string {
	return [...]string{"full-cone", "address-restricted", "port-restricted", "symmetric"}[k]
}

type packet struct {
	src, dst netip.AddrPort
	data     []byte
}

// Network routes packets between public hosts and NATs.
type Network struct {
	mu     sync.Mutex
	public map[netip.AddrPort]*Conn // sockets reachable directly
	nats   map[netip.Addr]*NAT      // NATs by public IP
}

func New() *Network {
	return &Network{public: map[netip.AddrPort]*Conn{}, nats: map[netip.Addr]*NAT{}}
}

// ListenPublic opens a socket directly on the internet at ip:port.
func (n *Network) ListenPublic(addr string) (*Conn, error) {
	ap, err := netip.ParseAddrPort(addr)
	if err != nil {
		return nil, err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, used := n.public[ap]; used {
		return nil, fmt.Errorf("netsim: %s already in use", ap)
	}
	c := newConn(n, ap, nil)
	n.public[ap] = c
	return c, nil
}

// NewNAT adds a router with the given public IP.
func (n *Network) NewNAT(publicIP string, kind Kind) *NAT {
	ip := netip.MustParseAddr(publicIP)
	nat := &NAT{
		net:      n,
		kind:     kind,
		publicIP: ip,
		nextPort: 40000,
		byKey:    map[mapKey]*mapping{},
		byPort:   map[uint16]*mapping{},
	}
	n.mu.Lock()
	n.nats[ip] = nat
	n.mu.Unlock()
	return nat
}

func (n *Network) route(p packet) {
	n.mu.Lock()
	if c, ok := n.public[p.dst]; ok {
		n.mu.Unlock()
		c.deliver(p)
		return
	}
	nat, ok := n.nats[p.dst.Addr()]
	n.mu.Unlock()
	if ok {
		nat.inbound(p)
	}
	// Anything else is an unroutable address: silently dropped, like the
	// real internet does with private or unused addresses.
}

// NAT is a simulated home router.
type NAT struct {
	net      *Network
	kind     Kind
	publicIP netip.Addr

	mu       sync.Mutex
	nextPort uint16
	nextHost byte
	byKey    map[mapKey]*mapping
	byPort   map[uint16]*mapping
}

type mapKey struct {
	inside netip.AddrPort
	dst    netip.AddrPort // zero unless the NAT is symmetric
}

type mapping struct {
	conn    *Conn
	public  netip.AddrPort
	dst     netip.AddrPort          // first destination (what a symmetric NAT keyed it by)
	allowed map[netip.AddrPort]bool // destinations sent to (for filtering)
}

// Listen opens a socket on a new host behind this NAT.
func (nat *NAT) Listen() *Conn {
	nat.mu.Lock()
	nat.nextHost++
	inside := netip.AddrPortFrom(netip.AddrFrom4([4]byte{192, 168, 1, 10 + nat.nextHost}), 50000)
	nat.mu.Unlock()
	return newConn(nat.net, inside, nat)
}

// SetKind changes the router's behaviour at runtime, as when it is
// reconfigured or starts forwarding a port. Existing mappings survive; when
// the router stops being symmetric, each inside socket keeps its oldest
// mapping (the one the outside world, e.g. the server, already knows).
func (nat *NAT) SetKind(kind Kind) {
	nat.mu.Lock()
	defer nat.mu.Unlock()
	nat.kind = kind
	byKey := map[mapKey]*mapping{}
	for port := uint16(40000); port < nat.nextPort; port++ { // oldest first
		m, ok := nat.byPort[port]
		if !ok {
			continue
		}
		key := mapKey{inside: m.conn.local}
		if kind == Symmetric {
			key.dst = m.dst
		}
		if _, taken := byKey[key]; !taken {
			byKey[key] = m
		}
	}
	nat.byKey = byKey
}

func (nat *NAT) outbound(c *Conn, dst netip.AddrPort) netip.AddrPort {
	nat.mu.Lock()
	defer nat.mu.Unlock()
	key := mapKey{inside: c.local}
	if nat.kind == Symmetric {
		key.dst = dst
	}
	m, ok := nat.byKey[key]
	if !ok {
		port := nat.nextPort
		nat.nextPort++
		m = &mapping{conn: c, public: netip.AddrPortFrom(nat.publicIP, port), dst: dst, allowed: map[netip.AddrPort]bool{}}
		nat.byKey[key] = m
		nat.byPort[port] = m
	}
	m.allowed[dst] = true
	return m.public
}

func (nat *NAT) inbound(p packet) {
	nat.mu.Lock()
	m, ok := nat.byPort[p.dst.Port()]
	pass := false
	if ok {
		switch nat.kind {
		case FullCone:
			pass = true
		case AddressRestricted:
			for a := range m.allowed {
				if a.Addr() == p.src.Addr() {
					pass = true
					break
				}
			}
		default: // port-restricted and symmetric
			pass = m.allowed[p.src]
		}
	}
	nat.mu.Unlock()
	if pass {
		p.dst = m.conn.local
		m.conn.deliver(p)
	}
}

// Conn is a simulated UDP socket.
type Conn struct {
	net   *Network
	local netip.AddrPort
	nat   *NAT // nil for public sockets

	inbox  chan packet
	closed chan struct{}
	once   sync.Once

	mu       sync.Mutex
	deadline time.Time
	wake     chan struct{} // closed and replaced when the deadline changes
}

func newConn(n *Network, local netip.AddrPort, nat *NAT) *Conn {
	return &Conn{
		net:    n,
		local:  local,
		nat:    nat,
		inbox:  make(chan packet, 4096),
		closed: make(chan struct{}),
		wake:   make(chan struct{}),
	}
}

func (c *Conn) deliver(p packet) {
	select {
	case c.inbox <- p:
	default: // full: dropped, like a real socket buffer overflow
	}
}

func (c *Conn) ReadFrom(b []byte) (int, net.Addr, error) {
	for {
		c.mu.Lock()
		deadline, wake := c.deadline, c.wake
		c.mu.Unlock()

		var timeout <-chan time.Time
		if !deadline.IsZero() {
			d := time.Until(deadline)
			if d <= 0 {
				return 0, nil, os.ErrDeadlineExceeded
			}
			t := time.NewTimer(d)
			defer t.Stop()
			timeout = t.C
		}
		select {
		case p := <-c.inbox:
			n := copy(b, p.data)
			return n, net.UDPAddrFromAddrPort(p.src), nil
		case <-c.closed:
			return 0, nil, net.ErrClosed
		case <-timeout:
			return 0, nil, os.ErrDeadlineExceeded
		case <-wake:
			// deadline changed; re-evaluate
		}
	}
}

func (c *Conn) WriteTo(b []byte, addr net.Addr) (int, error) {
	select {
	case <-c.closed:
		return 0, net.ErrClosed
	default:
	}
	ua, ok := addr.(*net.UDPAddr)
	if !ok {
		return 0, errors.New("netsim: not a UDP address")
	}
	dst := ua.AddrPort()
	dst = netip.AddrPortFrom(dst.Addr().Unmap(), dst.Port())
	src := c.local
	if c.nat != nil {
		src = c.nat.outbound(c, dst)
	}
	c.net.route(packet{src: src, dst: dst, data: append([]byte(nil), b...)})
	return len(b), nil
}

func (c *Conn) Close() error {
	c.once.Do(func() {
		close(c.closed)
		c.net.mu.Lock()
		if c.net.public[c.local] == c {
			delete(c.net.public, c.local)
		}
		c.net.mu.Unlock()
	})
	return nil
}

func (c *Conn) LocalAddr() net.Addr { return net.UDPAddrFromAddrPort(c.local) }

func (c *Conn) SetDeadline(t time.Time) error { return c.SetReadDeadline(t) }

func (c *Conn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	close(c.wake)
	c.wake = make(chan struct{})
	c.mu.Unlock()
	return nil
}

func (c *Conn) SetWriteDeadline(time.Time) error { return nil }

// SetReadBuffer and SetWriteBuffer accept quic-go's buffer sizing; the
// simulated socket's queue is fixed.
func (c *Conn) SetReadBuffer(int) error  { return nil }
func (c *Conn) SetWriteBuffer(int) error { return nil }
