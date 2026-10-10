package node

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/quic-go/quic-go"

	"TropicalPeer/internal/peers"
	"TropicalPeer/internal/rendezvous"
	"TropicalPeer/internal/transfer"
)

// Relay → direct upgrades.
//
// A transfer that starts on the relay keeps looking for a direct path in the
// background: every upgradeRetry the sender asks the server for a fresh
// introduction (both sides punch again, without a new relay session) and
// dials directly. Routers change: a port forward appears, a phone leaves a
// carrier network for Wi-Fi, a NAT mapping finally opens. When a direct
// connection comes up, the transfer joins it and moves the remaining bytes
// over, mid-file (see protocol and transfer packages).
var (
	upgradeRetry         = 5 * time.Second
	upgradeRetryHopeless = time.Minute // when both routers say direct can't work
	upgradeDialTimeout   = punchDuration
)

// recvSession is an incoming transfer that later connections may join.
type recvSession struct {
	in       *transfer.Incoming
	ctx      context.Context // the transfer's lifetime
	done     chan struct{}   // the session is over, connection included
	finished chan struct{}   // Receive has returned and reported
	stop     context.CancelCauseFunc
}

func recvKey(peerID, transferID string) string { return peerID + "/" + transferID }

// errSuperseded ends a receive whose sender has started over with a new
// attempt of the same transfer.
var errSuperseded = errors.New("the sender is trying again")

// stopReceiving closes every incoming transfer's connections as stopped.
func (n *Node) stopReceiving() {
	n.recvMu.Lock()
	var all []*recvSession
	for _, s := range n.receiving {
		all = append(all, s)
	}
	n.recvMu.Unlock()
	for _, s := range all {
		s.in.Stop()
	}
}

// supersede ends the running receive under key, if any, and waits until it
// has let go of its files.
func (n *Node) supersede(key string) {
	n.recvMu.Lock()
	old := n.receiving[key]
	n.recvMu.Unlock()
	if old == nil {
		return
	}
	old.stop(errSuperseded)
	select {
	case <-old.finished:
	case <-time.After(15 * time.Second):
	}
}

func (n *Node) registerRecv(key string, s *recvSession) {
	n.recvMu.Lock()
	if n.receiving == nil {
		n.receiving = map[string]*recvSession{}
	}
	n.receiving[key] = s
	n.recvMu.Unlock()
}

func (n *Node) unregisterRecv(key string, s *recvSession) {
	n.recvMu.Lock()
	if n.receiving[key] == s {
		delete(n.receiving, key)
	}
	n.recvMu.Unlock()
}

// joinIncoming attaches a new connection to a running incoming transfer.
func (n *Node) joinIncoming(ctx context.Context, peer peers.Peer, conn *quic.Conn, ctrl *transfer.Control, relayed bool) {
	n.recvMu.Lock()
	s := n.receiving[recvKey(peer.ID, ctrl.Hello.Join)]
	n.recvMu.Unlock()
	if s == nil {
		ctrl.RejectJoin("no such transfer")
		return
	}
	if err := s.in.Join(s.ctx, ctrl); err != nil {
		return
	}
	if n.events.OnPathChanged != nil {
		n.events.OnPathChanged(peer, ctrl.Hello.Join, relayed)
	}
	// Keep the connection until the transfer ends; then let the sender hang
	// up first so our last result isn't cut off.
	select {
	case <-s.done:
	case <-ctx.Done():
		return
	}
	select {
	case <-conn.Context().Done():
	case <-ctx.Done():
	case <-time.After(10 * time.Second):
	}
}

// upgrader looks for a direct path while a transfer runs on the relay.
type upgrader struct {
	mu    sync.Mutex
	conns []*quic.Conn
}

func (u *upgrader) closeAll() {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, c := range u.conns {
		c.CloseWithError(0, "")
	}
}

// run tries for a direct connection until one works or ctx ends, then hands
// it over on out.
func (u *upgrader) run(ctx context.Context, n *Node, peer peers.Peer, out chan<- *quic.Conn) {
	for {
		wait := upgradeRetry
		mine, _ := n.natProfile.Load().(rendezvous.NATProfile)
		if rendezvous.DirectHopeless(&mine, n.lastPeerNAT(peer.ID)) {
			wait = upgradeRetryHopeless
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		dctx, cancel := context.WithTimeout(ctx, upgradeDialTimeout)
		c, _, err := n.connect(dctx, peer, false)
		cancel()
		if err != nil {
			continue
		}
		u.mu.Lock()
		u.conns = append(u.conns, c)
		u.mu.Unlock()
		select {
		case out <- c:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (n *Node) lastPeerNAT(peerID string) *rendezvous.NATProfile {
	n.recvMu.Lock()
	defer n.recvMu.Unlock()
	return n.peerNAT[peerID]
}

func (n *Node) rememberPeerNAT(peerID string, p *rendezvous.NATProfile) {
	n.recvMu.Lock()
	defer n.recvMu.Unlock()
	if n.peerNAT == nil {
		n.peerNAT = map[string]*rendezvous.NATProfile{}
	}
	n.peerNAT[peerID] = p
}
