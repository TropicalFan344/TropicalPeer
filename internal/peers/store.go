// Package peers stores the devices this device has paired with, together with
// the per-peer accept policy.
package peers

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// utf8BOM is tolerated at the start of JSON files, which Windows editors
// and PowerShell often add when someone edits them by hand.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

const fileName = "peers.json"

var ErrNotFound = errors.New("peer not found")

type Peer struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	PublicKey ed25519.PublicKey `json:"publicKey"`
	Policy    Policy            `json:"policy"`
	// MaxAutoSize limits auto-accept: offers larger than this fall back to Ask.
	// Zero means no limit.
	MaxAutoSize int64 `json:"maxAutoSize,omitempty"`
	// SaveDir overrides the default download folder for this peer.
	SaveDir string    `json:"saveDir,omitempty"`
	AddedAt time.Time `json:"addedAt"`
	// Order is the peer's place in the list the user arranged (1 = first).
	// Zero means never placed: those follow, by name.
	Order int `json:"order,omitempty"`
	// RemovedUs: the peer removed this device, so it refuses our
	// connections until the two pair again.
	RemovedUs bool `json:"removedUs,omitempty"`
}

// Removed is a peer this device removed whose notice of it hasn't reached
// the peer yet. It is kept until the notice is delivered or the two pair
// again, so the peer learns why it can't send instead of seeing "offline".
type Removed struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	PublicKey ed25519.PublicKey `json:"publicKey"`
	RemovedAt time.Time         `json:"removedAt"`
}

// removedKeep is how long a removal notice is waited on before it is
// given up on.
const removedKeep = 90 * 24 * time.Hour

const removedFile = "removed.json"

// Store is a thread-safe, JSON-file-backed list of paired peers.
type Store struct {
	mu      sync.RWMutex
	path    string
	peers   map[string]*Peer
	removed map[string]Removed
}

func Open(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, fileName), peers: map[string]*Peer{}, removed: map[string]Removed{}}
	if data, err := os.ReadFile(filepath.Join(dir, removedFile)); err == nil {
		var list []Removed
		if json.Unmarshal(bytes.TrimPrefix(data, utf8BOM), &list) == nil {
			for _, r := range list {
				if time.Since(r.RemovedAt) < removedKeep {
					s.removed[r.ID] = r
				}
			}
		}
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var list []*Peer
	if err := json.Unmarshal(bytes.TrimPrefix(data, utf8BOM), &list); err != nil {
		return nil, fmt.Errorf("parse peers: %w", err)
	}
	for _, p := range list {
		s.peers[p.ID] = p
	}
	return s, nil
}

// List returns a snapshot of all peers in the user's order; peers never
// placed come after, sorted by name.
func (s *Store) List() []Peer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Peer, 0, len(s.peers))
	for _, p := range s.peers {
		out = append(out, *p)
	}
	sort.SliceStable(out, func(i, j int) bool { return listedBefore(&out[i], &out[j]) })
	return out
}

// listedBefore orders placed peers by their place, then the rest by name.
func listedBefore(a, b *Peer) bool {
	if (a.Order > 0) != (b.Order > 0) {
		return a.Order > 0
	}
	if a.Order != b.Order {
		return a.Order < b.Order
	}
	return strings.ToLower(a.Name) < strings.ToLower(b.Name)
}

func (s *Store) Get(id string) (Peer, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.peers[id]
	if !ok {
		return Peer{}, ErrNotFound
	}
	return *p, nil
}

// Find looks a peer up by exact ID or case-insensitive name.
func (s *Store) Find(idOrName string) (Peer, error) {
	if p, err := s.Get(idOrName); err == nil {
		return p, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var match *Peer
	for _, p := range s.peers {
		if strings.EqualFold(p.Name, idOrName) {
			if match != nil {
				return Peer{}, fmt.Errorf("more than one peer is named %q; use the ID", idOrName)
			}
			match = p
		}
	}
	if match == nil {
		return Peer{}, ErrNotFound
	}
	return *match, nil
}

// ByKey returns the peer with the given public key.
func (s *Store) ByKey(pub ed25519.PublicKey) (Peer, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.peers {
		if p.PublicKey.Equal(pub) {
			return *p, true
		}
	}
	return Peer{}, false
}

// Add inserts or replaces a peer. Re-pairing an existing peer keeps its
// settings, including any nickname the user gave it.
func (s *Store) Add(p Peer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.peers[p.ID]; ok {
		p.Name, p.Policy, p.MaxAutoSize, p.SaveDir, p.AddedAt, p.Order = old.Name, old.Policy, old.MaxAutoSize, old.SaveDir, old.AddedAt, old.Order
	}
	if p.AddedAt.IsZero() {
		p.AddedAt = time.Now()
	}
	if p.Order == 0 {
		// Once the user has arranged the list, a new peer joins at the end.
		last := 0
		for _, q := range s.peers {
			last = max(last, q.Order)
		}
		if last > 0 {
			p.Order = last + 1
		}
	}
	s.peers[p.ID] = &p
	if _, ok := s.removed[p.ID]; ok {
		// Paired again: nothing left to tell it.
		delete(s.removed, p.ID)
		s.saveRemovedLocked()
	}
	return s.saveLocked()
}

// Update applies fn to the stored peer and persists the result.
func (s *Store) Update(id string, fn func(*Peer)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.peers[id]
	if !ok {
		return ErrNotFound
	}
	fn(p)
	return s.saveLocked()
}

// Reorder puts the peers in the order of ids. Peers not named keep their
// relative order after the named ones.
func (s *Store) Reorder(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	placed := map[string]bool{}
	n := 0
	for _, id := range ids {
		if p, ok := s.peers[id]; ok && !placed[id] {
			n++
			p.Order = n
			placed[id] = true
		}
	}
	rest := make([]*Peer, 0, len(s.peers))
	for _, p := range s.peers {
		if !placed[p.ID] {
			rest = append(rest, p)
		}
	}
	sort.SliceStable(rest, func(i, j int) bool { return listedBefore(rest[i], rest[j]) })
	for _, p := range rest {
		n++
		p.Order = n
	}
	return s.saveLocked()
}

// Remove forgets a peer. Unless it had removed us first, it is kept as
// Removed until it has been told.
func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.peers[id]
	if !ok {
		return ErrNotFound
	}
	delete(s.peers, id)
	if !p.RemovedUs {
		s.removed[id] = Removed{ID: p.ID, Name: p.Name, PublicKey: p.PublicKey, RemovedAt: time.Now()}
		s.saveRemovedLocked()
	}
	return s.saveLocked()
}

// RemovedPeer returns a peer this device removed and hasn't told yet.
func (s *Store) RemovedPeer(id string) (Removed, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.removed[id]
	return r, ok
}

// RemovedPeers lists the peers this device removed and hasn't told yet.
func (s *Store) RemovedPeers() []Removed {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Removed, 0, len(s.removed))
	for _, r := range s.removed {
		out = append(out, r)
	}
	return out
}

// Told records that a removed peer got the notice.
func (s *Store) Told(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.removed[id]; ok {
		delete(s.removed, id)
		s.saveRemovedLocked()
	}
}

func (s *Store) saveRemovedLocked() {
	list := make([]Removed, 0, len(s.removed))
	for _, r := range s.removed {
		list = append(list, r)
	}
	path := filepath.Join(filepath.Dir(s.path), removedFile)
	if len(list) == 0 {
		os.Remove(path)
		return
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	os.MkdirAll(filepath.Dir(path), 0o700)
	os.WriteFile(path, data, 0o600)
}

func (s *Store) saveLocked() error {
	list := make([]*Peer, 0, len(s.peers))
	for _, p := range s.peers {
		list = append(list, p)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
