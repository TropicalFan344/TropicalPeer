package node

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/pion/logging"
	"github.com/pion/turn/v5"
	"github.com/quic-go/quic-go"

	"TropicalPeer/internal/rendezvous"
)

// TURN paths. When the server offers a TURN relay with an introduction
// (Cloudflare's), the receiving device reserves an address there before it
// answers, and the sender dials that address like a relay. Unlike hole
// punching this works between any two routers: the relay checks only the
// sender's IP, not its port, so symmetric routers on either side don't
// matter. QUIC runs over it unchanged, end-to-end encrypted with the same
// key pinning; the relay only ever sees ciphertext.

const (
	turnAllocTimeout = 3 * time.Second
	// turnUnused is how long a reserved address waits for the sender's
	// connection before it is given back.
	turnUnused = dialTimeout + 5*time.Second
)

// openTURN reserves an address on the TURN relay in servers, lets the
// sender's public addresses (from its candidates) reach it, and accepts the
// sender's connection through it. It returns the reserved address, or ""
// when there is none.
func (n *Node) openTURN(ctx context.Context, servers []rendezvous.ICEServer, senderCands []string) string {
	server, user, pass := pickTURN(servers)
	if server == "" {
		return ""
	}
	var perms []net.Addr
	for _, c := range senderCands {
		host, _, err := net.SplitHostPort(c)
		if err != nil {
			continue
		}
		// Only public addresses: the relay is on the internet.
		if ip := net.ParseIP(host).To4(); ip != nil && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !isCGNAT(ip) {
			perms = append(perms, &net.UDPAddr{IP: ip})
		}
	}
	if len(perms) == 0 {
		return ""
	}

	sock, err := n.listenPacket(0)
	if err != nil {
		return ""
	}
	lf := logging.NewDefaultLoggerFactory()
	lf.DefaultLogLevel = logging.LogLevelError
	client, err := turn.NewClient(&turn.ClientConfig{
		STUNServerAddr: server, TURNServerAddr: server,
		Conn: sock, Username: user, Password: pass, LoggerFactory: lf,
	})
	if err != nil {
		sock.Close()
		return ""
	}
	closeAll := func() {
		client.Close()
		sock.Close()
	}
	if err := client.Listen(); err != nil {
		closeAll()
		return ""
	}
	actx, cancel := context.WithTimeout(ctx, turnAllocTimeout)
	relayed, err := client.AllocateWithContext(actx)
	cancel()
	if err != nil {
		closeAll()
		return ""
	}
	if err := client.CreatePermission(perms...); err != nil {
		relayed.Close()
		closeAll()
		return ""
	}
	tr := &quic.Transport{Conn: turnConn{relayed}}
	ln, err := tr.Listen(n.serverTLS, quicConfig)
	if err != nil {
		tr.Close()
		relayed.Close()
		closeAll()
		return ""
	}
	go func() {
		defer func() {
			ln.Close()
			tr.Close()
			relayed.Close() // gives the address back
			closeAll()
		}()
		actx, cancel := context.WithTimeout(ctx, turnUnused)
		conn, err := ln.Accept(actx)
		cancel()
		if err != nil {
			return
		}
		n.handleIncoming(ctx, conn, true)
	}()
	return relayed.LocalAddr().String()
}

// turnConn is a relayed address for QUIC. Buffer sizes belong to the TURN
// client's own socket, so asking for them succeeds without changing
// anything (instead of quic-go warning that it can't).
type turnConn struct{ net.PacketConn }

func (turnConn) SetReadBuffer(int) error  { return nil }
func (turnConn) SetWriteBuffer(int) error { return nil }

// pickTURN finds a UDP TURN server with credentials in servers: its
// host:port, username and password.
func pickTURN(servers []rendezvous.ICEServer) (addr, user, pass string) {
	for _, s := range servers {
		if s.Username == "" {
			continue
		}
		for _, u := range s.URLs {
			rest, ok := strings.CutPrefix(u, "turn:")
			if !ok {
				continue // stun:, or turns: (TLS)
			}
			hostPort, query, _ := strings.Cut(rest, "?")
			if query != "" && query != "transport=udp" {
				continue
			}
			if _, _, err := net.SplitHostPort(hostPort); err != nil {
				hostPort = net.JoinHostPort(hostPort, "3478")
			}
			return hostPort, s.Username, s.Credential
		}
	}
	return "", "", ""
}

// isCGNAT reports addresses in the carrier-grade NAT range (100.64.0.0/10),
// which are as unreachable from the internet as private ones.
func isCGNAT(ip net.IP) bool {
	ip4 := ip.To4()
	return ip4 != nil && ip4[0] == 100 && ip4[1]&0xc0 == 64
}
