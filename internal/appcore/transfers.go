package appcore

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"TropicalPeer/internal/node"
	"TropicalPeer/internal/peers"
	"TropicalPeer/internal/transfer"
)

// Transfer statuses as the frontend sees them.
const (
	StatusConnecting = "connecting" // finding and dialing the peer
	StatusWaiting    = "waiting"    // waiting for the peer to accept
	StatusSending    = "sending"
	StatusReceiving  = "receiving"
	StatusDone       = "done"
	StatusDeclined   = "declined"
	StatusFailed     = "failed"
	StatusCancelled  = "cancelled"
	StatusPaused     = "paused" // either side paused it; either side can resume
)

type FileDTO struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// TransferDTO is a snapshot of one transfer, pushed as "transfer:update".
type TransferDTO struct {
	ID           string    `json:"id"`
	PeerID       string    `json:"peerId"`
	PeerName     string    `json:"peerName"`
	Direction    string    `json:"direction"` // "send" | "receive"
	Status       string    `json:"status"`
	Files        []FileDTO `json:"files"`
	FileIndex    int       `json:"fileIndex"`
	Done         int64     `json:"done"`
	Total        int64     `json:"total"`
	Speed        float64   `json:"speed"`        // bytes per second, smoothed
	Relayed      bool      `json:"relayed"`      // runs through the rendezvous server's relay
	Upgraded     bool      `json:"upgraded"`     // moved from the relay to a direct path mid-transfer
	Resuming     bool      `json:"resuming"`     // send: the connection dropped; trying to pick up where it stopped
	PausedByPeer bool      `json:"pausedByPeer"` // paused on the other device, not here
	Error        string    `json:"error"`
	SaveDir      string    `json:"saveDir"`   // receive: destination folder
	Saved        []string  `json:"saved"`     // receive: full paths of saved files
	StartedAt    int64     `json:"startedAt"` // unix ms
	EndedAt      int64     `json:"endedAt"`   // unix ms, 0 while active
}

func (t *TransferDTO) active() bool {
	switch t.Status {
	case StatusConnecting, StatusWaiting, StatusSending, StatusReceiving:
		return true
	}
	return false
}

type transferState struct {
	dto      TransferDTO
	files    []transfer.Source // kept for retry
	cancel   func()            // a person cancelled: the other side discards what it has
	pause    func()            // a person paused: both sides keep what arrived
	stop     func()            // send: stop but keep what arrived, to resume later
	remoteID string            // receive: the sender's ID for the transfer (to ask it to resume)
	lastAt   time.Time
	lastDone int64
}

// Sending again after a dropped connection: the waits before each attempt.
// Together with the attempts themselves they cover a phone switching
// networks or the other app being restarted.
var resumeWaits = []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second, 30 * time.Second, 30 * time.Second, 60 * time.Second}

// SendFiles starts sending the files at paths to a peer and returns
// immediately. Progress arrives as "transfer:update" events.
func (c *Core) SendFiles(peerID string, paths []string) (TransferDTO, error) {
	if _, err := c.n().Peers.Get(peerID); err != nil {
		return TransferDTO{}, c.errT("transfer.peerGone")
	}
	sources, err := c.describeFiles(paths)
	if err != nil {
		return TransferDTO{}, err
	}
	return c.SendSources(peerID, sources)
}

// SendSources is SendFiles for files that aren't paths on disk, such as
// Android documents.
func (c *Core) SendSources(peerID string, sources []transfer.Source) (TransferDTO, error) {
	peer, err := c.n().Peers.Get(peerID)
	if err != nil {
		return TransferDTO{}, c.errT("transfer.peerGone")
	}
	if len(sources) == 0 {
		return TransferDTO{}, c.errT("transfer.chooseFile")
	}
	files := make([]FileDTO, len(sources))
	for i, src := range sources {
		files[i] = FileDTO{Name: transfer.SafeName(src.Name), Size: src.Size}
	}

	st := &transferState{
		dto: TransferDTO{
			ID:        transfer.NewTransferID(),
			PeerID:    peer.ID,
			PeerName:  peer.Name,
			Direction: string(transfer.Sending),
			Status:    StatusConnecting,
			Files:     files,
			StartedAt: time.Now().UnixMilli(),
		},
		files: sources,
	}
	for _, f := range files {
		st.dto.Total += f.Size
	}

	c.mu.Lock()
	c.transfers[st.dto.ID] = st
	c.mu.Unlock()
	c.startSend(st, peer)
	return c.snapshot(st), nil
}

// startSend runs (or runs again) an outgoing transfer.
func (c *Core) startSend(st *transferState, peer peers.Peer) {
	ctx, cancel := context.WithCancelCause(c.ctx)
	c.mu.Lock()
	st.cancel = func() { cancel(transfer.ErrCancelled) }
	st.pause = func() { cancel(transfer.ErrPaused) }
	st.stop = func() { cancel(nil) }
	st.lastAt = time.Time{}
	c.mu.Unlock()
	c.emitTransfer(st)
	go func() {
		defer cancel(nil)
		c.runSend(ctx, st, peer)
	}()
}

// runSend sends, and when the connection drops after the other device
// accepted, keeps trying to pick up where it stopped for a few minutes.
func (c *Core) runSend(ctx context.Context, st *transferState, peer peers.Peer) {
	accepted := false
	var err error
	for attempt := 0; ; attempt++ {
		err = c.sendOnce(ctx, st, peer, &accepted)
		if err == nil || !accepted || !resumable(err) || attempt >= len(resumeWaits) {
			break
		}
		log.Printf("transfer %s: attempt %d failed, trying again in %s: %v", st.dto.ID, attempt+1, resumeWaits[attempt], err)
		c.updateTransfer(st, func(d *TransferDTO) {
			d.Status, d.Resuming, d.Speed = StatusConnecting, true, 0
		})
		select {
		case <-time.After(resumeWaits[attempt]):
		case <-ctx.Done():
			err = ctx.Err()
		}
		if ctx.Err() != nil {
			break
		}
	}
	if errors.Is(context.Cause(ctx), transfer.ErrPaused) {
		err = transfer.ErrPaused
	}
	c.finishSend(st, err)
}

// resumable reports whether a failed send is worth trying again: the
// connection was lost, rather than someone cancelling or the file failing.
func resumable(err error) bool {
	var rejected *transfer.RejectedError
	switch {
	case errors.Is(err, context.Canceled), errors.As(err, &rejected), transfer.IsCancelledByPeer(err), transfer.IsPausedByPeer(err):
		return false
	case containsAny(err.Error(), "file changed", "the other device reported", "no such file", "cannot find"):
		return false
	}
	return true
}

// sendOnce makes one attempt. accepted turns true once bytes may flow.
func (c *Core) sendOnce(ctx context.Context, st *transferState, peer peers.Peer, accepted *bool) error {
	return c.n().SendSources(ctx, peer.ID, st.dto.ID, st.files, node.SendOptions{
		OnPhase: func(p node.SendPhase) {
			c.updateTransfer(st, func(d *TransferDTO) {
				if p == node.PhaseWaiting {
					d.Status = StatusWaiting
				}
			})
		},
		OnProgress: func(p transfer.Progress) {
			*accepted = true
			c.updateTransfer(st, func(d *TransferDTO) {
				d.Status, d.Resuming = StatusSending, false
				applyProgress(st, d, p)
			})
		},
		OnConnected: func(relayed bool) {
			c.updateTransfer(st, func(d *TransferDTO) {
				if d.Relayed && !relayed {
					d.Upgraded = true
				}
				d.Relayed = relayed
			})
		},
	})
}

func (c *Core) finishSend(st *transferState, err error) {
	c.updateTransfer(st, func(d *TransferDTO) {
		d.EndedAt = time.Now().UnixMilli()
		d.Speed, d.Resuming, d.PausedByPeer = 0, false, false
		var rejected *transfer.RejectedError
		var offline *node.OfflineError
		switch {
		case err == nil:
			d.Status, d.Done = StatusDone, d.Total
		case errors.Is(err, transfer.ErrPaused):
			d.Status = StatusPaused
		case transfer.IsPausedByPeer(err):
			d.Status, d.PausedByPeer = StatusPaused, true
		case errors.Is(err, context.Canceled):
			d.Status = StatusCancelled
		case errors.As(err, &rejected):
			d.Status, d.Error = StatusDeclined, rejected.Reason
		case errors.As(err, &offline):
			d.Status, d.Error = StatusFailed, c.T("transfer.offline", "name", d.PeerName)
		case transfer.IsCancelledByPeer(err):
			d.Status, d.Error = StatusFailed, c.T("transfer.peerCancelled", "name", d.PeerName)
		default:
			d.Status, d.Error = StatusFailed, c.friendlyTransferError(err)
		}
	})
}

// applyProgress records a progress report and updates the smoothed speed.
// Callers hold c.mu.
func applyProgress(st *transferState, d *TransferDTO, p transfer.Progress) {
	d.FileIndex = p.FileIndex
	d.Done = p.Done
	now := time.Now()
	if st.lastAt.IsZero() {
		st.lastAt, st.lastDone = now, p.Done
		return
	}
	if dt := now.Sub(st.lastAt).Seconds(); dt >= 0.25 {
		inst := float64(p.Done-st.lastDone) / dt
		if d.Speed == 0 {
			d.Speed = inst
		} else {
			d.Speed = 0.7*d.Speed + 0.3*inst
		}
		st.lastAt, st.lastDone = now, p.Done
	}
}

// CancelTransfer stops an active transfer.
func (c *Core) CancelTransfer(id string) {
	c.mu.Lock()
	st, ok := c.transfers[id]
	if !ok {
		c.mu.Unlock()
		return
	}
	if st.dto.Status == StatusPaused {
		// Nothing is running, so no connection carries the cancel: drop it
		// here (with any partial files) and tell the other side separately.
		receive := st.dto.Direction == string(transfer.Receiving)
		peerID, sharedID := st.dto.PeerID, st.dto.ID
		if receive {
			sharedID = st.remoteID
			delete(c.stopped, incomingKey(peerID, sharedID))
		}
		st.dto.Status, st.dto.PausedByPeer = StatusCancelled, false
		c.mu.Unlock()
		if receive {
			c.n().DiscardPartials(peerID, sharedID)
		}
		c.emitTransfer(st)
		go c.notifyCancel(peerID, sharedID)
		return
	}
	cancel := st.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// PauseTransfer stops an active transfer, keeping what arrived on both
// sides. ResumeTransfer continues it.
func (c *Core) PauseTransfer(id string) {
	c.mu.RLock()
	var pause func()
	if st, ok := c.transfers[id]; ok && st.dto.active() {
		pause = st.pause
	}
	c.mu.RUnlock()
	if pause != nil {
		pause()
	}
}

// ResumeTransfer continues a paused or interrupted transfer. An outgoing
// one is sent again from where the other device stopped; for an incoming
// one, the sender's app is asked to continue (it has to be open).
func (c *Core) ResumeTransfer(id string) (TransferDTO, error) {
	c.mu.RLock()
	st, ok := c.transfers[id]
	var peerID, remoteID, name string
	var receive bool
	if ok {
		receive = st.dto.Direction == string(transfer.Receiving)
		peerID, remoteID, name = st.dto.PeerID, st.remoteID, st.dto.PeerName
	}
	c.mu.RUnlock()
	if !ok {
		return TransferDTO{}, c.errT("transfer.notFound")
	}
	if !receive {
		return c.RetryTransfer(id)
	}
	ctx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
	defer cancel()
	err := c.n().RequestResume(ctx, peerID, remoteID)
	var refused *transfer.ResumeRefusedError
	switch {
	case err == nil:
		return c.snapshot(st), nil
	case errors.As(err, &refused):
		return TransferDTO{}, c.errT("transfer.resumeGone", "name", name)
	default:
		return TransferDTO{}, c.errT("transfer.resumeAskFailed", "name", name)
	}
}

// cancelNoticeWaits: the other device may be offline when a paused transfer
// is cancelled; keep trying to tell it for a minute or so.
var cancelNoticeWaits = []time.Duration{0, 5 * time.Second, 15 * time.Second, 40 * time.Second}

// notifyCancel tells the peer that a transfer we had paused (transferID is
// the sender's ID) was cancelled here.
func (c *Core) notifyCancel(peerID, transferID string) {
	for _, wait := range cancelNoticeWaits {
		select {
		case <-time.After(wait):
		case <-c.ctx.Done():
			return
		}
		ctx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
		err := c.n().NotifyCancel(ctx, peerID, transferID)
		cancel()
		var refused *transfer.ResumeRefusedError
		if err == nil || errors.As(err, &refused) {
			return
		}
	}
}

// onCancelNotice: the peer cancelled a paused or interrupted transfer
// between us. Our row says so; partial files we kept are deleted.
func (c *Core) onCancelNotice(peer peers.Peer, transferID string) {
	key := incomingKey(peer.ID, transferID)
	c.mu.Lock()
	st := c.stopped[key]
	if st != nil {
		delete(c.stopped, key)
	} else if s, ok := c.transfers[transferID]; ok && s.dto.Direction == string(transfer.Sending) && s.dto.PeerID == peer.ID {
		st = s
	}
	if st == nil || st.dto.active() || st.dto.Status == StatusDone || st.dto.Status == StatusCancelled {
		c.mu.Unlock()
		return
	}
	receive := st.dto.Direction == string(transfer.Receiving)
	d := &st.dto
	d.Status, d.PausedByPeer, d.Resuming = StatusFailed, false, false
	d.Error = c.T("transfer.peerCancelled", "name", peer.Name)
	c.mu.Unlock()
	if receive {
		c.n().DiscardPartials(peer.ID, transferID)
	}
	c.emitTransfer(st)
}

// onResumeRequest answers a receiver asking us to continue a transfer we
// sent it.
func (c *Core) onResumeRequest(peer peers.Peer, transferID string) (bool, string) {
	c.mu.RLock()
	st, ok := c.transfers[transferID]
	var status string
	if ok {
		status = st.dto.Status
		ok = st.dto.Direction == string(transfer.Sending) && st.dto.PeerID == peer.ID
	}
	c.mu.RUnlock()
	switch {
	case !ok:
		return false, "the transfer isn't in the list there anymore"
	case status == StatusDone || status == StatusCancelled || status == StatusDeclined:
		return false, "the transfer is " + status + " there"
	case st.dto.active():
		return true, ""
	}
	if _, err := c.RetryTransfer(transferID); err != nil {
		return false, err.Error()
	}
	return true, ""
}

// RetryTransfer sends a stopped transfer again. It continues where the
// other device stopped receiving, in the same row. A finished transfer is
// sent again as a new one.
func (c *Core) RetryTransfer(id string) (TransferDTO, error) {
	c.mu.Lock()
	st, ok := c.transfers[id]
	if !ok {
		c.mu.Unlock()
		return TransferDTO{}, c.errT("transfer.notFound")
	}
	if st.dto.Direction != string(transfer.Sending) {
		c.mu.Unlock()
		return TransferDTO{}, c.errT("transfer.onlySent")
	}
	if st.dto.active() {
		c.mu.Unlock()
		return c.snapshot(st), nil
	}
	if st.dto.Status == StatusDone {
		// Sending finished files again is a new transfer.
		peerID, files := st.dto.PeerID, st.files
		c.mu.Unlock()
		return c.SendSources(peerID, files)
	}
	peerID := st.dto.PeerID
	c.mu.Unlock()
	peer, err := c.n().Peers.Get(peerID)
	if err != nil {
		return TransferDTO{}, c.errT("transfer.peerGone")
	}
	c.mu.Lock()
	d := &st.dto
	d.PeerName = peer.Name
	d.Status, d.Error, d.EndedAt, d.Speed = StatusConnecting, "", 0, 0
	d.Relayed, d.Upgraded, d.Resuming = false, false, false
	c.mu.Unlock()
	c.startSend(st, peer)
	return c.snapshot(st), nil
}

// ListTransfers returns all transfers of this session, newest first.
func (c *Core) ListTransfers() []TransferDTO {
	c.mu.RLock()
	out := make([]TransferDTO, 0, len(c.transfers))
	for _, st := range c.transfers {
		out = append(out, c.snapshotLocked(st))
	}
	c.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt > out[j].StartedAt })
	return out
}

// ClearFinishedTransfers forgets transfers that are no longer active
// (paused ones stay: they aren't finished).
func (c *Core) ClearFinishedTransfers() {
	c.mu.Lock()
	for id, st := range c.transfers {
		if !st.dto.active() && st.dto.Status != StatusPaused {
			delete(c.transfers, id)
		}
	}
	c.mu.Unlock()
	c.emit("transfers:cleared")
}

func (c *Core) updateTransfer(st *transferState, fn func(*TransferDTO)) {
	c.mu.Lock()
	fn(&st.dto)
	c.mu.Unlock()
	c.emitTransfer(st)
}

func (c *Core) snapshot(st *transferState) TransferDTO {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshotLocked(st)
}

func (c *Core) snapshotLocked(st *transferState) TransferDTO {
	d := st.dto
	// Never nil: JSON null would break the frontend's list handling.
	d.Files = append([]FileDTO{}, d.Files...)
	d.Saved = append([]string{}, d.Saved...)
	return d
}

func (c *Core) emitTransfer(st *transferState) {
	c.emit("transfer:update", c.snapshot(st))
}

// describeFiles checks that every path is a file we can send.
func (c *Core) describeFiles(paths []string) ([]transfer.Source, error) {
	if len(paths) == 0 {
		return nil, c.errT("transfer.chooseFile")
	}
	files := make([]transfer.Source, 0, len(paths))
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return nil, c.errT("transfer.cantRead", "name", filepath.Base(p))
		}
		if st.IsDir() {
			return nil, c.errT("transfer.isFolder", "name", filepath.Base(p))
		}
		if !st.Mode().IsRegular() {
			return nil, c.errT("transfer.notRegular", "name", filepath.Base(p))
		}
		src, err := transfer.FileSource(p)
		if err != nil {
			return nil, c.errT("transfer.cantRead", "name", filepath.Base(p))
		}
		files = append(files, src)
	}
	return files, nil
}

func (c *Core) friendlyTransferError(err error) string {
	msg := err.Error()
	switch {
	case errors.Is(err, context.DeadlineExceeded), containsAny(msg, "timeout", "no recent network activity"):
		return c.T("transfer.timedOutConn")
	case containsAny(msg, "doesn't relay"):
		return c.T("transfer.noRelay")
	case containsAny(msg, "could not reach"):
		return c.T("transfer.unreachable")
	case containsAny(msg, "connect to rendezvous server"):
		return c.T("transfer.noServer")
	}
	return msg
}
