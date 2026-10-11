package node

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"

	"TropicalPeer/internal/peers"
	"TropicalPeer/internal/portmap"
	"TropicalPeer/internal/rendezvous"
)

// How a connection to a peer is found:
//
//  1. Both devices keep probing the rendezvous server's UDP port from their
//     QUIC socket. That tells the server each socket's public address (the
//     NAT mapping) and keeps the mapping alive.
//  2. The sender asks the server to introduce it to the receiver. Both get
//     the other's candidate addresses at the same moment, plus a relay
//     session ID.
//  3. Both fire punch packets at each other's candidates. Each outgoing
//     packet opens the sender's own NAT for replies from that address, so
//     once both have sent, packets get through in both directions.
//  4. Meanwhile the sender dials QUIC at every candidate. After relayDelay
//     (or once every direct attempt has failed) it also dials through the
//     relay. The first connection to complete its handshake wins.
//
// TLS key pinning is the same on every path, so a wrong or hijacked path
// simply fails its handshake.
const (
	probeInterval = 20 * time.Second
	punchInterval = 150 * time.Millisecond
	punchDuration = 6 * time.Second
	relayDelay    = 2 * time.Second
	dialTimeout   = 15 * time.Second
)

// clockBase anchors round-trip measurements to the monotonic clock (wall
// time on Windows is too coarse for sub-millisecond round trips).
var clockBase = time.Now()

// listenPacket opens a UDP socket, through the test hook when set.
func (n *Node) listenPacket(port int) (net.PacketConn, error) {
	if n.ListenPacket != nil {
		return n.ListenPacket(port)
	}
	return net.ListenUDP("udp", &net.UDPAddr{Port: port})
}

// serverUDP is the rendezvous server's UDP address: same host and port as
// its TCP address unless a test overrides it.
func (n *Node) serverUDP() (net.Addr, error) {
	if n.ServerUDPAddr != nil {
		return n.ServerUDPAddr()
	}
	return net.ResolveUDPAddr("udp", n.Settings().ServerAddr)
}

// controlLoop reads the non-QUIC packets that arrive on the main socket.
func (n *Node) controlLoop(ctx context.Context) {
	buf := make([]byte, 2048)
	for {
		size, _, err := n.transport.ReadNonQUICPacket(ctx, buf)
		if err != nil {
			return
		}
		kind, body, ok := rendezvous.Decode(buf[:size])
		if !ok {
			continue
		}
		switch kind {
		case rendezvous.KindProbeAck:
			if c, err := rendezvous.DecodeControl(body); err == nil {
				if c.Via == "alt" {
					n.altAck.Store(c.Addr)
				} else {
					n.mainAck.Store(c.Addr)
					if sent := n.probeSentAt.Load(); sent != 0 {
						n.serverRTT.Store(max(1, int64(time.Since(clockBase))-sent))
					}
					if old, _ := n.publicAddr.Load().(string); old != c.Addr {
						n.publicAddr.Store(c.Addr)
						n.emitNetwork()
					}
				}
			}
		case rendezvous.KindFilterTest:
			n.filterSeen.Store(true)
		case rendezvous.KindPunch:
			// Nothing to do: the punch has already done its job by arriving
			// (our own punches opened our NAT for this peer).
		}
	}
}

// PublicAddr is this device's UDP address as the rendezvous server sees it
// ("" until the first probe is answered).
func (n *Node) PublicAddr() string {
	if v, ok := n.publicAddr.Load().(string); ok {
		return v
	}
	return ""
}

// probeLoop keeps the server informed of our public UDP address while the
// presence registration p lasts.
func (n *Node) probeLoop(p *rendezvous.Presence) {
	t := time.NewTicker(probeInterval)
	defer t.Stop()
	for {
		n.sendProbe()
		select {
		case <-p.Done():
			return
		case <-t.C:
		}
	}
}

func (n *Node) sendProbe() {
	if server, err := n.serverUDP(); err == nil {
		n.probeSentAt.Store(int64(time.Since(clockBase)))
		n.sendProbeTo(server)
	}
}

func (n *Node) sendProbeTo(addr net.Addr) {
	c := rendezvous.SignControl(n.Identity.PrivateKey, rendezvous.KindProbe, rendezvous.UDPControl{ID: n.Identity.ID()})
	n.transport.WriteTo(rendezvous.EncodeControl(rendezvous.KindProbe, c), addr)
}

// punch sends punch packets to every candidate until ctx ends or
// punchDuration passes.
func (n *Node) punch(ctx context.Context, candidates []string, nonce string) {
	var targets []net.Addr
	for _, c := range candidates {
		if a, err := net.ResolveUDPAddr("udp", c); err == nil {
			targets = append(targets, a)
		}
	}
	if len(targets) == 0 {
		return
	}
	pkt := rendezvous.EncodeControl(rendezvous.KindPunch, rendezvous.UDPControl{Nonce: nonce})
	stop := time.After(punchDuration)
	t := time.NewTicker(punchInterval)
	defer t.Stop()
	for {
		for _, a := range targets {
			n.transport.WriteTo(pkt, a)
		}
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-t.C:
		}
	}
}

// onConnectOffer runs on the receiving side when a peer wants to reach us.
// Only a paired device gets a yes; until then the server tells the asking
// device nothing about us, not even that we're online.
func (n *Node) onConnectOffer(p *rendezvous.Presence, m rendezvous.Msg) {
	_, err := n.Peers.Get(m.From)
	paired := err == nil
	p.Answer(m, paired)
	if !paired {
		// Not a paired device: don't open anything for it. If we removed
		// it and it hasn't heard, tell it now that it's trying to reach us.
		if r, ok := n.Peers.RemovedPeer(m.From); ok {
			go n.tellRemoved(r)
		}
		return
	}
	n.mu.RLock()
	ctx := n.runCtx
	n.mu.RUnlock()
	if ctx == nil {
		return
	}
	go n.punch(ctx, m.Candidates, m.Nonce)
	if m.Relay {
		// Bind our end now, so a relayed dial from the sender lands.
		go func() {
			bctx, cancel := context.WithTimeout(ctx, dialTimeout)
			defer cancel()
			n.relay.bind(bctx, m.Session)
		}()
	}
}

// waitPresence returns the current registration, waiting briefly for one if
// the node has only just started (or is reconnecting).
func (n *Node) waitPresence(ctx context.Context) (*rendezvous.Presence, error) {
	deadline := time.Now().Add(10 * time.Second)
	for {
		if p, _ := n.presence.Load().(*rendezvous.Presence); p != nil {
			return p, nil
		}
		if time.Now().After(deadline) {
			return nil, errors.New("not connected to the rendezvous server")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

type dialResult struct {
	conn    *quic.Conn
	relayed bool
	err     error
}

// connect finds a path to peer: direct (LAN, or through both NATs) when
// possible, through the relay otherwise (unless allowRelay is false).
func (n *Node) connect(ctx context.Context, peer peers.Peer, allowRelay bool) (*quic.Conn, bool, error) {
	p, err := n.waitPresence(ctx)
	if err != nil {
		return nil, false, err
	}
	info, err := p.Connect(ctx, peer.ID, !allowRelay)
	if errors.Is(err, rendezvous.ErrPeerOffline) {
		return nil, false, &OfflineError{Name: peer.Name}
	}
	if err != nil {
		return nil, false, err
	}
	n.rememberPeerNAT(peer.ID, info.NAT)
	tlsConf, err := n.Identity.ClientTLS(peer.PublicKey)
	if err != nil {
		return nil, false, err
	}

	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	go n.punch(dialCtx, info.Candidates, info.Nonce)

	results := make(chan dialResult, len(info.Candidates)+1)
	attempts := 0
	directFailed := make(chan struct{}, len(info.Candidates))
	candidates := info.Candidates
	if DevRelayOnly && info.Relay {
		candidates = nil
	}
	for _, c := range candidates {
		addr, err := net.ResolveUDPAddr("udp", c)
		if err != nil {
			continue
		}
		attempts++
		go func() {
			conn, err := n.transport.Dial(dialCtx, addr, tlsConf, quicConfig)
			if err != nil {
				directFailed <- struct{}{}
			}
			results <- dialResult{conn: conn, err: err}
		}()
	}
	if info.Relay {
		attempts++
		directCount := attempts - 1
		delay := relayDelay
		mine, _ := n.natProfile.Load().(rendezvous.NATProfile)
		if rendezvous.DirectHopeless(&mine, info.NAT) {
			delay = relayDelayHopeless
		}
		go func() {
			// Give direct paths a head start; skip the wait once they've all failed.
			timer := time.NewTimer(delay)
			defer timer.Stop()
			for failed := 0; failed < directCount; {
				select {
				case <-timer.C:
					failed = directCount
					continue
				case <-directFailed:
					failed++
				case <-dialCtx.Done():
					results <- dialResult{err: dialCtx.Err()}
					return
				}
			}
			conn, err := n.dialRelay(dialCtx, info.Session, tlsConf)
			results <- dialResult{conn: conn, relayed: true, err: err}
		}()
	}
	if attempts == 0 {
		return nil, false, fmt.Errorf("no way to reach %s", peer.Name)
	}

	var winner dialResult
	var lastErr error
	for range attempts {
		r := <-results
		switch {
		case r.err != nil:
			lastErr = r.err
		case winner.conn == nil:
			winner = r
			cancel() // stop the other attempts and the punching
		default:
			r.conn.CloseWithError(0, "duplicate")
		}
	}
	if winner.conn == nil {
		if !info.Relay && allowRelay {
			return nil, false, fmt.Errorf("could not reach %s directly, and the server doesn't relay: %w", peer.Name, lastErr)
		}
		return nil, false, fmt.Errorf("could not reach %s: %w", peer.Name, lastErr)
	}
	return winner.conn, winner.relayed, nil
}

// DevRelayOnly makes outgoing connections skip direct paths and go through
// the relay. Only dev builds set it (TROPICALPEER_DEV_RELAY_ONLY), to test
// relayed transfers between devices that could reach each other directly.
var DevRelayOnly bool

// platformIPs, when set, replaces asking the OS for this device's addresses.
var platformIPs atomic.Pointer[[]net.IP]

// SetLocalIPs supplies this device's network addresses for platforms where Go
// can't list them itself: Android 11+ denies apps the netlink calls behind
// net.InterfaceAddrs, so the app passes what Android's ConnectivityManager
// reports, and again whenever the network changes.
// gateway is the router's address (nil if unknown), which NAT-PMP port
// forwarding needs and can't look up on Android either.
func SetLocalIPs(ips []net.IP, gateway net.IP) {
	platformIPs.Store(&ips)
	portmap.SetPlatformNetwork(ips, gateway)
}

// localAddrs lists this machine's addresses that peers on the same network
// (or the same machine) might reach us on.
func localAddrs(port int) []string {
	p := strconv.Itoa(port)
	out := []string{net.JoinHostPort("127.0.0.1", p)}
	var ips []net.IP
	if given := platformIPs.Load(); given != nil {
		ips = *given
	} else {
		ifaces, err := net.InterfaceAddrs()
		if err != nil {
			return out
		}
		for _, a := range ifaces {
			if ipnet, ok := a.(*net.IPNet); ok {
				ips = append(ips, ipnet.IP)
			}
		}
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
			continue
		}
		out = append(out, net.JoinHostPort(ip.String(), p))
	}
	return out
}
