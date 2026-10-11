package node

import (
	"context"
	"log"
	"time"

	"TropicalPeer/internal/peers"
	"TropicalPeer/internal/transfer"
)

// RemovedUsError means the peer removed this device: it refuses our
// connections until the two pair again.
type RemovedUsError struct{ Name string }

func (e *RemovedUsError) Error() string { return e.Name + " removed this device" }

// RemovePeer forgets a peer and tells it so (now, or the next time it tries
// to reach us), so it can say why sending fails instead of "offline".
func (n *Node) RemovePeer(id string) error {
	p, err := n.Peers.Get(id)
	if err != nil {
		return err
	}
	if err := n.Peers.Remove(id); err != nil {
		return err
	}
	if !p.RemovedUs {
		if r, ok := n.Peers.RemovedPeer(id); ok {
			go n.tellRemoved(r)
		}
	}
	return nil
}

// tellEvery spaces out removal notices to one peer that it received but
// didn't take (a version that doesn't know them).
const tellEvery = time.Minute

// tellRemoved delivers a removal notice, if one wasn't tried just now.
func (n *Node) tellRemoved(r peers.Removed) {
	n.tellMu.Lock()
	if time.Since(n.told[r.ID]) < tellEvery {
		n.tellMu.Unlock()
		return
	}
	if n.told == nil {
		n.told = map[string]time.Time{}
	}
	n.told[r.ID] = time.Now()
	n.tellMu.Unlock()

	n.mu.RLock()
	ctx := n.runCtx
	n.mu.RUnlock()
	if ctx == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, _, err := n.connect(ctx, peers.Peer{ID: r.ID, Name: r.Name, PublicKey: r.PublicKey}, true)
	if err != nil {
		// Offline: told when it next tries to reach us. Not reaching it
		// doesn't count against that.
		n.tellMu.Lock()
		delete(n.told, r.ID)
		n.tellMu.Unlock()
		return
	}
	defer conn.CloseWithError(0, "")
	if err := transfer.NotifyUnpaired(ctx, conn); err != nil && !transfer.IsRefusedByPeer(err) {
		return
	}
	// Delivered, or it had already removed us too: nothing left to tell.
	log.Printf("told %s it was removed", r.Name)
	n.Peers.Told(r.ID)
}

// tellAllRemoved retries every undelivered removal notice.
func (n *Node) tellAllRemoved() {
	for _, r := range n.Peers.RemovedPeers() {
		go n.tellRemoved(r)
	}
}

// markRemovedUs records that a peer removed this device.
func (n *Node) markRemovedUs(peer peers.Peer) {
	if peer.RemovedUs {
		return
	}
	if err := n.Peers.Update(peer.ID, func(p *peers.Peer) { p.RemovedUs = true }); err != nil {
		return
	}
	log.Printf("%s removed this device", peer.Name)
	if n.events.OnRemovedUs != nil {
		peer.RemovedUs = true
		n.events.OnRemovedUs(peer)
	}
}

// refused turns the other device refusing our certificate into
// RemovedUsError (and remembers it); other errors pass through.
func (n *Node) refused(peer peers.Peer, err error) error {
	if err != nil && transfer.IsRefusedByPeer(err) {
		n.markRemovedUs(peer)
		return &RemovedUsError{Name: peer.Name}
	}
	return err
}
