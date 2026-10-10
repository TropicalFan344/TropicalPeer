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
}

// Store is a thread-safe, JSON-file-backed list of paired peers.
type Store struct {
	mu    sync.RWMutex
	path  string
	peers map[string]*Peer
}

func Open(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, fileName), peers: map[string]*Peer{}}
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

func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.peers[id]; !ok {
		return ErrNotFound
	}
	delete(s.peers, id)
	return s.saveLocked()
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
