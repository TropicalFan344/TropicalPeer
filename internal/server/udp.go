package server

import (
	"context"
	"net"
	"time"

	"TropicalPeer/internal/rendezvous"
)

const (
	relayIdleTimeout = 10 * time.Minute
	maxRelaySessions = 4096
)

// relaySession forwards packets between the two devices of one introduction.
// Each side "binds" by sending a signed packet from its relay socket, which
// tells the server where that side's packets come from.
type relaySession struct {
	ids     [2]string
	addrs   [2]net.Addr // set once each side binds
	created time.Time
	lastUse time.Time

	// Simple token bucket: bytes per second, burst of one second.
	rate   int64
	tokens float64
	refill time.Time
}

func newRelaySession(a, b string, rate int64) *relaySession {
	now := time.Now()
	return &relaySession{ids: [2]string{a, b}, created: now, lastUse: now, rate: rate, tokens: float64(rate), refill: now}
}

func (r *relaySession) allow(n int) bool {
	if r.rate <= 0 {
		return true
	}
	now := time.Now()
	r.tokens += now.Sub(r.refill).Seconds() * float64(r.rate)
	if r.tokens > float64(r.rate) {
		r.tokens = float64(r.rate)
	}
	r.refill = now
	if r.tokens < float64(n) {
		return false // over the cap: drop; QUIC's congestion control backs off
	}
	r.tokens -= float64(n)
	return true
}

// ServeUDP answers address probes and relays sessions until ctx is cancelled.
// It should listen on the same port number as the TCP listener.
func (s *Server) ServeUDP(ctx context.Context, pc net.PacketConn) error {
	s.mu.Lock()
	s.udp = pc
	s.mu.Unlock()
	go func() {
		<-ctx.Done()
		pc.Close()
	}()
	go s.expireSessions(ctx)

	buf := make([]byte, 64*1024)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		kind, body, ok := rendezvous.Decode(buf[:n])
		if !ok {
			continue
		}
		switch kind {
		case rendezvous.KindProbe:
			s.handleProbe(pc, from, body)
		case rendezvous.KindRelayBind:
			s.handleBind(pc, from, body)
		case rendezvous.KindRelayData:
			s.forward(pc, from, buf[:n], body)
		}
	}
}

// handleProbe records the public address of a device's QUIC socket and tells
// the device what it is. Probes are signed so nobody can redirect someone
// else's connections.
func (s *Server) handleProbe(pc net.PacketConn, from net.Addr, body []byte) {
	c, err := rendezvous.DecodeControl(body)
	if err != nil {
		return
	}
	s.mu.Lock()
	p, ok := s.online[c.ID]
	if !ok || !rendezvous.VerifyControl(p.pub, rendezvous.KindProbe, c) {
		s.mu.Unlock()
		return
	}
	p.reflexive, p.reflexiveAt = from.String(), time.Now()
	alt := s.alt
	s.mu.Unlock()
	pc.WriteTo(rendezvous.EncodeControl(rendezvous.KindProbeAck, rendezvous.UDPControl{Addr: from.String()}), from)
	if alt != nil {
		// Sent from a port the device hasn't talked to: only lenient routers
		// let it through. The device times this against its own probes.
		alt.WriteTo(rendezvous.EncodeControl(rendezvous.KindFilterTest, rendezvous.UDPControl{}), from)
	}
}

// EnableAlt records the second UDP port so registrations advertise it. Call
// it before Serve starts accepting devices; ServeUDPAlt calls it too.
func (s *Server) EnableAlt(pc net.PacketConn) {
	s.mu.Lock()
	s.alt = pc
	if ua, ok := pc.LocalAddr().(*net.UDPAddr); ok {
		s.altPort = ua.Port
	}
	s.mu.Unlock()
}

// ServeUDPAlt runs the second UDP port used for router detection. It only
// answers probes; comparing the address seen here with the one seen on the
// main port tells a device whether its router is symmetric.
func (s *Server) ServeUDPAlt(ctx context.Context, pc net.PacketConn) error {
	s.EnableAlt(pc)
	go func() {
		<-ctx.Done()
		pc.Close()
	}()
	buf := make([]byte, 2048)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		kind, body, ok := rendezvous.Decode(buf[:n])
		if !ok || kind != rendezvous.KindProbe {
			continue
		}
		c, err := rendezvous.DecodeControl(body)
		if err != nil {
			continue
		}
		s.mu.Lock()
		p, online := s.online[c.ID]
		valid := online && rendezvous.VerifyControl(p.pub, rendezvous.KindProbe, c)
		s.mu.Unlock()
		if valid {
			pc.WriteTo(rendezvous.EncodeControl(rendezvous.KindProbeAck, rendezvous.UDPControl{Addr: from.String(), Via: "alt"}), from)
		}
	}
}

func (s *Server) handleBind(pc net.PacketConn, from net.Addr, body []byte) {
	c, err := rendezvous.DecodeControl(body)
	if err != nil {
		return
	}
	sid, err := rendezvous.ParseSession(c.Session)
	if err != nil {
		return
	}
	s.mu.Lock()
	sess, ok := s.sessions[rendezvous.SessionKey(sid)]
	p, online := s.online[c.ID]
	if !ok || !online || !rendezvous.VerifyControl(p.pub, rendezvous.KindRelayBind, c) {
		s.mu.Unlock()
		return
	}
	side := -1
	for i, id := range sess.ids {
		if id == c.ID {
			side = i
		}
	}
	if side < 0 {
		s.mu.Unlock()
		return // not a member of this session
	}
	sess.addrs[side] = from
	sess.lastUse = time.Now()
	s.mu.Unlock()
	pc.WriteTo(rendezvous.EncodeControl(rendezvous.KindBindAck, rendezvous.UDPControl{Session: c.Session}), from)
}

func (s *Server) forward(pc net.PacketConn, from net.Addr, packet, body []byte) {
	sid, _, err := rendezvous.DecodeRelayData(body)
	if err != nil {
		return
	}
	s.mu.Lock()
	sess, ok := s.sessions[rendezvous.SessionKey(sid)]
	var to net.Addr
	if ok {
		switch {
		case sameAddr(sess.addrs[0], from):
			to = sess.addrs[1]
		case sameAddr(sess.addrs[1], from):
			to = sess.addrs[0]
		}
		if to != nil && !sess.allow(len(packet)) {
			to = nil
		}
		if to != nil {
			sess.lastUse = time.Now()
		}
	}
	s.mu.Unlock()
	if to != nil {
		pc.WriteTo(packet, to)
	}
}

func sameAddr(a, b net.Addr) bool { return a != nil && b != nil && a.String() == b.String() }

func (s *Server) expireSessions(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.mu.Lock()
		for k, sess := range s.sessions {
			if time.Since(sess.lastUse) > relayIdleTimeout {
				delete(s.sessions, k)
			}
		}
		// Hard cap: drop the oldest sessions if something floods introductions.
		for len(s.sessions) > maxRelaySessions {
			var oldestK uint64
			var oldest time.Time
			for k, sess := range s.sessions {
				if oldest.IsZero() || sess.created.Before(oldest) {
					oldestK, oldest = k, sess.created
				}
			}
			delete(s.sessions, oldestK)
		}
		s.mu.Unlock()
	}
}
