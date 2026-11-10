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
	// How long a device has to answer a connect offer. No answer counts as
	// no: the device asking is told the other one is offline.
	answerTimeout = 10 * time.Second
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

	answers bool // answers connect offers (rendezvous.CapAnswer)
	turn    bool // takes a TURN relay with a connect offer (rendezvous.CapTURN)

	watch map[string]bool // the devices it says it is paired with (watch.go), under Server.mu
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

	// Links enables share by link (nil = off). Set before serving.
	Links *Links

	// AppTURN, when set, also gives each introduction a TURN relay of its
	// own (Cloudflare's): the target device reserves an address there and
	// the requester dials it, which works between any two routers. It is
	// one more relayed path next to Relay. Set before serving.
	AppTURN TURNProvider

	// OfflineGrace is how long a device that dropped off still reads as
	// online to its paired devices (watch.go). Set before serving.
	OfflineGrace time.Duration

	pushMu     sync.Mutex // orders presence pushes (watch.go)
	mu         sync.Mutex
	online     map[string]*presence
	nameplates map[string]*pending
	sessions   map[uint64]*relaySession
	waiting    map[string]*waitingAnswer // connect offers awaiting the target's answer, by session
	udp        net.PacketConn
	alt        net.PacketConn // second UDP port for router detection
	altPort    int
	links      map[string]*linkHost
	linkLost   map[string]time.Time // when a link's host went away (it may come back)
	turn       map[string]*turnSession
	opens      map[string]int // link opens per IP this minute
	opensReset time.Time
	appTURN    map[string]appTURNSession // by session ID
}

// TURNProvider mints TURN credentials for a session and revokes them.
type TURNProvider interface {
	ICE(session string) []rendezvous.ICEServer
	Revoke(session string)
}

// appTURNSession is an introduction that got an AppTURN relay.
type appTURNSession struct {
	ends    [2]*presence // the two devices introduced
	created time.Time
}

// maxAppTURNPerHour caps the AppTURN sessions a device starts or receives
// in an hour: every one is relay allowance someone pays for, while a
// transfer that keeps reconnecting needs only a few.
const maxAppTURNPerHour = 30

func New(logger *log.Logger) *Server {
	return &Server{
		Logger:       logger,
		Relay:        true,
		RelayRate:    32 << 20,
		OfflineGrace: defaultOfflineGrace,
		online:       map[string]*presence{},
		nameplates:   map[string]*pending{},
		sessions:     map[uint64]*relaySession{},
		waiting:      map[string]*waitingAnswer{},
		appTURN:      map[string]appTURNSession{},
	}
}

// waitingAnswer is a connect offer sent to a device that answers offers.
type waitingAnswer struct {
	to     *presence
	answer chan rendezvous.Msg
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
	case rendezvous.TypePairCreate:
		err = s.handlePairCreate(nc, conn)
		return // connection ownership passes to the pipe
	case rendezvous.TypePairJoin:
		err = s.handlePairJoin(nc, conn, m)
		return
	case rendezvous.TypeLinkHost:
		err = s.handleLinkHost(nc, conn, m)
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
	for _, c := range m.Caps {
		switch c {
		case rendezvous.CapAnswer:
			p.answers = true
		case rendezvous.CapTURN:
			p.turn = true
		}
	}
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
		s.leftLater(p)
	}()

	s.mu.Lock()
	altPort, relay := s.altPort, s.Relay
	s.mu.Unlock()
	reg := rendezvous.Msg{Type: rendezvous.TypeRegistered, Observed: observedIP, AltPort: altPort, Relay: relay,
		Caps: []string{rendezvous.CapPresence}}
	if s.Links != nil {
		reg.Caps, reg.WebURL = append(reg.Caps, rendezvous.CapLinks), s.Links.WebURL
	}
	if err := p.send(reg); err != nil {
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
		case rendezvous.TypeWatch:
			s.handleWatch(p, req.Watch)
		case rendezvous.TypeNATInfo:
			s.mu.Lock()
			p.nat = req.NAT
			s.mu.Unlock()
		case rendezvous.TypeConnectAnswer:
			s.mu.Lock()
			w := s.waiting[req.Session]
			s.mu.Unlock()
			if w != nil && w.to == p { // only the device asked may answer
				select {
				case w.answer <- req:
				default:
				}
			}
		}
	}
}

// handleConnect introduces two online devices to each other: the target
// hears about the requester first, and once it accepts (it only accepts
// devices it is paired with) the requester learns the target's candidate
// addresses, so both punch at about the same time. When relaying is on,
// both get the same relay session ID.
//
// Anyone can register a key and ask, so the requester is told nothing
// about the target (not even that it is online) unless the target says yes.
// Devices that predate answering are introduced right away, as before.
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
	fromCands, fromNAT := s.candidatesLocked(from), from.nat
	session, nonce := randomHex(rendezvous.SessionIDLen), randomHex(16)
	relay := s.Relay && !m.NoRelay
	var sessKey uint64
	if relay {
		id, _ := rendezvous.ParseSession(session)
		sessKey = rendezvous.SessionKey(id)
		s.sessions[sessKey] = newRelaySession(from.id, to.id, s.RelayRate)
	}
	var w *waitingAnswer
	if to.answers {
		w = &waitingAnswer{to: to, answer: make(chan rendezvous.Msg, 1)}
		s.waiting[session] = w
	}
	// Only a device that answers can hand back the address it reserved.
	appTURN := s.AppTURN != nil && !m.NoRelay && to.answers && to.turn &&
		s.appTURNCountLocked(from) < maxAppTURNPerHour && s.appTURNCountLocked(to) < maxAppTURNPerHour
	if appTURN {
		s.appTURN[session] = appTURNSession{ends: [2]*presence{from, to}, created: time.Now()}
	}
	s.mu.Unlock()

	offer := rendezvous.Msg{
		Type: rendezvous.TypeConnectOffer, From: from.id,
		Candidates: fromCands, Session: session, Nonce: nonce, Relay: relay, NAT: fromNAT,
	}
	s.mu.Lock()
	offer.Predict = predictedLocked(from, m.Predict)
	s.mu.Unlock()
	if appTURN {
		if offer.ICE = s.AppTURN.ICE(session); offer.ICE == nil {
			s.endAppTURN(session) // Cloudflare unreachable: the other paths remain
			appTURN = false
		}
	}
	to.send(offer)

	introduce := func(answer rendezvous.Msg) {
		s.mu.Lock()
		reply.Candidates, reply.NAT = s.candidatesLocked(to), to.nat
		reply.Predict = predictedLocked(to, answer.Predict)
		s.mu.Unlock()
		reply.Session, reply.Nonce, reply.Relay = session, nonce, relay
		reply.TURNAddrs = answer.TURNAddrs
		from.send(reply)
		s.Logger.Printf("connect %s -> %s (relay %v, turn %d, predict %v)", from.id, to.id, relay, len(answer.TURNAddrs), reply.Predict != "")
	}
	if w == nil {
		introduce(rendezvous.Msg{})
		return
	}
	// Wait for the answer without holding up the requester's other messages.
	go func() {
		var answer rendezvous.Msg
		select {
		case answer = <-w.answer:
		case <-time.After(answerTimeout):
		}
		s.mu.Lock()
		delete(s.waiting, session)
		if !answer.Accept && relay {
			delete(s.sessions, sessKey)
		}
		s.mu.Unlock()
		if !answer.Accept || len(answer.TURNAddrs) == 0 {
			if appTURN {
				s.endAppTURN(session)
			}
		}
		if !answer.Accept {
			reply.Error = "offline" // same as not being there at all
			from.send(reply)
			return
		}
		introduce(answer)
	}()
}

// predictedLocked is where p's fresh punching socket on port should be
// reachable: that port at the public IP its probes come from. "" when
// there's no port or no recent probe.
func predictedLocked(p *presence, port string) string {
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 || n > 65535 || p.reflexive == "" || time.Since(p.reflexiveAt) > 2*time.Minute {
		return ""
	}
	host, _, err := net.SplitHostPort(p.reflexive)
	if err != nil {
		return ""
	}
	return net.JoinHostPort(host, port)
}

func (s *Server) appTURNCountLocked(p *presence) int {
	n := 0
	for _, a := range s.appTURN {
		if (a.ends[0].id == p.id || a.ends[1].id == p.id) && time.Since(a.created) < time.Hour {
			n++
		}
	}
	return n
}

// endAppTURN forgets an AppTURN session and revokes its credentials.
func (s *Server) endAppTURN(session string) {
	s.mu.Lock()
	delete(s.appTURN, session)
	s.mu.Unlock()
	go s.AppTURN.Revoke(session)
}

// AppTURNLive says whether an AppTURN session's credentials should keep
// working: while both its devices stay online (a transfer through it can't
// outlive them), for turnSessionMax at most. Sessions that ended are
// forgotten.
func (s *Server) AppTURNLive(session string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.appTURN[session]
	if !ok {
		return false
	}
	if s.online[a.ends[0].id] == a.ends[0] && s.online[a.ends[1].id] == a.ends[1] && time.Since(a.created) < turnSessionMax {
		return true
	}
	delete(s.appTURN, session)
	return false
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
