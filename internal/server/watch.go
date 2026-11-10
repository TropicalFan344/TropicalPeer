package server

import (
	"time"

	"TropicalPeer/internal/rendezvous"
)

// Presence between paired devices. Each registered device may list the
// devices it is paired with (rendezvous.TypeWatch); the server then tells it
// which of those are online and pushes every change. A device counts as
// online to another only when each lists the other, so a stranger who knows a
// device's ID (or a device that was removed) always reads it as offline, the
// same answer it gets when the device really is offline. The lists live in
// memory only, for as long as each registration lasts.

// defaultOfflineGrace is how long a device that dropped its connection still
// counts as online: long enough to ride out a network switch or a reconnect
// without the other side's card flickering.
const defaultOfflineGrace = 20 * time.Second

type presencePush struct {
	to *presence
	m  rendezvous.Msg
}

func pushPresence(to *presence, id string, online bool) presencePush {
	return presencePush{to, rendezvous.Msg{Type: rendezvous.TypePresence, ID: id, Online: online}}
}

// sendPushes delivers presence pushes in order. pushMu keeps a device going
// offline and the same device's next registration from overtaking each other.
func (s *Server) sendPushes(pushes []presencePush) {
	for _, x := range pushes {
		x.to.send(x.m)
	}
}

// mutualLocked: a and b each list the other.
func mutualLocked(a, b *presence) bool {
	return a != nil && b != nil && a.watch[b.id] && b.watch[a.id]
}

// handleWatch replaces p's list of paired devices, answers for each of them,
// and tells the ones that just became (or stopped being) mutual about p.
func (s *Server) handleWatch(p *presence, ids []string) {
	next := map[string]bool{}
	for _, id := range ids {
		if len(next) == rendezvous.MaxWatch {
			break
		}
		if id != "" && len(id) <= 64 && id != p.id {
			next[id] = true
		}
	}

	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	var pushes []presencePush
	s.mu.Lock()
	if s.online[p.id] != p { // replaced by a newer registration meanwhile
		s.mu.Unlock()
		return
	}
	prev := p.watch
	p.watch = next
	for id := range next {
		q := s.online[id]
		mutual := mutualLocked(p, q)
		pushes = append(pushes, pushPresence(p, id, mutual))
		if mutual && !prev[id] {
			pushes = append(pushes, pushPresence(q, p.id, true))
		}
	}
	for id := range prev {
		if q := s.online[id]; !next[id] && q != nil && q.watch[p.id] {
			pushes = append(pushes, pushPresence(q, p.id, false))
		}
	}
	s.mu.Unlock()
	s.sendPushes(pushes)
}

// leftLater tells the devices that saw p online that it went offline, unless
// p registered again within the grace period and still lists them (then
// nothing changed for them).
func (s *Server) leftLater(p *presence) {
	s.mu.Lock()
	watched := len(p.watch) > 0
	grace := s.OfflineGrace
	s.mu.Unlock()
	if !watched {
		return
	}
	time.AfterFunc(grace, func() {
		s.pushMu.Lock()
		defer s.pushMu.Unlock()
		var pushes []presencePush
		s.mu.Lock()
		now := s.online[p.id] // a newer registration, if any
		for id := range p.watch {
			if q := s.online[id]; q != nil && q.watch[p.id] && !mutualLocked(now, q) {
				pushes = append(pushes, pushPresence(q, p.id, false))
			}
		}
		s.mu.Unlock()
		s.sendPushes(pushes)
	})
}
