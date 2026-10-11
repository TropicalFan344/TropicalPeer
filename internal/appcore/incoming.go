package appcore

import (
	"context"
	"errors"
	"log"
	"sort"
	"time"

	"TropicalPeer/internal/peers"
	"TropicalPeer/internal/protocol"
	"TropicalPeer/internal/transfer"
)

// offerTimeout is how long an "accept these files?" prompt stays open.
const offerTimeout = 3 * time.Minute

// OfferDTO is an incoming offer waiting for the user, pushed as "offer:new".
type OfferDTO struct {
	ID        string    `json:"id"`
	PeerID    string    `json:"peerId"`
	PeerName  string    `json:"peerName"`
	Files     []FileDTO `json:"files"`
	Total     int64     `json:"total"`
	SaveDir   string    `json:"saveDir"`
	ExpiresAt int64     `json:"expiresAt"` // unix ms
}

// OfferClosed tells the frontend a prompt went away without the user, as
// "offer:closed".
type OfferClosed struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

type pendingOffer struct {
	dto      OfferDTO
	decision chan bool
}

func incomingKey(peerID, transferID string) string { return peerID + "/" + transferID }

// onOffer runs on the node's goroutine and blocks until the user answers,
// the sender gives up, or the prompt expires.
func (c *Core) onOffer(ctx context.Context, peer peers.Peer, offer protocol.Offer) bool {
	po := &pendingOffer{
		dto: OfferDTO{
			ID:        transfer.NewTransferID(),
			PeerID:    peer.ID,
			PeerName:  peer.Name,
			Files:     toFileDTOs(offer.Files),
			Total:     offer.TotalSize(),
			SaveDir:   c.saveDirFor(peer),
			ExpiresAt: time.Now().Add(offerTimeout).UnixMilli(),
		},
		decision: make(chan bool, 1),
	}
	c.mu.Lock()
	c.offers[po.dto.ID] = po
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.offers, po.dto.ID)
		c.mu.Unlock()
	}()

	c.emit("offer:new", po.dto)

	select {
	case accept := <-po.decision:
		return accept
	case <-ctx.Done():
		c.emit("offer:closed", OfferClosed{ID: po.dto.ID, Reason: c.T("offer.peerCancelled", "name", peer.Name)})
	case <-time.After(offerTimeout):
		c.emit("offer:closed", OfferClosed{ID: po.dto.ID, Reason: c.T("offer.timedOut", "name", peer.Name)})
	}
	return false
}

// RespondOffer accepts or declines an incoming offer.
func (c *Core) RespondOffer(id string, accept bool) error {
	c.mu.RLock()
	po, ok := c.offers[id]
	c.mu.RUnlock()
	if !ok {
		return c.errT("offer.gone")
	}
	select {
	case po.decision <- accept:
	default: // already answered
	}
	return nil
}

// ListOffers returns the offers waiting for an answer, oldest first.
func (c *Core) ListOffers() []OfferDTO {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]OfferDTO, 0, len(c.offers))
	for _, po := range c.offers {
		out = append(out, po.dto)
	}
	sortOffers(out)
	return out
}

func (c *Core) onAccepted(peer peers.Peer, offer protocol.Offer, saveDir string, relayed bool, cancel, pause func()) {
	key := incomingKey(peer.ID, offer.TransferID)
	c.mu.Lock()
	st := c.incoming[key]
	if st == nil {
		st = c.stopped[key]
	}
	if st != nil {
		// The sender picked an interrupted transfer up again: carry on in
		// the same row.
		delete(c.stopped, key)
		d := &st.dto
		d.PeerName, d.SaveDir, d.Relayed, d.Upgraded = peer.Name, saveDir, relayed, false
		d.Status, d.Error, d.EndedAt, d.Speed, d.Saved, d.PausedByPeer = StatusReceiving, "", 0, 0, nil, false
		st.cancel, st.pause, st.lastAt = cancel, pause, time.Time{}
		c.transfers[d.ID] = st
		c.incoming[key] = st
		c.mu.Unlock()
		c.emitTransfer(st)
		return
	}
	c.mu.Unlock()
	st = &transferState{
		dto: TransferDTO{
			ID:        transfer.NewTransferID(),
			PeerID:    peer.ID,
			PeerName:  peer.Name,
			Direction: string(transfer.Receiving),
			Status:    StatusReceiving,
			Files:     toFileDTOs(offer.Files),
			Total:     offer.TotalSize(),
			SaveDir:   saveDir,
			Relayed:   relayed,
			StartedAt: time.Now().UnixMilli(),
		},
		cancel:   cancel,
		pause:    pause,
		remoteID: offer.TransferID,
	}
	c.mu.Lock()
	c.transfers[st.dto.ID] = st
	c.incoming[incomingKey(peer.ID, offer.TransferID)] = st
	c.mu.Unlock()
	c.emitTransfer(st)
}

func (c *Core) onIncomingProgress(peer peers.Peer, p transfer.Progress) {
	st := c.incomingState(peer.ID, p.TransferID)
	if st == nil {
		return
	}
	c.updateTransfer(st, func(d *TransferDTO) { applyProgress(st, d, p) })
}

func (c *Core) onReceived(peer peers.Peer, offer protocol.Offer, files []transfer.Result, err error) {
	st := c.incomingState(peer.ID, offer.TransferID)
	if st == nil {
		return // declined before it started; nothing to show
	}
	key := incomingKey(peer.ID, offer.TransferID)
	paused := errors.Is(err, transfer.ErrPaused) || transfer.IsPausedByPeer(err)
	interrupted := err != nil && !errors.Is(err, context.Canceled) && !transfer.IsCancelledByPeer(err) && !errors.Is(err, transfer.ErrCorrupted)
	c.mu.Lock()
	delete(c.incoming, key)
	if interrupted {
		c.stopped[key] = st
	}
	c.mu.Unlock()

	defer func() {
		d := c.snapshot(st)
		log.Printf("transfer %s: receive from %s ended %s (paused by peer %v, relayed %v): %s", d.ID, d.PeerName, d.Status, d.PausedByPeer, d.Relayed, transfer.Describe(err))
	}()
	c.updateTransfer(st, func(d *TransferDTO) {
		d.EndedAt = time.Now().UnixMilli()
		d.Speed = 0
		for _, f := range files {
			d.Saved = append(d.Saved, f.Path)
		}
		switch {
		case err == nil:
			d.Status, d.Done = StatusDone, d.Total
		case paused:
			d.Status, d.PausedByPeer = StatusPaused, transfer.IsPausedByPeer(err)
		case errors.Is(err, context.Canceled):
			d.Status = StatusCancelled
		case transfer.IsCancelledByPeer(err):
			d.Status, d.Error = StatusFailed, c.T("transfer.peerCancelled", "name", d.PeerName)
		case interrupted:
			d.Status, d.Error = StatusFailed, c.T("transfer.willResume", "name", d.PeerName)
		default:
			d.Status, d.Error = StatusFailed, c.friendlyTransferError(err)
		}
	})
}

func (c *Core) incomingState(peerID, transferID string) *transferState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.incoming[incomingKey(peerID, transferID)]
}

func (c *Core) saveDirFor(p peers.Peer) string {
	if p.SaveDir != "" {
		return p.SaveDir
	}
	return c.n().Settings().DownloadDir
}

func toFileDTOs(files []protocol.FileInfo) []FileDTO {
	out := make([]FileDTO, len(files))
	for i, f := range files {
		out[i] = FileDTO{Name: transfer.SafeName(f.Name), Size: f.Size}
	}
	return out
}

// onPathChanged notes an incoming transfer moving to another connection.
func (c *Core) onPathChanged(peer peers.Peer, transferID string, relayed bool) {
	st := c.incomingState(peer.ID, transferID)
	if st == nil {
		return
	}
	c.updateTransfer(st, func(d *TransferDTO) {
		if d.Relayed && !relayed {
			d.Upgraded = true
		}
		d.Relayed = relayed
	})
}

func sortOffers(o []OfferDTO) {
	sort.Slice(o, func(i, j int) bool { return o[i].ExpiresAt < o[j].ExpiresAt })
}
