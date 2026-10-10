// Package server implements the TropicalPeer rendezvous server: it tracks which
// devices are online and where, pipes pairing traffic between two devices that
// share a nameplate, introduces devices that want to connect (so they can punch
// through their NATs), and optionally relays end-to-end encrypted QUIC packets
// when no direct path exists. It never sees file contents or pairing secrets.
package server

import (
	"context"
	"crypto/ed25519"
	crand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net"
	"strconv"
	"sync"
	"time"

	"TropicalPeer/internal/identity"
	"TropicalPeer/internal/rendezvous"
)

const (
	firstMsgTimeout  = 30 * time.Second
	nameplateTTL     = 5 * time.Minute
	pairExchangeTime = 2 * time.Minute
	pairByteLimit    = 256 * 1024 // pairing is a few small messages; this is not a relay
	maxClockSkew     = 5 * time.Minute
)

type presence struct {
	id    string
	pub   ed25519.PublicKey
	addrs []string
	nc    net.Conn

	wmu  sync.Mutex // serializes pushes on conn
	conn *rendezvous.Conn

	// Set by UDP probes (under Server.mu): the public address of the
	// device's QUIC socket as this server sees it.
	reflexive   string
	reflexiveAt time.Time
	nat         *rendezvous.NATProfile // as last reported by the device
}

func (p *presence) send(m rendezvous.Msg) error {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	return p.conn.Send(m)
}

type pending struct {
	conn   net.Conn
	reader io.Reader
	joined chan struct{}
	done   chan struct{}
}

type Server struct {
	Logger *log.Logger

	// Relay enables relaying for devices that can't reach each other
	// directly. RelayRate caps each relay session in bytes per second
	// (0 = unlimited).
	Relay     bool
	RelayRate int64

	mu         sync.Mutex
	online     map[string]*presence
	nameplates map[string]*pending
	sessions   map[uint64]*relaySession
	udp        net.PacketConn
	alt        net.PacketConn // second UDP port for router detection
	altPort    int
}

func New(logger *log.Logger) *Server {
	return &Server{
		Logger:     logger,
		Relay:      true,
		RelayRate:  32 << 20,
		online:     map[string]*presence{},
		nameplates: map[string]*pending{},
		sessions:   map[uint64]*relaySession{},
	}
}

// Serve accepts connections until ctx is cancelled.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		nc, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return err
		}
		go s.handle(nc)
	}
}

func (s *Server) handle(nc net.Conn) {
	conn := rendezvous.NewConn(nc)
	nc.SetDeadline(time.Now().Add(firstMsgTimeout))
	var m rendezvous.Msg
	if err := conn.Recv(&m); err != nil {
		nc.Close()
		return
	}
	nc.SetDeadline(time.Time{})

	var err error
	switch m.Type {
	case rendezvous.TypeRegister:
		err = s.handleRegister(nc, conn, m)
	case rendezvous.TypeLookup:
		err = s.handleLookup(conn, m)
	case rendezvous.TypePairCreate:
		err = s.handlePairCreate(nc, conn)
		return // connection ownership passes to the pipe
	case rendezvous.TypePairJoin:
		err = s.handlePairJoin(nc, conn, m)
		return
	default:
		err = fmt.Errorf("unknown message type %q", m.Type)
	}
	if err != nil {
		conn.Send(rendezvous.Msg{Type: rendezvous.TypeError, Error: err.Error()})
	}
	nc.Close()
}

func (s *Server) handleRegister(nc net.Conn, conn *rendezvous.Conn, m rendezvous.Msg) error {
	if len(m.PublicKey) != ed25519.PublicKeySize || identity.DeviceID(m.PublicKey) != m.ID {
		return errors.New("device ID does not match public key")
	}
	if d := time.Since(time.Unix(m.Time, 0)); d > maxClockSkew || d < -maxClockSkew {
		return errors.New("registration timestamp too far off; check the device clock")
	}
	if !ed25519.Verify(m.PublicKey, rendezvous.RegisterPayload(m.ID, m.Time, m.Port, m.Addrs), m.Sig) {
		return errors.New("bad registration signature")
	}
	if m.Port <= 0 || m.Port > 65535 {
		return errors.New("invalid port")
	}

	observedIP, _, _ := net.SplitHostPort(nc.RemoteAddr().String())
	addrs := dedupe(append([]string{net.JoinHostPort(observedIP, strconv.Itoa(m.Port))}, m.Addrs...))
	if len(addrs) > 16 {
		addrs = addrs[:16]
	}

	p := &presence{id: m.ID, pub: m.PublicKey, addrs: addrs, nc: nc, conn: conn}
	s.mu.Lock()
	if old, ok := s.online[m.ID]; ok {
		old.nc.Close() // newest registration wins
	}
	s.online[m.ID] = p
	s.mu.Unlock()
	s.Logger.Printf("online  %s %v", m.ID, addrs)

	defer func() {
		s.mu.Lock()
		if s.online[m.ID] == p {
			delete(s.online, m.ID)
		}
		s.mu.Unlock()
		s.Logger.Printf("offline %s", m.ID)
	}()

	s.mu.Lock()
	altPort, relay := s.altPort, s.Relay
	s.mu.Unlock()
	if err := p.send(rendezvous.Msg{Type: rendezvous.TypeRegistered, Observed: observedIP, AltPort: altPort, Relay: relay}); err != nil {
		return nil
	}
	// The device is online until this connection closes. Meanwhile it may
	// ask to be introduced to other devices.
	for {
		var req rendezvous.Msg
		if err := conn.Recv(&req); err != nil {
			return nil
		}
		switch req.Type {
		case rendezvous.TypeConnect:
			s.handleConnect(p, req)
		case rendezvous.TypeNATInfo:
			s.mu.Lock()
			p.nat = req.NAT
			s.mu.Unlock()
		}
	}
}

// handleConnect introduces two online devices to each other: each learns the
// other's candidate addresses at the same moment so both can start punching,
// and, when relaying is on, both get the same relay session ID.
func (s *Server) handleConnect(from *presence, m rendezvous.Msg) {
	reply := rendezvous.Msg{Type: rendezvous.TypeConnectInfo, Req: m.Req, To: m.To}

	s.mu.Lock()
	to, ok := s.online[m.To]
	if !ok || to == from {
		s.mu.Unlock()
		reply.Error = "offline"
		from.send(reply)
		return
	}
	fromCands, toCands := s.candidatesLocked(from), s.candidatesLocked(to)
	fromNAT, toNAT := from.nat, to.nat
	session, nonce := randomHex(rendezvous.SessionIDLen), randomHex(16)
	relay := s.Relay && !m.NoRelay
	if relay {
		id, _ := rendezvous.ParseSession(session)
		s.sessions[rendezvous.SessionKey(id)] = newRelaySession(from.id, to.id, s.RelayRate)
	}
	s.mu.Unlock()

	to.send(rendezvous.Msg{
		Type: rendezvous.TypeConnectOffer, From: from.id,
		Candidates: fromCands, Session: session, Nonce: nonce, Relay: relay, NAT: fromNAT,
	})
	reply.Candidates, reply.Session, reply.Nonce, reply.Relay, reply.NAT = toCands, session, nonce, relay, toNAT
	from.send(reply)
	s.Logger.Printf("connect %s -> %s (relay %v)", from.id, to.id, relay)
}

// candidatesLocked lists where a device may be reached: its real UDP mapping
// first (from probes), then its LAN addresses and TCP-observed IP.
func (s *Server) candidatesLocked(p *presence) []string {
	var out []string
	if p.reflexive != "" && time.Since(p.reflexiveAt) < 2*time.Minute {
		out = append(out, p.reflexive)
	}
	return dedupe(append(out, p.addrs...))
}

func (s *Server) handleLookup(conn *rendezvous.Conn, m rendezvous.Msg) error {
	s.mu.Lock()
	p, ok := s.online[m.ID]
	var addrs []string
	if ok {
		addrs = append(addrs, p.addrs...)
	}
	s.mu.Unlock()
	return conn.Send(rendezvous.Msg{Type: rendezvous.TypeLookupResult, ID: m.ID, Online: ok, Addrs: addrs})
}

func (s *Server) handlePairCreate(nc net.Conn, conn *rendezvous.Conn) error {
	p := &pending{conn: nc, reader: conn.Reader(), joined: make(chan struct{}), done: make(chan struct{})}

	s.mu.Lock()
	nameplate := s.allocNameplateLocked()
	if nameplate == "" {
		s.mu.Unlock()
		conn.Send(rendezvous.Msg{Type: rendezvous.TypeError, Error: "server busy, try again"})
		nc.Close()
		return nil
	}
	s.nameplates[nameplate] = p
	s.mu.Unlock()

	if err := conn.Send(rendezvous.Msg{Type: rendezvous.TypePairCreated, Nameplate: nameplate}); err != nil {
		s.releaseNameplate(nameplate, p)
		nc.Close()
		return nil
	}
	s.Logger.Printf("pair    nameplate %s created", nameplate)

	// If the creator hangs up early, the nameplate simply lingers until the TTL;
	// a joiner then sees the pipe close and reports the failure.
	select {
	case <-p.joined:
		<-p.done
	case <-time.After(nameplateTTL):
		conn.Send(rendezvous.Msg{Type: rendezvous.TypeError, Error: "pairing code expired"})
	}
	s.releaseNameplate(nameplate, p)
	nc.Close()
	return nil
}

func (s *Server) handlePairJoin(nc net.Conn, conn *rendezvous.Conn, m rendezvous.Msg) error {
	s.mu.Lock()
	p, ok := s.nameplates[m.Nameplate]
	if ok {
		delete(s.nameplates, m.Nameplate) // single use
	}
	s.mu.Unlock()
	if !ok {
		conn.Send(rendezvous.Msg{Type: rendezvous.TypeError, Error: "unknown or expired pairing code"})
		nc.Close()
		return nil
	}
	close(p.joined)
	defer close(p.done)
	defer nc.Close()

	creator := rendezvous.NewConn(p.conn)
	if err := creator.Send(rendezvous.Msg{Type: rendezvous.TypePairPeerReady}); err != nil {
		conn.Send(rendezvous.Msg{Type: rendezvous.TypeError, Error: "other device disconnected"})
		return nil
	}
	if err := conn.Send(rendezvous.Msg{Type: rendezvous.TypePairJoined}); err != nil {
		return nil
	}
	s.Logger.Printf("pair    nameplate %s joined", m.Nameplate)

	deadline := time.Now().Add(pairExchangeTime)
	nc.SetDeadline(deadline)
	p.conn.SetDeadline(deadline)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		io.Copy(p.conn, io.LimitReader(conn.Reader(), pairByteLimit))
		p.conn.Close()
	}()
	go func() {
		defer wg.Done()
		io.Copy(nc, io.LimitReader(p.reader, pairByteLimit))
		nc.Close()
	}()
	wg.Wait()
	return nil
}

func (s *Server) releaseNameplate(name string, p *pending) {
	s.mu.Lock()
	if s.nameplates[name] == p {
		delete(s.nameplates, name)
	}
	s.mu.Unlock()
}

// allocNameplateLocked picks a short unused number. Short codes are fine: the
// nameplate is only an address; the secret words are checked by the PAKE.
func (s *Server) allocNameplateLocked() string {
	for _, limit := range []int{100, 1000, 10000} {
		for range 50 {
			n := strconv.Itoa(1 + rand.IntN(limit-1))
			if _, used := s.nameplates[n]; !used {
				return n
			}
		}
	}
	return ""
}

func randomHex(n int) string {
	b := make([]byte, n)
	crand.Read(b)
	return hex.EncodeToString(b)
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, a := range in {
		if a != "" && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	return out
}
