package rendezvous

import (
	"context"
	"fmt"
	"net"
	"time"

	"TropicalPeer/internal/identity"
)

// Client talks to a rendezvous server.
type Client struct {
	Addr string
	ID   *identity.Identity
}

func (c *Client) dial(ctx context.Context) (net.Conn, *Conn, error) {
	var d net.Dialer
	nc, err := d.DialContext(ctx, "tcp", c.Addr)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to rendezvous server %s: %w", c.Addr, err)
	}
	return nc, NewConn(nc), nil
}

// Lookup asks the server where a peer can currently be reached.
func (c *Client) Lookup(ctx context.Context, peerID string) (online bool, addrs []string, err error) {
	nc, conn, err := c.dial(ctx)
	if err != nil {
		return false, nil, err
	}
	defer nc.Close()
	stopDeadline := context.AfterFunc(ctx, func() { nc.SetDeadline(time.Now()) })
	defer stopDeadline()

	if err := conn.Send(Msg{Type: TypeLookup, ID: peerID}); err != nil {
		return false, nil, err
	}
	m, err := conn.RecvMsg()
	if err != nil {
		return false, nil, err
	}
	return m.Online, m.Addrs, nil
}

// PairSession is an open pairing channel that the server pipes to the other device.
type PairSession struct {
	Nameplate string
	*Conn
	nc net.Conn
}

func (p *PairSession) Close() error { return p.nc.Close() }

// CreatePair reserves a nameplate. Call WaitForPeer before exchanging messages.
func (c *Client) CreatePair(ctx context.Context) (*PairSession, error) {
	nc, conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	if err := conn.Send(Msg{Type: TypePairCreate}); err != nil {
		nc.Close()
		return nil, err
	}
	m, err := conn.RecvMsg()
	if err != nil {
		nc.Close()
		return nil, err
	}
	return &PairSession{Nameplate: m.Nameplate, Conn: conn, nc: nc}, nil
}

// WaitForPeer blocks until the other device joins the nameplate.
func (p *PairSession) WaitForPeer(ctx context.Context) error {
	stop := context.AfterFunc(ctx, func() { p.nc.SetDeadline(time.Now()) })
	defer stop()
	m, err := p.RecvMsg()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	if m.Type != TypePairPeerReady {
		return fmt.Errorf("unexpected message %q while waiting for peer", m.Type)
	}
	return nil
}

// JoinPair connects to a nameplate created by another device.
func (c *Client) JoinPair(ctx context.Context, nameplate string) (*PairSession, error) {
	nc, conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	if err := conn.Send(Msg{Type: TypePairJoin, Nameplate: nameplate}); err != nil {
		nc.Close()
		return nil, err
	}
	if _, err := conn.RecvMsg(); err != nil {
		nc.Close()
		return nil, err
	}
	return &PairSession{Nameplate: nameplate, Conn: conn, nc: nc}, nil
}

// SetDeadline bounds the remaining pairing exchange.
func (p *PairSession) SetDeadline(t time.Time) error { return p.nc.SetDeadline(t) }
