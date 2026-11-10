package rendezvous

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"
)

// ErrPeerOffline is returned by Presence.Connect when the target isn't online.
var ErrPeerOffline = errors.New("peer is offline")

// Presence is this device's registration with the server. While it lasts the
// device counts as online. It is also a two-way channel: Connect asks the
// server to introduce us to a peer, and the server pushes connect offers from
// peers that want to reach us.
type Presence struct {
	nc   net.Conn
	conn *Conn

	// AltPort is the server's second UDP port for router detection (0 if
	// the server has none). Relay says whether the server relays.
	AltPort int
	Relay   bool
	// LinksURL is where the server serves share-by-link pages ("" when it
	// doesn't support links).
	LinksURL string
	// Watches: the server reports paired devices' presence (CapPresence).
	Watches bool

	wmu sync.Mutex // serializes writes

	mu         sync.Mutex
	waiters    map[string]chan Msg
	onPresence func(id string, online bool)
	err        error
	done       chan struct{}
}

// StartPresence registers this device. port is the local QUIC port;
// lanAddrs are extra addresses (host:port) peers on the same network can try.
// onOffer runs (on its own goroutine) for every connect offer pushed to us;
// it should Answer it on the Presence it came from.
func (c *Client) StartPresence(ctx context.Context, port int, lanAddrs []string, onOffer func(*Presence, Msg)) (*Presence, error) {
	nc, conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	id := c.ID.ID()
	err = conn.Send(Msg{
		Type:      TypeRegister,
		ID:        id,
		PublicKey: c.ID.PublicKey,
		Addrs:     lanAddrs,
		Port:      port,
		Time:      now,
		Sig:       ed25519.Sign(c.ID.PrivateKey, RegisterPayload(id, now, port, lanAddrs)),
		Caps:      []string{CapAnswer, CapTURN},
	})
	if err != nil {
		nc.Close()
		return nil, err
	}
	reg, err := conn.RecvMsg()
	if err != nil {
		nc.Close()
		return nil, err
	}

	p := &Presence{nc: nc, conn: conn, AltPort: reg.AltPort, Relay: reg.Relay, waiters: map[string]chan Msg{}, done: make(chan struct{})}
	for _, c := range reg.Caps {
		switch c {
		case CapLinks:
			p.LinksURL = reg.WebURL
		case CapPresence:
			p.Watches = true
		}
	}
	go func() {
		select {
		case <-ctx.Done():
			nc.Close()
		case <-p.done:
		}
	}()
	go p.readLoop(onOffer)
	return p, nil
}

func (p *Presence) readLoop(onOffer func(*Presence, Msg)) {
	for {
		var m Msg
		if err := p.conn.Recv(&m); err != nil {
			p.mu.Lock()
			p.err = err
			for _, w := range p.waiters {
				close(w)
			}
			p.waiters = nil
			p.mu.Unlock()
			close(p.done)
			return
		}
		switch m.Type {
		case TypeConnectInfo:
			p.mu.Lock()
			w := p.waiters[m.Req]
			delete(p.waiters, m.Req)
			p.mu.Unlock()
			if w != nil {
				w <- m
			}
		case TypeConnectOffer:
			if onOffer != nil {
				go onOffer(p, m)
			}
		case TypePresence:
			p.mu.Lock()
			fn := p.onPresence
			p.mu.Unlock()
			if fn != nil && m.ID != "" {
				fn(m.ID, m.Online) // in order, on this goroutine
			}
		}
	}
}

// Answer tells the server whether we take a connect offer. Only after a yes
// does the device asking learn where we are.
func (p *Presence) Answer(offer Msg, accept bool) error {
	return p.send(Msg{Type: TypeConnectAnswer, Session: offer.Session, Accept: accept})
}

// Accept takes a connect offer and says where else we can be reached: the
// addresses we reserved on the TURN relay the offer came with, and the port
// of the fresh socket we punch from (0 = none).
func (p *Presence) Accept(offer Msg, turnAddrs []string, predictPort int) error {
	m := Msg{Type: TypeConnectAnswer, Session: offer.Session, Accept: true, TURNAddrs: turnAddrs}
	if predictPort > 0 {
		m.Predict = strconv.Itoa(predictPort)
	}
	return p.send(m)
}

func (p *Presence) send(m Msg) error {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	return p.conn.Send(m)
}

// SendNAT tells the server what this device's router is like, so it can
// pass that on when introducing us.
func (p *Presence) SendNAT(profile NATProfile) error {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	return p.conn.Send(Msg{Type: TypeNATInfo, NAT: &profile})
}

// Watch tells the server which devices we are paired with (the whole list,
// replacing the last one), and has fn hear whenever one of them comes online
// or goes offline. The server answers for each listed device right away.
// Only works when Watches is true; fn must not block.
func (p *Presence) Watch(ids []string, fn func(id string, online bool)) error {
	if len(ids) > MaxWatch {
		ids = ids[:MaxWatch]
	}
	p.mu.Lock()
	p.onPresence = fn
	p.mu.Unlock()
	return p.send(Msg{Type: TypeWatch, Watch: ids})
}

// Done is closed when the registration ends; Err then says why.
func (p *Presence) Done() <-chan struct{} { return p.done }

func (p *Presence) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// Close ends the registration.
func (p *Presence) Close() error { return p.nc.Close() }

// Connect asks the server to introduce us to device to. The reply carries the
// target's candidate addresses and, when the server relays (and noRelay is
// false), a relay session ID. The server tells the target about us at the
// same moment.
func (p *Presence) Connect(ctx context.Context, to string, noRelay bool) (Msg, error) {
	return p.ConnectPredict(ctx, to, noRelay, 0)
}

// ConnectPredict is Connect that also tells the target the port of a fresh
// socket we'll punch from (see node/predict.go); 0 = none.
func (p *Presence) ConnectPredict(ctx context.Context, to string, noRelay bool, predictPort int) (Msg, error) {
	b := make([]byte, 8)
	rand.Read(b)
	req := hex.EncodeToString(b)

	w := make(chan Msg, 1)
	p.mu.Lock()
	if p.waiters == nil {
		p.mu.Unlock()
		return Msg{}, errors.New("not connected to the rendezvous server")
	}
	p.waiters[req] = w
	p.mu.Unlock()

	p.wmu.Lock()
	m := Msg{Type: TypeConnect, Req: req, To: to, NoRelay: noRelay}
	if predictPort > 0 {
		m.Predict = strconv.Itoa(predictPort)
	}
	err := p.conn.Send(m)
	p.wmu.Unlock()
	if err != nil {
		return Msg{}, err
	}

	select {
	case m, ok := <-w:
		if !ok {
			return Msg{}, errors.New("lost the connection to the rendezvous server")
		}
		if m.Error == "offline" {
			return m, ErrPeerOffline
		}
		if m.Error != "" {
			return m, errors.New("server: " + m.Error)
		}
		return m, nil
	case <-ctx.Done():
		p.mu.Lock()
		if p.waiters != nil {
			delete(p.waiters, req)
		}
		p.mu.Unlock()
		return Msg{}, ctx.Err()
	}
}
