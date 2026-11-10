package node

import (
	"context"
	"time"

	"TropicalPeer/internal/peers"
	"TropicalPeer/internal/rendezvous"
)

// Paired peers' presence, from the rendezvous server (rendezvous.CapPresence):
// this device lists the peers it is paired with, and the server says which of
// them are online. A peer only reads as online when it lists this device too,
// so an older app that doesn't list anyone, or a server without presence,
// leaves its peers unknown rather than offline.

// watchPeers lists our peers with the server on a new registration.
func (n *Node) watchPeers(p *rendezvous.Presence) {
	if !p.Watches {
		return
	}
	n.presMu.Lock()
	n.peerOnline = map[string]bool{} // unknown until the server answers for each
	n.presMu.Unlock()
	n.sendWatch(p)
}

// rewatch sends the list again after pairing or removing a peer.
func (n *Node) rewatch() {
	p, _ := n.presence.Load().(*rendezvous.Presence)
	if p == nil || !p.Watches {
		return
	}
	n.presMu.Lock()
	if n.peerOnline != nil {
		have := map[string]bool{}
		for _, q := range n.Peers.List() {
			have[q.ID] = true
		}
		for id := range n.peerOnline {
			if !have[id] {
				delete(n.peerOnline, id)
			}
		}
	}
	n.presMu.Unlock()
	n.sendWatch(p)
}

func (n *Node) sendWatch(p *rendezvous.Presence) {
	list := n.Peers.List()
	ids := make([]string, 0, len(list))
	for _, q := range list {
		ids = append(ids, q.ID)
	}
	p.Watch(ids, func(id string, online bool) { n.onPeerPresence(p, id, online) })
}

// onPeerPresence records what the server said about peer id.
func (n *Node) onPeerPresence(p *rendezvous.Presence, id string, online bool) {
	if cur, _ := n.presence.Load().(*rendezvous.Presence); cur != p {
		return // from a registration that has ended
	}
	if _, err := n.Peers.Get(id); err != nil {
		return // removed meanwhile
	}
	n.presMu.Lock()
	if n.peerOnline == nil {
		n.presMu.Unlock()
		return
	}
	was, known := n.peerOnline[id]
	n.peerOnline[id] = online
	n.presMu.Unlock()

	// Seen when it comes online, and again as it leaves: "seen 2 h ago"
	// then means when it went away.
	if online || was {
		n.markSeen(id)
	}
	if (!known || was != online) && n.events.OnPresence != nil {
		n.events.OnPresence(id)
	}
}

// forgetPresence: the registration p ended, so no peer's presence is known.
// Peers that were online count as seen until now, unless the node is
// shutting down (nothing is written to the profile on the way out).
func (n *Node) forgetPresence(ctx context.Context, p *rendezvous.Presence) {
	if !p.Watches {
		return
	}
	n.presMu.Lock()
	was := n.peerOnline
	n.peerOnline = nil
	n.presMu.Unlock()
	if was == nil {
		return
	}
	if ctx.Err() == nil {
		for id, on := range was {
			if on {
				n.markSeen(id)
			}
		}
	}
	if n.events.OnPresence != nil {
		n.events.OnPresence("")
	}
}

func (n *Node) markSeen(id string) {
	now := time.Now().UnixMilli()
	n.Peers.Update(id, func(q *peers.Peer) { q.LastSeen = now })
}

// PeerPresence says whether peer id is online. known is false while the
// server can't tell (no connection, a server or peer app without presence,
// or no answer yet); online is then false too.
func (n *Node) PeerPresence(id string) (known, online bool) {
	n.presMu.Lock()
	defer n.presMu.Unlock()
	online, known = n.peerOnline[id]
	return known, online
}
