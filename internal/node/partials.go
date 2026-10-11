package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"TropicalPeer/internal/protocol"
	"TropicalPeer/internal/transfer"
)

// Partial files.
//
// An incoming transfer that breaks off (the connection drops, either app
// quits) keeps its .part files, and resume.json remembers them, so the next
// attempt continues where this one stopped (see protocol). Files a
// multi-file transfer already saved are remembered too, so its retry skips
// them. Cancelling discards the parts; anything left untouched for
// partialsKeep is cleaned up when the app starts.
const (
	partialsFile = "resume.json"
	partialsKeep = 7 * 24 * time.Hour
	partialsSave = time.Second // batch writes while files fly by
	// Files at least this big are recorded the moment they start.
	partialsSaveNow = 8 << 20
)

type partialEntry struct {
	Peer     string `json:"peer"`
	Transfer string `json:"transfer"`
	Index    int    `json:"index"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Part     string `json:"part,omitempty"`  // while the file is incomplete
	Final    string `json:"final,omitempty"` // the path it is meant to get
	Saved    string `json:"saved,omitempty"` // once it arrived in full
	// Offer fingerprints the accepted offer's file list: only that exact
	// offer may continue without asking again.
	Offer   string    `json:"offer,omitempty"`
	Updated time.Time `json:"updated"`
}

type partials struct {
	path string

	mu      sync.Mutex
	entries []partialEntry
	timer   *time.Timer
}

func loadPartials(dir string) *partials {
	p := &partials{path: filepath.Join(dir, partialsFile)}
	if b, err := os.ReadFile(p.path); err == nil {
		json.Unmarshal(b, &p.entries)
	}
	// Forget what's gone or stale, deleting stale parts.
	before := len(p.entries)
	kept := p.entries[:0]
	for _, e := range p.entries {
		stale := time.Since(e.Updated) > partialsKeep
		if e.Part != "" {
			if _, err := os.Stat(e.Part); err != nil {
				continue
			}
			if stale {
				os.Remove(e.Part)
				continue
			}
		} else if stale {
			continue
		}
		kept = append(kept, e)
	}
	p.entries = kept
	if len(kept) != before {
		p.save()
	}
	return p
}

// save writes resume.json soon, batching quick successive changes.
func (p *partials) saveSoon() {
	if p.timer == nil {
		p.timer = time.AfterFunc(partialsSave, func() {
			p.mu.Lock()
			p.timer = nil
			p.mu.Unlock()
			p.save()
		})
	}
}

func (p *partials) save() {
	p.mu.Lock()
	b, err := json.MarshalIndent(p.entries, "", "  ")
	p.mu.Unlock()
	if err != nil {
		return
	}
	tmp := p.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		os.Rename(tmp, p.path)
	}
}

// saveNow writes the file now, replacing any batched write.
func (p *partials) saveNow() {
	p.mu.Lock()
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	p.mu.Unlock()
	p.save()
}

// flush writes any batched change now (the app is stopping).
func (p *partials) flush() {
	p.mu.Lock()
	pending := p.timer != nil && p.timer.Stop()
	p.timer = nil
	p.mu.Unlock()
	if pending {
		p.save()
	}
}

// remove drops entries matching drop and reports whether there were any.
// Callers hold p.mu.
func (p *partials) remove(drop func(e partialEntry) bool) bool {
	kept := p.entries[:0]
	for _, e := range p.entries {
		if !drop(e) {
			kept = append(kept, e)
		}
	}
	changed := len(kept) != len(p.entries)
	p.entries = kept
	return changed
}

// accepted reports whether this exact offer (same transfer, same files)
// was accepted before and left something behind: then it continues without
// asking again. A different offer reusing the transfer ID is asked about
// like any new one.
func (p *partials) accepted(peer string, offer protocol.Offer) bool {
	fp := offer.Fingerprint()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.entries {
		if e.Peer == peer && e.Transfer == offer.TransferID && e.Offer == fp {
			return true
		}
	}
	return false
}

// forget drops a finished transfer's records.
func (p *partials) forget(peer, transferID string) {
	p.mu.Lock()
	changed := p.remove(func(e partialEntry) bool { return e.Peer == peer && e.Transfer == transferID })
	p.mu.Unlock()
	if changed {
		p.saveNow()
	}
}

// discard deletes a cancelled transfer's parts and drops its records.
func (p *partials) discard(peer, transferID string) {
	p.mu.Lock()
	changed := p.remove(func(e partialEntry) bool {
		if e.Peer != peer || e.Transfer != transferID {
			return false
		}
		if e.Part != "" {
			os.Remove(e.Part)
		}
		return true
	})
	p.mu.Unlock()
	if changed {
		p.saveNow()
	}
}

// forPeer is the view one incoming transfer works with.
func (p *partials) forPeer(peer string) transfer.Partials { return peerPartials{p, peer} }

type peerPartials struct {
	p    *partials
	peer string
}

func (v peerPartials) Find(transferID string, index int, f protocol.FileInfo) (string, string, bool) {
	v.p.mu.Lock()
	defer v.p.mu.Unlock()
	// The same file of the same transfer first; else the same name and
	// size sent again in a new transfer (the hashes decide if it really
	// is the same file).
	var found *partialEntry
	for i := range v.p.entries {
		e := &v.p.entries[i]
		if e.Peer != v.peer || e.Part == "" {
			continue
		}
		if e.Transfer == transferID && e.Index == index && e.Size == f.Size {
			found = e
			break
		}
		if found == nil && e.Name == f.Name && e.Size == f.Size {
			found = e
		}
	}
	if found == nil {
		return "", "", false
	}
	return found.Part, found.Final, true
}

func (v peerPartials) Saved(transferID string, index int) (string, bool) {
	v.p.mu.Lock()
	defer v.p.mu.Unlock()
	for _, e := range v.p.entries {
		if e.Peer == v.peer && e.Transfer == transferID && e.Index == index && e.Saved != "" {
			return e.Saved, true
		}
	}
	return "", false
}

func (v peerPartials) Started(transferID, fingerprint string, index int, f protocol.FileInfo, part, final string) {
	v.p.mu.Lock()
	v.p.remove(func(e partialEntry) bool {
		return e.Part == part || (e.Peer == v.peer && e.Transfer == transferID && e.Index == index)
	})
	v.p.entries = append(v.p.entries, partialEntry{
		Peer: v.peer, Transfer: transferID, Index: index, Name: f.Name, Size: f.Size,
		Part: part, Final: final, Offer: fingerprint, Updated: time.Now(),
	})
	now := f.Size >= partialsSaveNow
	if !now {
		v.p.saveSoon()
	}
	v.p.mu.Unlock()
	if now {
		// If the app is killed mid-file, a big part must be findable when
		// it comes back. Small files are cheap to send again.
		v.p.save()
	}
}

func (v peerPartials) Finished(transferID string, index int, path string) {
	v.p.mu.Lock()
	defer v.p.mu.Unlock()
	for i := range v.p.entries {
		e := &v.p.entries[i]
		if e.Peer == v.peer && e.Transfer == transferID && e.Index == index {
			e.Part, e.Final, e.Saved, e.Updated = "", "", path, time.Now()
		}
	}
	v.p.saveSoon()
}

func (v peerPartials) Discard(part string) {
	v.p.mu.Lock()
	defer v.p.mu.Unlock()
	if v.p.remove(func(e partialEntry) bool { return e.Part == part }) {
		v.p.saveSoon()
	}
}
