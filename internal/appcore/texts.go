package appcore

import (
	"context"
	"errors"
	"log"
	"net/url"
	"strings"
	"time"

	"TropicalPeer/internal/node"
	"TropicalPeer/internal/peers"
	"TropicalPeer/internal/protocol"
	"TropicalPeer/internal/transfer"
)

// Texts sent and received this session (docs/plan-send-text.md). They live
// in memory only: closing the app forgets them.

// TextDTO is one text, pushed as "text:new" when it arrives or has been
// sent, and as "text:update" when it is read.
type TextDTO struct {
	ID        string `json:"id"`
	PeerID    string `json:"peerId"`
	PeerName  string `json:"peerName"`
	Direction string `json:"direction"` // "send" | "receive"
	Body      string `json:"body"`
	At        int64  `json:"at"` // unix ms
	// Unread: received and not yet copied or closed. An unread text keeps
	// an update from restarting the app (see busy).
	Unread  bool `json:"unread"`
	Relayed bool `json:"relayed"`
}

// textsKept bounds the session's texts; the oldest go first.
const textsKept = 50

// sendTextTimeout bounds finding the peer and hearing back from it.
const sendTextTimeout = 45 * time.Second

// SendText delivers a text to a peer and returns once it was shown there.
// Unlike files nothing is queued: an offline peer is an error.
func (c *Core) SendText(peerID, body string) (TextDTO, error) {
	peer, err := c.n().Peers.Get(peerID)
	if err != nil {
		return TextDTO{}, c.errT("transfer.peerGone")
	}
	if _, err := protocol.CleanText(body); err != nil {
		return TextDTO{}, errors.New(c.friendlyTextError(peer, err))
	}
	ctx, cancel := context.WithTimeout(c.ctx, sendTextTimeout)
	defer cancel()
	text := protocol.Text{ID: transfer.NewTextID(), Body: body}
	relayed, err := c.n().SendText(ctx, peer.ID, text)
	if err != nil {
		log.Printf("text %s: send to %s failed: %s", text.ID, peer.Name, transfer.Describe(err))
		return TextDTO{}, errors.New(c.friendlyTextError(peer, err))
	}
	clean, _ := protocol.CleanText(body)
	log.Printf("text %s: sent to %s (%d bytes, relayed %v)", text.ID, peer.Name, len(clean), relayed)
	d := TextDTO{ID: text.ID, PeerID: peer.ID, PeerName: peer.Name, Direction: string(transfer.Sending),
		Body: clean, At: time.Now().UnixMilli(), Relayed: relayed}
	c.addText(d)
	return d, nil
}

func (c *Core) onText(peer peers.Peer, text protocol.Text, relayed bool) {
	log.Printf("text %s: received from %s (%d bytes, relayed %v)", text.ID, peer.Name, len(text.Body), relayed)
	c.addText(TextDTO{ID: text.ID, PeerID: peer.ID, PeerName: peer.Name, Direction: string(transfer.Receiving),
		Body: text.Body, At: time.Now().UnixMilli(), Unread: true, Relayed: relayed})
}

func (c *Core) addText(d TextDTO) {
	c.mu.Lock()
	c.texts = append(c.texts, d)
	if len(c.texts) > textsKept {
		c.texts = append([]TextDTO(nil), c.texts[len(c.texts)-textsKept:]...)
	}
	c.mu.Unlock()
	c.emit("text:new", d)
}

// ListTexts returns this session's texts, newest first.
func (c *Core) ListTexts() []TextDTO {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]TextDTO, len(c.texts))
	for i, d := range c.texts {
		out[len(out)-1-i] = d
	}
	return out
}

// MarkTextRead records that a received text was seen (copied or closed).
func (c *Core) MarkTextRead(id string) {
	c.mu.Lock()
	var changed *TextDTO
	for i := range c.texts {
		if c.texts[i].ID == id && c.texts[i].Unread {
			c.texts[i].Unread = false
			d := c.texts[i]
			changed = &d
		}
	}
	c.mu.Unlock()
	if changed != nil {
		c.emit("text:update", *changed)
	}
}

// clearReadTextsLocked forgets texts that have been read. Callers hold c.mu.
func (c *Core) clearReadTextsLocked() {
	keep := c.texts[:0]
	for _, d := range c.texts {
		if d.Unread {
			keep = append(keep, d)
		}
	}
	c.texts = keep
}

// unreadTextsLocked reports whether a received text hasn't been seen yet.
// Callers hold c.mu.
func (c *Core) unreadTextsLocked() bool {
	for _, d := range c.texts {
		if d.Unread {
			return true
		}
	}
	return false
}

// cleanNote checks a note for files, worded for people when it can't go.
func (c *Core) cleanNote(note string) (string, error) {
	clean, err := protocol.CleanNote(note)
	switch {
	case errors.Is(err, protocol.ErrTextTooLong):
		return "", c.errT("text.noteTooLong")
	case err != nil:
		return "", c.errT("text.invalid")
	}
	return clean, nil
}

func (c *Core) friendlyTextError(peer peers.Peer, err error) string {
	var offline *node.OfflineError
	var removedUs *node.RemovedUsError
	var refused *transfer.TextRefusedError
	switch {
	case errors.Is(err, protocol.ErrTextEmpty):
		return c.T("text.empty")
	case errors.Is(err, protocol.ErrTextTooLong):
		return c.T("text.tooLong")
	case errors.Is(err, protocol.ErrTextInvalid):
		return c.T("text.invalid")
	case errors.Is(err, transfer.ErrPeerOutdated):
		return c.T("text.peerNeedsUpdate", "name", peer.Name)
	case errors.As(err, &removedUs):
		return c.T("transfer.removedYou", "name", peer.Name)
	case errors.As(err, &offline):
		return c.T("transfer.offline", "name", peer.Name)
	case errors.As(err, &refused) && refused.Reason == node.TextBusy:
		return c.T("text.busy", "name", peer.Name)
	case errors.As(err, &refused):
		return c.T("text.refused", "name", peer.Name)
	}
	return c.friendlyTransferError(err)
}

// WebAddress returns raw as a link the system browser may open: only http
// and https, with a host. A text's links come from another device, so
// anything else (file:, javascript:, an app's own scheme) is refused.
func WebAddress(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", false
	}
	return u.String(), true
}

// TextPreview is the start of a text for a notification: its first 100
// characters, on one line.
func TextPreview(body string) string {
	const max = 100
	line := strings.Join(strings.Fields(body), " ")
	if r := []rune(line); len(r) > max {
		return string(r[:max]) + "…"
	}
	return line
}
