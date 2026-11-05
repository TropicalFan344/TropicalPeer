package node

import (
	"context"
	"crypto/tls"
	"math/rand/v2"
	"net"
	"time"

	"github.com/quic-go/quic-go"

	"TropicalPeer/internal/rendezvous"
)

// Port prediction. A symmetric router gives every destination its own public
// port, so the address the server sees is useless to a peer, and two such
// routers can't punch through to each other. But many of them keep a
// socket's own port for its first destination when no other mapping holds
// it. So each side opens a fresh socket on a random port, tells the other
// through the server, and its first packet goes to the other's predicted
// address (that port at the other's public IP): both routers then map
// exactly the announced ports to each other, and the punches meet. Where a
// router doesn't keep ports the path just fails and the relay carries on.
//
// The fresh socket must send nothing before its first packet to the peer,
// or that packet would get the next port instead of the announced one.

const (
	predictPortMin = 20000
	predictPortMax = 60000
	// predictUnused is how long the receiver's socket waits for the sender.
	predictUnused = dialTimeout + 5*time.Second
	// relayDelayPredicted replaces relayDelayHopeless when a predicted path
	// is being tried: it connects in a few round trips, which the relay
	// shouldn't beat just by starting first.
	relayDelayPredicted = time.Second
)

// predicted is a fresh socket for port prediction, with QUIC on it.
type predicted struct {
	tr   *quic.Transport
	port int
}

// openPredicted opens a fresh socket on a random port.
func (n *Node) openPredicted() *predicted {
	for range 5 {
		port := predictPortMin + rand.IntN(predictPortMax-predictPortMin)
		conn, err := n.listenPacket(port)
		if err != nil {
			continue
		}
		if ua, ok := conn.LocalAddr().(*net.UDPAddr); ok && ua.Port != port {
			conn.Close() // the test hook may hand out another port; we must know ours
			continue
		}
		return &predicted{tr: &quic.Transport{Conn: conn}, port: port}
	}
	return nil
}

func (p *predicted) close() { p.tr.Close() }

// punch sends punch packets to addr until ctx ends or punchDuration passes;
// the first one is what opens our router toward the peer.
func (p *predicted) punch(ctx context.Context, addr net.Addr, nonce string) {
	pkt := rendezvous.EncodeControl(rendezvous.KindPunch, rendezvous.UDPControl{Nonce: nonce})
	stop := time.After(punchDuration)
	t := time.NewTicker(punchInterval)
	defer t.Stop()
	for {
		p.tr.WriteTo(pkt, addr)
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-t.C:
		}
	}
}

// wantPredict says whether to try port prediction toward peerID: when
// either router is (or may be) symmetric, which plain punching can't get
// through.
func (n *Node) wantPredict(peerID string) bool {
	mine, _ := n.natProfile.Load().(rendezvous.NATProfile)
	if mine.Mapped {
		return false // a forwarded port is reachable as it is
	}
	peer := n.lastPeerNAT(peerID)
	return !mine.Known || mine.Symmetric || peer == nil || !peer.Known || (peer.Symmetric && !peer.Mapped)
}

// acceptPredicted is the receiving side: it opens a fresh socket, punches
// toward the sender's predicted address and accepts the sender's connection
// on it. It returns the socket's port (0 = none).
func (n *Node) acceptPredicted(ctx context.Context, senderAddr, nonce string) int {
	addr, err := net.ResolveUDPAddr("udp", senderAddr)
	if err != nil {
		return 0
	}
	p := n.openPredicted()
	if p == nil {
		return 0
	}
	ln, err := p.tr.Listen(n.serverTLS, quicConfig)
	if err != nil {
		p.close()
		return 0
	}
	go p.punch(ctx, addr, nonce)
	go func() {
		defer p.close()
		actx, cancel := context.WithTimeout(ctx, predictUnused)
		conn, err := ln.Accept(actx)
		cancel()
		if err != nil {
			return
		}
		n.handleIncoming(ctx, conn, false)
	}()
	return p.port
}

// dialPredicted is the sending side, once the receiver's predicted address
// is known: punch toward it from our fresh socket and dial QUIC there.
func (p *predicted) dial(ctx context.Context, receiverAddr, nonce string, tlsConf *tls.Config) (*quic.Conn, error) {
	addr, err := net.ResolveUDPAddr("udp", receiverAddr)
	if err != nil {
		return nil, err
	}
	go p.punch(ctx, addr, nonce)
	return p.tr.Dial(ctx, addr, tlsConf, quicConfig)
}

// keepUntilClosed closes the fresh socket once conn (which runs on it) ends.
func (p *predicted) keepUntilClosed(conn *quic.Conn) {
	go func() {
		<-conn.Context().Done()
		p.close()
	}()
}
