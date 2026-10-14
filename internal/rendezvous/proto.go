// Package rendezvous defines the wire protocol between TropicalPeer devices and
// the rendezvous server, plus the client side of it.
//
// The protocol is newline-delimited JSON over TCP. The server only ever sees:
//   - presence (device ID, public key, reachable addresses), and
//   - opaque pairing traffic, which it pipes between two devices that share a
//     nameplate. It cannot read or forge it: the pairing code's secret part
//     never leaves the devices (see package pairing).
package rendezvous

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"TropicalPeer/internal/wire"
)

const DefaultAddr = "localhost:7777"

const (
	TypeRegister      = "register"
	TypeRegistered    = "registered"
	TypePairCreate    = "pair_create"
	TypePairCreated   = "pair_created"
	TypePairJoin      = "pair_join"
	TypePairJoined    = "pair_joined"
	TypePairPeerReady = "pair_peer_ready"

	// Over the presence connection, after "registered":
	TypeConnect      = "connect"       // device → server: set up a path to device To
	TypeConnectInfo  = "connect_info"  // server → requester: the target's candidates
	TypeConnectOffer = "connect_offer" // server → target: someone wants to connect
	// target → server: whether it takes the connection (only paired devices
	// get a yes). Until it says yes, the requester learns nothing about it.
	TypeConnectAnswer = "connect_answer"
	TypeNATInfo       = "nat_info" // device → server: what its router is like
	TypeError         = "error"

	// Share by link, on a connection of its own (see links.go):
	TypeLinkHost    = "link_host"    // sender → server: route browsers that open Link to us
	TypeLinkHosted  = "link_hosted"  // server → sender: done; WebURL is where links point
	TypeLinkVisitor = "link_visitor" // server → sender: a browser opened the link (Session, ICE)
	TypeLinkSignal  = "link_signal"  // both ways: WebRTC signaling for Session (Data)
	TypeLinkDrop    = "link_drop"    // both ways: Session is over
)

// Msg is the envelope for every message. Only the fields relevant to Type are set.
type Msg struct {
	Type string `json:"type"`

	// register
	ID        string            `json:"id,omitempty"`
	PublicKey ed25519.PublicKey `json:"publicKey,omitempty"`
	Addrs     []string          `json:"addrs,omitempty"`
	Port      int               `json:"port,omitempty"`
	Time      int64             `json:"time,omitempty"`
	Sig       []byte            `json:"sig,omitempty"`
	Caps      []string          `json:"caps,omitempty"` // what this client supports (CapAnswer)

	// registered: the address the server saw us connect from, and the
	// server's second UDP port for router detection (0 = none)
	Observed string `json:"observed,omitempty"`
	AltPort  int    `json:"altPort,omitempty"`

	// nat_info: our router; connect_info/connect_offer: the other device's
	NAT *NATProfile `json:"nat,omitempty"`

	// connect_answer
	Accept bool `json:"accept,omitempty"`

	// pair_*
	Nameplate string `json:"nameplate,omitempty"`

	// connect*
	Req        string   `json:"req,omitempty"` // matches a connect to its connect_info
	To         string   `json:"to,omitempty"`
	From       string   `json:"from,omitempty"`
	Candidates []string `json:"candidates,omitempty"` // addresses to punch and dial
	Session    string   `json:"session,omitempty"`    // relay session ID (hex)
	Nonce      string   `json:"nonce,omitempty"`
	Relay      bool     `json:"relay,omitempty"`   // the server will relay this session
	NoRelay    bool     `json:"noRelay,omitempty"` // connect: direct paths only (no relay session)

	// link_*
	Link   string          `json:"link,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"` // opaque to the server
	ICE    []ICEServer     `json:"ice,omitempty"`
	WebURL string          `json:"webURL,omitempty"`

	Error string `json:"error,omitempty"`
}

// ICEServer is a STUN or TURN server for WebRTC, in the shape browsers take
// for RTCIceServer.
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// CapAnswer: the device answers connect offers (connect_answer), so the
// server shares its addresses only with devices it accepts.
const CapAnswer = "answer"

// RegisterPayload is the byte string a device signs to prove it owns its ID.
func RegisterPayload(id string, unixTime int64, port int, addrs []string) []byte {
	return []byte("tropicalpeer-register\n" + id + "\n" +
		strconv.FormatInt(unixTime, 10) + "\n" + strconv.Itoa(port) + "\n" +
		strings.Join(addrs, ","))
}

// Conn is a wire.Conn that also understands server error messages.
type Conn struct{ *wire.Conn }

func NewConn(rw io.ReadWriter) *Conn { return &Conn{wire.NewConn(rw)} }

// RecvMsg reads an envelope and turns a server "error" message into a Go error.
func (c *Conn) RecvMsg() (Msg, error) {
	var m Msg
	if err := c.Recv(&m); err != nil {
		return m, err
	}
	if m.Type == TypeError {
		return m, fmt.Errorf("server: %s", m.Error)
	}
	return m, nil
}

// NATProfile describes how a device's router treats UDP, as measured with
// the server's two probe ports (see node/natdetect.go).
type NATProfile struct {
	Known bool `json:"known"`
	// Symmetric: the router gives each destination its own public port, so
	// a peer can't learn the port to aim at (hole punching toward it fails).
	Symmetric bool `json:"symmetric"`
	// Lenient: the router lets in packets from a port it hasn't sent to
	// (full-cone or address-restricted filtering).
	Lenient bool `json:"lenient"`
	// Mapped: the router forwards our port (UPnP / NAT-PMP), so anyone can
	// reach us at the mapped address regardless of the rest.
	Mapped bool `json:"mapped"`
}

// DirectHopeless predicts, from both routers, whether a direct connection
// from a sender to a receiver can't work, so the sender should go to the
// relay right away instead of giving direct paths a head start.
//
// A receiver behind a symmetric router can't be aimed at. A sender behind
// one arrives from a port the receiver never saw, which only a lenient
// receiver lets in. A mapped port is reachable whatever the router.
func DirectHopeless(sender, receiver *NATProfile) bool {
	if receiver == nil || !receiver.Known || receiver.Mapped {
		return false
	}
	if receiver.Symmetric {
		return true
	}
	return sender != nil && sender.Known && sender.Symmetric && !sender.Mapped && !receiver.Lenient
}
