package server

import (
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/pion/turn/v5"

	"TropicalPeer/internal/rendezvous"
)

const turnCredentialLife = 24 * time.Hour

// TURN is the relay browsers and senders fall back to when WebRTC can't
// connect directly. It only ever carries DTLS ciphertext.
type TURN struct {
	srv    *turn.Server
	secret string
	host   string // public name or IP used in URLs
	port   int
}

// TURNConfig sets up StartTURN.
type TURNConfig struct {
	Port     int    // UDP and TCP listening port, e.g. 3478
	Host     string // public host name or IP put in the URLs browsers get
	PublicIP net.IP // the address relayed packets appear from
	RelayMin int    // UDP port range for relay allocations
	RelayMax int
	AllowLAN bool // let the relay send to private addresses (a sender on the server's own network)
}

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
	gen := func() turn.RelayAddressGenerator {
		return &turn.RelayAddressGeneratorPortRange{
			RelayAddress: cfg.PublicIP,
			Address:      "0.0.0.0",
			MinPort:      uint16(cfg.RelayMin),
			MaxPort:      uint16(cfg.RelayMax),
		}
	}
	allow := func(_ net.Addr, peer net.IP) bool {
		if peer.IsLoopback() || peer.IsUnspecified() || peer.IsMulticast() || peer.IsLinkLocalUnicast() {
			return false
		}
		return cfg.AllowLAN || !peer.IsPrivate()
	}
	srv, err := turn.NewServer(turn.ServerConfig{
		Realm:              "tropicalpeer",
		AuthHandler:        turn.LongTermTURNRESTAuthHandler(secret, nil),
		PacketConnConfigs:  []turn.PacketConnConfig{{PacketConn: udp, RelayAddressGenerator: gen(), PermissionHandler: allow}},
		ListenerConfigs:    []turn.ListenerConfig{{Listener: tcp, RelayAddressGenerator: gen(), PermissionHandler: allow}},
		AllocationLifetime: 10 * time.Minute,
	})
	if err != nil {
		udp.Close()
		tcp.Close()
		return nil, fmt.Errorf("turn: %w", err)
	}
	return &TURN{srv: srv, secret: secret, host: cfg.Host, port: cfg.Port}, nil
}

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

func (t *TURN) Close() error { return t.srv.Close() }
