package rendezvous

import (
	"crypto/ed25519"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"
)

// UDP control packets share a socket with QUIC. They start with Magic, a
// first byte with both high bits clear, which QUIC never produces, so quic-go
// hands them to the application (Transport.ReadNonQUICPacket).
//
//	[Magic][kind][body]
//
// body is JSON for control kinds and raw bytes for KindRelayData:
//
//	[Magic][KindRelayData][8-byte session ID][QUIC packet]
const Magic byte = 0x1F

const (
	KindProbe     byte = 1 // device → server: "what address do you see?" (signed)
	KindProbeAck  byte = 2 // server → device: {addr}
	KindPunch     byte = 3 // device → device: opens the sender's NAT toward the peer
	KindRelayBind byte = 4 // device → server: "this socket is my end of session S" (signed)
	KindBindAck   byte = 5 // server → device: {session}
	KindRelayData byte = 6 // either way: a QUIC packet inside a relay session
	// KindFilterTest is sent by the server from its second port to the address
	// a probe came from, before the device has ever contacted that port. If it
	// arrives, the device's router lets in packets from ports it hasn't sent to.
	KindFilterTest byte = 7
)

// SessionIDLen is the length of a relay session ID on the wire.
const SessionIDLen = 8

// MaxClockSkew bounds how old a signed UDP control packet may be.
const MaxClockSkew = 5 * time.Minute

// UDPControl is the JSON body of control packets. Only the fields relevant
// to the kind are set.
type UDPControl struct {
	ID      string `json:"id,omitempty"`
	Session string `json:"session,omitempty"` // hex
	Time    int64  `json:"time,omitempty"`
	Sig     []byte `json:"sig,omitempty"`
	Addr    string `json:"addr,omitempty"`  // probe ack: address the server saw
	Nonce   string `json:"nonce,omitempty"` // punch
	Via     string `json:"via,omitempty"`   // probe ack: "alt" when from the second port
}

// EncodeControl builds a control packet.
func EncodeControl(kind byte, c UDPControl) []byte {
	body, _ := json.Marshal(c)
	return append([]byte{Magic, kind}, body...)
}

// Decode splits a packet into its kind and body. ok is false for anything
// that isn't a TropicalPeer control packet.
func Decode(p []byte) (kind byte, body []byte, ok bool) {
	if len(p) < 2 || p[0] != Magic {
		return 0, nil, false
	}
	return p[1], p[2:], true
}

// DecodeControl parses a control body.
func DecodeControl(body []byte) (UDPControl, error) {
	var c UDPControl
	err := json.Unmarshal(body, &c)
	return c, err
}

// EncodeRelayData wraps a QUIC packet for a relay session.
func EncodeRelayData(session [SessionIDLen]byte, quicPacket []byte) []byte {
	out := make([]byte, 0, 2+SessionIDLen+len(quicPacket))
	out = append(out, Magic, KindRelayData)
	out = append(out, session[:]...)
	return append(out, quicPacket...)
}

// DecodeRelayData splits a relay data body into session and QUIC packet.
func DecodeRelayData(body []byte) (session [SessionIDLen]byte, payload []byte, err error) {
	if len(body) < SessionIDLen {
		return session, nil, errors.New("short relay packet")
	}
	copy(session[:], body)
	return session, body[SessionIDLen:], nil
}

// ParseSession turns the hex form used in JSON into a session ID.
func ParseSession(s string) (id [SessionIDLen]byte, err error) {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != SessionIDLen {
		return id, errors.New("bad session ID")
	}
	copy(id[:], b)
	return id, nil
}

// SessionKey returns a session ID as a map key.
func SessionKey(id [SessionIDLen]byte) uint64 { return binary.BigEndian.Uint64(id[:]) }

func controlPayload(kind byte, id, session string, t int64) []byte {
	return []byte("tropicalpeer-udp\n" + strconv.Itoa(int(kind)) + "\n" + id + "\n" + session + "\n" + strconv.FormatInt(t, 10))
}

// SignControl fills Time and Sig for a probe or relay bind.
func SignControl(priv ed25519.PrivateKey, kind byte, c UDPControl) UDPControl {
	c.Time = time.Now().Unix()
	c.Sig = ed25519.Sign(priv, controlPayload(kind, c.ID, c.Session, c.Time))
	return c
}

// VerifyControl checks a signed probe or relay bind against the device's key.
func VerifyControl(pub ed25519.PublicKey, kind byte, c UDPControl) bool {
	if d := time.Since(time.Unix(c.Time, 0)); d > MaxClockSkew || d < -MaxClockSkew {
		return false
	}
	return ed25519.Verify(pub, controlPayload(kind, c.ID, c.Session, c.Time), c.Sig)
}
