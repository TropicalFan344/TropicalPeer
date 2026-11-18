package appcore

import (
	"context"
	"errors"
	"log"
	"strconv"
	"time"

	"TropicalPeer/internal/linkshare"
	"TropicalPeer/internal/transfer"
)

// Replies on share links: someone who opened a link offers files back (see
// package linkshare). They reach the person like files from a paired device,
// through the same prompt and the same row on Transfers.

// replyHooks: a link that asks for files waits for the answer as long as the
// visitor does, since the owner asked them to come; a reply gets the same
// time as an offer from a device.
func (c *Core) replyHooks(asked bool) *linkshare.ReplyHooks {
	wait := offerTimeout
	if asked {
		wait = 0
	}
	return &linkshare.ReplyHooks{
		Dir:  c.replyDir,
		Wait: wait,
		Ask: func(ctx context.Context, o linkshare.ReplyOffer) linkshare.ReplyAnswer {
			accept, expired := c.askOffer(ctx, OfferDTO{
				ID:          transfer.NewTransferID(),
				PeerID:      replyPeerID(o),
				PeerName:    o.Name,
				Files:       replyFiles(o),
				Total:       o.Total,
				SaveDir:     c.replyDir(),
				FromLink:    true,
				VisitorNote: o.Note,
			}, wait)
			switch {
			case accept:
				return linkshare.ReplyAccepted
			case expired:
				return linkshare.ReplyUnanswered
			}
			return linkshare.ReplyDeclined
		},
		Started: c.startReply,
	}
}

func (c *Core) replyDir() string { return c.n().Settings().DownloadDir }

// replyPeerID stands in for a device's ID: each visitor gets its own colour.
func replyPeerID(o linkshare.ReplyOffer) string { return "link:" + o.Session }

func replyFiles(o linkshare.ReplyOffer) []FileDTO {
	files := make([]FileDTO, len(o.Files))
	for i, f := range o.Files {
		files[i] = FileDTO{Name: f.Name, Size: f.Size}
	}
	return files
}

// startReply adds an accepted batch to Transfers.
func (c *Core) startReply(o linkshare.ReplyOffer, cancel func()) linkshare.ReplyRun {
	st := &transferState{
		dto: TransferDTO{
			ID:        o.ID,
			PeerID:    replyPeerID(o),
			PeerName:  o.Name,
			Direction: string(transfer.Receiving),
			Status:    StatusReceiving,
			Files:     replyFiles(o),
			Total:     o.Total,
			SaveDir:   c.replyDir(),
			Relayed:   o.Relayed,
			StartedAt: time.Now().UnixMilli(),
			FromLink:  true,
		},
		cancel: cancel,
	}
	c.mu.Lock()
	c.transfers[st.dto.ID] = st
	c.mu.Unlock()
	c.emitTransfer(st)
	return linkshare.ReplyRun{
		Progress: func(fileIndex int, done int64) {
			c.updateProgress(st, func(d *TransferDTO) {
				applyProgress(st, d, transfer.Progress{FileIndex: fileIndex, Done: done})
			})
		},
		Finished: func(saved []string, err error) { c.finishReply(st, saved, err) },
	}
}

func (c *Core) finishReply(st *transferState, saved []string, err error) {
	c.updateTransfer(st, func(d *TransferDTO) {
		d.EndedAt = time.Now().UnixMilli()
		d.Speed = 0
		d.Saved = saved
		switch {
		case err == nil:
			d.Status, d.Done = StatusDone, d.Total
		case errors.Is(err, linkshare.ErrReplyStopped):
			d.Status = StatusCancelled
		case errors.Is(err, linkshare.ErrReplyCancelled):
			d.Status, d.Error = StatusFailed, c.T("transfer.peerCancelled", "name", d.PeerName)
		case errors.Is(err, linkshare.ErrReplyLost):
			d.Status, d.Error = StatusFailed, c.T("links.replyLost", "name", d.PeerName)
		case errors.Is(err, linkshare.ErrReplyDamaged):
			d.Status, d.Error = StatusFailed, c.T("links.replyDamaged")
		default:
			d.Status, d.Error = StatusFailed, c.friendlyTransferError(err)
		}
		// What did arrive stays, and the row says so.
		if err != nil && len(saved) > 0 {
			d.Status = StatusFailed
			d.Error = c.T("links.replyPartial", "saved", strconv.Itoa(len(saved)), "total", strconv.Itoa(len(d.Files)))
		}
	})
	d := c.snapshot(st)
	log.Printf("transfer %s: reply from %s ended %s: %v", d.ID, d.PeerName, d.Status, err)
}
