package server

import (
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/turn/v5"
)

// quota counts TURN allocations per session (the TURN REST username's user
// part), per client address and in all, and remembers which session each
// client connection allocated for (its permissions depend on the session).
type quota struct {
	mu         sync.Mutex
	perSession map[string]int
	perIP      map[string]int
	owners     map[string]string // client 5-tuple → session
	total      int
}

func clientKey(a net.Addr) string { return a.Network() + " " + a.String() }

func ipOf(a net.Addr) string {
	host, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return a.String()
	}
	return host
}

// owner is the session a client connection's allocation belongs to.
func (q *quota) owner(client net.Addr) string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.owners[clientKey(client)]
}

// blockedPeer: addresses a relay never sends to, whatever the session.
func blockedPeer(ip net.IP) bool {
	ip4 := ip.To4()
	return ip4 == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip4[0] == 0 || ip4[0] >= 240 // "this network", reserved and broadcast
}

// isCGNAT: the carrier-grade NAT range, private in all but name.
func isCGNAT(ip net.IP) bool {
	ip4 := ip.To4()
	return ip4 != nil && ip4[0] == 100 && ip4[1]&0xc0 == 64
}

// sessionOf is the session in a TURN REST username ("expiry:session"); the
// hooks get it with or without the expiry.
func sessionOf(user string) string {
	if _, s, ok := strings.Cut(user, ":"); ok {
		return s
	}
	return user
}

func (q *quota) allow(username, _ string, src net.Addr) bool {
	session := sessionOf(username)
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.total < maxAllocations && q.perSession[session] < maxAllocationsPerSession &&
		q.perIP[ipOf(src)] < maxAllocationsPerIP
}

func (q *quota) add(src net.Addr, user string, n int) {
	session := sessionOf(user)
	ip := ipOf(src)
	q.mu.Lock()
	defer q.mu.Unlock()
	q.total += n
	q.perSession[session] += n
	if q.perSession[session] <= 0 {
		delete(q.perSession, session)
	}
	q.perIP[ip] += n
	if q.perIP[ip] <= 0 {
		delete(q.perIP, ip)
	}
	if n > 0 {
		q.owners[clientKey(src)] = session
	} else {
		delete(q.owners, clientKey(src))
	}
}

// limitedGenerator hands out relay sockets with a rate cap, and keeps
// track of them by session so a finished session's sockets can be closed.
type limitedGenerator struct {
	turn.RelayAddressGenerator
	rate     int64
	relays   *relaySockets
	publicIP *atomic.Pointer[net.IP] // announced instead of the generator's fixed address
}

func (g *limitedGenerator) AllocatePacketConn(c turn.AllocateListenerConfig) (net.PacketConn, net.Addr, error) {
	pc, addr, err := g.RelayAddressGenerator.AllocatePacketConn(c)
	if err != nil {
		return pc, addr, err
	}
	if ua, ok := addr.(*net.UDPAddr); ok && g.publicIP != nil {
		addr = &net.UDPAddr{IP: *g.publicIP.Load(), Port: ua.Port}
	}
	lc := &limitedConn{PacketConn: pc, session: sessionOf(c.UserID), relays: g.relays}
	if g.rate > 0 {
		lc.bucket = newBucket(g.rate)
	}
	g.relays.add(lc)
	return lc, addr, nil
}

// limitedConn is one allocation's relay socket. It caps the traffic, both
// directions together: packets to the peer come from the server's shared
// read loop, so over the cap they're dropped rather than waited for
// (WebRTC's congestion control then backs off); packets from the peer are
// read on the allocation's own goroutine, so that read waits instead.
type limitedConn struct {
	net.PacketConn
	bucket  *bucket // nil: no cap
	session string
	relays  *relaySockets
}

func (c *limitedConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	if c.bucket != nil && !c.bucket.take(len(p)) {
		return len(p), nil // dropped, as a congested network would
	}
	return c.PacketConn.WriteTo(p, addr)
}

func (c *limitedConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, addr, err := c.PacketConn.ReadFrom(p)
	if err == nil && c.bucket != nil {
		c.bucket.wait(n)
	}
	return n, addr, err
}

func (c *limitedConn) Close() error {
	c.relays.remove(c)
	return c.PacketConn.Close()
}

// relaySockets are the open relay sockets, by session. Relay ports are few
// (a home server may forward only ten), and an allocation lingers for its
// whole lifetime after its browser goes away; closing the sockets of a
// session that has ended gives its ports back at once.
type relaySockets struct {
	mu        sync.Mutex
	bySession map[string]map[*limitedConn]bool
}

func (r *relaySockets) add(c *limitedConn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bySession == nil {
		r.bySession = map[string]map[*limitedConn]bool{}
	}
	if r.bySession[c.session] == nil {
		r.bySession[c.session] = map[*limitedConn]bool{}
	}
	r.bySession[c.session][c] = true
}

func (r *relaySockets) remove(c *limitedConn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.bySession[c.session], c)
	if len(r.bySession[c.session]) == 0 {
		delete(r.bySession, c.session)
	}
}

// closeEnded closes the sockets of every session live no longer accepts.
func (r *relaySockets) closeEnded(live func(session string) bool) int {
	r.mu.Lock()
	var ended []*limitedConn
	for session, conns := range r.bySession {
		if live(session) {
			continue
		}
		for c := range conns {
			ended = append(ended, c)
		}
	}
	r.mu.Unlock()
	for _, c := range ended {
		c.Close()
	}
	return len(ended)
}

// bucket is a token bucket: rate bytes per second, a burst of one second.
type bucket struct {
	mu     sync.Mutex
	rate   float64
	tokens float64
	at     time.Time
}

func newBucket(rate int64) *bucket {
	return &bucket{rate: float64(rate), tokens: float64(rate), at: time.Now()}
}

func (b *bucket) refill() {
	now := time.Now()
	b.tokens = min(b.rate, b.tokens+now.Sub(b.at).Seconds()*b.rate)
	b.at = now
}

// take takes n if there's enough, without waiting.
func (b *bucket) take(n int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refill()
	if b.tokens < float64(n) {
		return false
	}
	b.tokens -= float64(n)
	return true
}

// wait takes n, sleeping off any shortfall.
func (b *bucket) wait(n int) {
	b.mu.Lock()
	b.refill()
	b.tokens -= float64(n)
	short := -b.tokens
	b.mu.Unlock()
	if short > 0 {
		time.Sleep(time.Duration(short / b.rate * float64(time.Second)))
	}
}
