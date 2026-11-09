package node

import (
	"context"
	"errors"
	"sync"
	"time"

	"TropicalPeer/internal/peers"
	"TropicalPeer/internal/protocol"
	"TropicalPeer/internal/transfer"
)

// Texts: short texts between paired devices, delivered while both are
// online and never stored on the way (see docs/plan-send-text.md).

const (
	// textsPerMinute bounds how many texts one peer can put on screen (and
	// in notifications) in a minute. More are refused with TextBusy.
	textsPerMinute = 30
	// textIDsKept is how many recent text IDs are remembered per peer, to
	// show a text sent again (its answer was lost) only once.
	textIDsKept = 200
)

// TextBusy is the refusal reason when a peer sends texts too fast.
const TextBusy = "busy"

// textLog remembers, per peer, recent text IDs and arrival times.
type textLog struct {
	mu    sync.Mutex
	peers map[string]*peerTexts
}

type peerTexts struct {
	ids    []string // oldest first, at most textIDsKept
	recent []time.Time
}

// admit decides about a text from peerID that arrived at now: seen means
// it was shown before; busy means too many arrived in the last minute.
// An admitted text is recorded.
func (l *textLog) admit(peerID, id string, now time.Time) (seen, busy bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.peers == nil {
		l.peers = map[string]*peerTexts{}
	}
	p := l.peers[peerID]
	if p == nil {
		p = &peerTexts{}
		l.peers[peerID] = p
	}
	for _, x := range p.ids {
		if x == id {
			return true, false
		}
	}
	keep := p.recent[:0]
	for _, t := range p.recent {
		if now.Sub(t) < time.Minute {
			keep = append(keep, t)
		}
	}
	p.recent = keep
	if len(p.recent) >= textsPerMinute {
		return false, true
	}
	p.recent = append(p.recent, now)
	p.ids = append(p.ids, id)
	if len(p.ids) > textIDsKept {
		p.ids = p.ids[len(p.ids)-textIDsKept:]
	}
	return false, false
}

// receiveText answers a text from peer: ok when it was shown.
func (n *Node) receiveText(peer peers.Peer, t protocol.Text, relayed bool) (ok bool, reason string) {
	if peer.Policy == peers.PolicyBlock {
		return false, "" // don't tell a blocked peer it is blocked
	}
	body, err := protocol.CleanText(t.Body)
	if err != nil || t.ID == "" || len(t.ID) > 64 {
		return false, "invalid text"
	}
	seen, busy := n.texts.admit(peer.ID, t.ID, time.Now())
	switch {
	case seen:
		return true, ""
	case busy:
		return false, TextBusy
	case n.events.OnText == nil:
		return false, "nobody is available to read it"
	}
	n.events.OnText(peer, protocol.Text{ID: t.ID, Body: body}, relayed)
	return true, ""
}

// SendText delivers a text to a paired peer and returns once it was shown
// there. relayed says the connection ran through a relay. The body is
// cleaned as the receiver will (protocol.CleanText) before it goes.
func (n *Node) SendText(ctx context.Context, peerID string, t protocol.Text) (relayed bool, err error) {
	body, err := protocol.CleanText(t.Body)
	if err != nil {
		return false, err
	}
	t.Body = body
	select {
	case <-n.ready:
	default:
		return false, errors.New("node is not running")
	}
	peer, err := n.Peers.Get(peerID)
	if err != nil {
		return false, err
	}
	if peer.RemovedUs {
		return false, &RemovedUsError{Name: peer.Name}
	}
	conn, relayed, err := n.connect(ctx, peer, true)
	if err != nil {
		return false, n.refused(peer, err)
	}
	defer conn.CloseWithError(0, "")
	return relayed, n.refused(peer, transfer.SendText(ctx, conn, t))
}
