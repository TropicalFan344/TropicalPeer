package server

import (
	"fmt"
	"net"
	"slices"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/pion/turn/v5"

	"TropicalPeer/internal/rendezvous"
)

const turnCredentialLife = turnSessionMax // the session check (Session) ends them sooner

// TURN is the relay browsers and senders fall back to when WebRTC can't
// connect directly. It only ever carries DTLS ciphertext.
type TURN struct {
	srv    *turn.Server
	stop   chan struct{}
	secret string
	host   string // public name or IP used in URLs
	port   int

	publicIP *atomic.Pointer[net.IP] // what new allocations announce; see SetPublicIP
}

// TURNConfig sets up StartTURN.
type TURNConfig struct {
	Port     int    // UDP and TCP listening port, e.g. 3478
	Host     string // public host name or IP put in the URLs browsers get
	PublicIP net.IP // the address relayed packets appear from
	RelayMin int    // UDP port range for relay allocations
	RelayMax int
	// AllowLAN lets the relay send to any private address. Without it, a
	// session's relay reaches private addresses only where its own sender or
	// browser connects from (a sender on the server's own network).
	AllowLAN bool
	// Rate caps each relay allocation in bytes per second, both directions
	// together (0 = unlimited), like the QUIC relay's -relay-rate.
	Rate int64
	// Session says whether a session's credentials still work (the sender
	// admitted it and is still sharing) and which private addresses its
	// relay may reach. Nil: credentials work until they expire, and only
	// AllowLAN opens private addresses.
	Session func(session string) (private []net.IP, ok bool)
}

// Allocation limits: a session is one browser and one sender, each with a
// UDP and maybe a TCP allocation; one address can't take more than a few
// sessions' worth, and the total keeps a busy server's ports and memory in
// check.
const (
	maxAllocationsPerSession = 6
	maxAllocationsPerIP      = 12
	maxAllocations           = 256
)

// StartTURN listens on cfg.Port (UDP and TCP).
func StartTURN(cfg TURNConfig) (*TURN, error) {
	udp, err := net.ListenPacket("udp4", ":"+strconv.Itoa(cfg.Port))
	if err != nil {
		return nil, err
	}
	tcp, err := net.Listen("tcp4", ":"+strconv.Itoa(cfg.Port))
	if err != nil {
		udp.Close()
		return nil, err
	}
	secret := randomHex(32)
	relays := &relaySockets{}
	publicIP := &atomic.Pointer[net.IP]{}
	publicIP.Store(&cfg.PublicIP)
	gen := func() turn.RelayAddressGenerator {
		return &limitedGenerator{relays: relays, publicIP: publicIP, RelayAddressGenerator: &turn.RelayAddressGeneratorPortRange{
			RelayAddress: cfg.PublicIP,
			Address:      "0.0.0.0",
			MinPort:      uint16(cfg.RelayMin),
			MaxPort:      uint16(cfg.RelayMax),
		}, rate: cfg.Rate}
	}
	q := &quota{perSession: map[string]int{}, perIP: map[string]int{}, owners: map[string]string{}}
	// The relay sends only where a peer could be: never to this machine,
	// multicast or reserved ranges, and to private networks only for the
	// session's own two ends (or anywhere with AllowLAN).
	allow := func(client net.Addr, peer net.IP) bool {
		if blockedPeer(peer) {
			return false
		}
		if !peer.IsPrivate() && !isCGNAT(peer) {
			return true
		}
		if cfg.AllowLAN {
			return true
		}
		if cfg.Session == nil {
			return false
		}
		private, ok := cfg.Session(q.owner(client))
		return ok && slices.ContainsFunc(private, peer.Equal)
	}
	rest := turn.LongTermTURNRESTAuthHandler(secret, nil)
	authenticate := func(ra *turn.RequestAttributes) (string, []byte, bool) {
		user, key, ok := rest(ra)
		if ok && cfg.Session != nil {
			_, ok = cfg.Session(user)
		}
		return user, key, ok
	}
	srv, err := turn.NewServer(turn.ServerConfig{
		Realm:        "tropicalpeer",
		AuthHandler:  authenticate,
		QuotaHandler: q.allow,
		EventHandler: turn.EventHandler{
			OnAllocationCreated: func(src, _ net.Addr, _, userID, _ string, _ net.Addr, _ int) { q.add(src, userID, 1) },
			OnAllocationDeleted: func(src, _ net.Addr, _, userID, _ string) { q.add(src, userID, -1) },
		},
		PacketConnConfigs:  []turn.PacketConnConfig{{PacketConn: udp, RelayAddressGenerator: gen(), PermissionHandler: allow}},
		ListenerConfigs:    []turn.ListenerConfig{{Listener: tcp, RelayAddressGenerator: gen(), PermissionHandler: allow}},
		AllocationLifetime: 10 * time.Minute,
	})
	if err != nil {
		udp.Close()
		tcp.Close()
		return nil, fmt.Errorf("turn: %w", err)
	}
	t := &TURN{srv: srv, stop: make(chan struct{}), secret: secret, host: cfg.Host, port: cfg.Port, publicIP: publicIP}
	if cfg.Session != nil {
		go freeEndedRelays(relays, cfg.Session, t.stop)
	}
	return t, nil
}

// SetPublicIP changes the address relayed packets appear from, for
// allocations made from now on: a home connection's address changes now
// and then, and allocations announcing the old one can't be reached.
func (t *TURN) SetPublicIP(ip net.IP) { t.publicIP.Store(&ip) }

// PublicIP is the address new allocations announce.
func (t *TURN) PublicIP() net.IP { return *t.publicIP.Load() }

// ICE returns the STUN and TURN servers for one session, with credentials
// that work only for it and expire.
func (t *TURN) ICE(session string) []rendezvous.ICEServer {
	user, pass, _ := turn.GenerateLongTermTURNRESTCredentials(t.secret, session, turnCredentialLife)
	hp := net.JoinHostPort(t.host, strconv.Itoa(t.port))
	return []rendezvous.ICEServer{
		{URLs: []string{"stun:" + hp}},
		{URLs: []string{"turn:" + hp + "?transport=udp", "turn:" + hp + "?transport=tcp"}, Username: user, Credential: pass},
	}
}

func (t *TURN) Close() error {
	close(t.stop)
	return t.srv.Close()
}

// freeEndedRelays closes the relay sockets of sessions that ended (the
// sender dropped them, or their link is gone), every relaySweep.
func freeEndedRelays(relays *relaySockets, session func(string) ([]net.IP, bool), stop <-chan struct{}) {
	t := time.NewTicker(relaySweep)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			relays.closeEnded(func(s string) bool {
				_, ok := session(s)
				return ok
			})
		}
	}
}

// relaySweep is how often ended sessions' relay sockets are closed (a
// variable for tests).
var relaySweep = 15 * time.Second
