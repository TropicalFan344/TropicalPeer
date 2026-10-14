// Package node runs one TropicalPeer device: it stays registered with the
// rendezvous server, accepts transfers from paired peers according to their
// accept policy, and sends files to them. The GUI and the CLI are both thin
// layers over Node.
package node

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"

	"TropicalPeer/internal/identity"
	"TropicalPeer/internal/pairing"
	"TropicalPeer/internal/peers"
	"TropicalPeer/internal/portmap"
	"TropicalPeer/internal/protocol"
	"TropicalPeer/internal/rendezvous"
	"TropicalPeer/internal/transfer"
)

// Events lets the UI react to what the node is doing. All fields are optional.
// Callbacks may be invoked from multiple goroutines.
type Events struct {
	// OnServerStatus reports whether we are registered with the rendezvous server.
	OnServerStatus func(online bool, err error)
	// OnOffer is asked about offers from peers whose policy is "ask" (or that
	// exceed their auto-accept size limit). Without it such offers are declined.
	OnOffer func(ctx context.Context, peer peers.Peer, offer protocol.Offer) (accept bool)
	// OnAccepted reports an incoming transfer that is about to start, whether
	// the user accepted it or the peer's policy did. cancel aborts it; pause
	// stops it keeping what arrived (the sender is told it was paused).
	// relayed says the connection runs through the rendezvous server's relay.
	// OnPathChanged reports a running incoming transfer moving to another
	// connection (relay → direct); transferID is the sender's ID, as in the
	// offer.
	OnPathChanged func(peer peers.Peer, transferID string, relayed bool)
	OnAccepted    func(peer peers.Peer, offer protocol.Offer, saveDir string, relayed bool, cancel, pause func())
	// OnResumeRequest is asked when a peer we sent to wants a paused or
	// interrupted transfer continued; transferID is ours. ok means it is
	// being sent again; reason explains a refusal.
	OnResumeRequest func(peer peers.Peer, transferID string) (ok bool, reason string)
	// OnCancelNotice reports that the peer cancelled a transfer between us
	// while nothing connected us (it was paused or interrupted).
	// transferID is the sender's ID for it.
	OnCancelNotice func(peer peers.Peer, transferID string)
	// OnRemovedUs reports that a peer removed this device (it told us, or
	// refused our certificate): sending to it fails until we pair again.
	OnRemovedUs func(peer peers.Peer)
	// OnProgress reports progress of incoming transfers. (Outgoing transfers
	// report through SendOptions.)
	OnProgress func(peer peers.Peer, p transfer.Progress)
	// OnNetwork reports changes to how reachable this device is (router
	// type, port forwarding, public address).
	OnNetwork func(NetworkInfo)
	// OnReceived reports the end of an incoming transfer (err is nil on
	// success). err is context.Canceled when cancelled through OnAccepted's
	// cancel, and satisfies transfer.IsCancelledByPeer when the sender gave up.
	OnReceived func(peer peers.Peer, offer protocol.Offer, files []transfer.Result, err error)
}

type Node struct {
	dir      string
	Identity *identity.Identity
	Peers    *peers.Store
	events   Events

	mu       sync.RWMutex
	settings Settings

	tellMu sync.Mutex
	told   map[string]time.Time // last removal notice tried, by peer ID

	// ListenPacket, when set, replaces real UDP sockets (port 0 = any port).
	// ServerUDPAddr, when set, replaces resolving the server's UDP address
	// from Settings.ServerAddr. Both exist for the NAT simulator in tests.
	ListenPacket  func(port int) (net.PacketConn, error)
	ServerUDPAddr func() (net.Addr, error)

	ready          chan struct{} // closed once Run has opened the port
	transport      *quic.Transport
	port           int
	relay          *relayConn
	relayTransport *quic.Transport

	presence   atomic.Value // *rendezvous.Presence while registered
	linksURL   atomic.Value // string: the server's link page address, as last registered
	publicAddr atomic.Value // string: our UDP address as the server sees it

	// Router detection (natdetect.go).
	mainAck      atomic.Value // string: address seen by the server's main port
	altAck       atomic.Value // string: address seen by its second port
	filterSeen   atomic.Bool  // the server's filter test got through
	natProfile   atomic.Value // rendezvous.NATProfile
	detectMu     sync.Mutex
	detected     *rendezvous.NATProfile
	altContacted atomic.Bool // we've probed the second port (filter test no longer meaningful)
	lenientSeen  bool        // the filter test's answer, under detectMu
	serverRTT    atomic.Int64
	probeSentAt  atomic.Int64 // time.Since(clockBase) at the last main-port probe
	checking     atomic.Bool

	// Router port forwarding (under mu).
	mapping       *portmap.Mapping
	mappingErr    string
	portMapCancel context.CancelFunc

	partials    *partials // what interrupted incoming transfers left behind
	recvMu      sync.Mutex
	receiving   map[string]*recvSession           // incoming transfers by recvKey
	peerNAT     map[string]*rendezvous.NATProfile // last seen per peer
	portMapDone chan struct{}

	runCtx         context.Context    // set while Run is active
	presenceCancel context.CancelFunc // stops the current presence loop
}

// Connections only live while a transfer (or its accept prompt) does, so
// keep-alives are cheap, and a path that silently died (a phone dropping off
// Wi-Fi) is noticed quickly: the sender then reconnects and resumes.
var quicConfig = &quic.Config{
	MaxIdleTimeout:       20 * time.Second,
	KeepAlivePeriod:      5 * time.Second,
	HandshakeIdleTimeout: 10 * time.Second,
}

// New loads (or creates) the device state stored in dir.
func New(dir string, events Events) (*Node, error) {
	id, err := identity.LoadOrCreate(dir)
	if err != nil {
		return nil, fmt.Errorf("load identity: %w", err)
	}
	store, err := peers.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("load peers: %w", err)
	}
	settings, err := loadSettings(dir)
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	return &Node{dir: dir, Identity: id, Peers: store, events: events, settings: settings, ready: make(chan struct{}), partials: loadPartials(dir)}, nil
}

func (n *Node) Settings() Settings {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.settings
}

// UpdateSettings persists new settings. A new server address takes effect
// immediately; a new UDP port applies the next time Run starts.
func (n *Node) UpdateSettings(s Settings) error {
	n.mu.Lock()
	if err := saveSettings(n.dir, s); err != nil {
		n.mu.Unlock()
		return err
	}
	serverChanged := s.ServerAddr != n.settings.ServerAddr
	mappingChanged := s.DisablePortMapping != n.settings.DisablePortMapping
	n.settings = s
	n.mu.Unlock()

	if serverChanged {
		n.detectMu.Lock()
		n.detected = nil // a different server may see us differently
		n.detectMu.Unlock()
		n.restartPresence()
	}
	if mappingChanged {
		n.restartPortMapping()
	}
	return nil
}

// SetName changes the name other devices see when pairing.
func (n *Node) SetName(name string) error {
	n.Identity.Name = name
	return n.Identity.Save(n.dir)
}

func (n *Node) rendezvous() *rendezvous.Client {
	return &rendezvous.Client{Addr: n.Settings().ServerAddr, ID: n.Identity}
}

// Run opens the UDP port, registers with the rendezvous server and accepts
// incoming transfers until ctx is cancelled.
func (n *Node) Run(ctx context.Context) error {
	release, err := lockProfile(n.dir)
	if err != nil {
		return err
	}
	defer release()
	defer n.partials.flush()
	udp, err := n.listenPacket(n.Settings().Port)
	if err != nil && n.Settings().Port != 0 {
		// The preferred port is taken (e.g. a second copy of the app); a random
		// port still works for peers that find us through the rendezvous server.
		udp, err = n.listenPacket(0)
	}
	if err != nil {
		return fmt.Errorf("open UDP port: %w", err)
	}
	if ua, ok := udp.LocalAddr().(*net.UDPAddr); ok {
		n.port = ua.Port
	}
	n.transport = &quic.Transport{Conn: udp}
	defer n.transport.Close()

	// A second socket carries relayed traffic (see relay.go).
	relaySock, err := n.listenPacket(0)
	if err != nil {
		return fmt.Errorf("open relay socket: %w", err)
	}
	n.relay = newRelayConn(relaySock, n.serverUDP, n.Identity)
	n.relayTransport = &quic.Transport{Conn: n.relay}
	defer n.relayTransport.Close()

	tlsConf, err := n.Identity.ServerTLS(func(pub ed25519.PublicKey) bool {
		_, ok := n.Peers.ByKey(pub)
		return ok
	})
	if err != nil {
		return err
	}
	ln, err := n.transport.Listen(tlsConf, quicConfig)
	if err != nil {
		return err
	}
	defer ln.Close()
	relayLn, err := n.relayTransport.Listen(tlsConf, quicConfig)
	if err != nil {
		return err
	}
	defer relayLn.Close()
	// Before the sockets close: tell senders we're stopping, so they can
	// come back and resume instead of waiting for silence to time out.
	defer n.stopReceiving()
	go n.controlLoop(ctx)
	close(n.ready)

	n.mu.Lock()
	n.runCtx = ctx
	n.mu.Unlock()
	n.restartPresence()
	n.restartPortMapping()
	go func() {
		// Removal notices that didn't get through last time.
		if _, err := n.waitPresence(ctx); err == nil {
			n.tellAllRemoved()
		}
	}()

	go n.acceptLoop(ctx, relayLn, true)
	return n.acceptLoop(ctx, ln, false)
}

func (n *Node) acceptLoop(ctx context.Context, ln *quic.Listener, relayed bool) error {
	for {
		conn, err := ln.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go n.handleIncoming(ctx, conn, relayed)
	}
}

// Ready is closed once Run has opened its port and can send and receive.
func (n *Node) Ready() <-chan struct{} { return n.ready }

// Port is the UDP port in use once Ready is closed.
func (n *Node) Port() int { return n.port }

// restartPresence (re)connects to the rendezvous server in the background,
// dropping any existing connection. It does nothing unless Run is active.
func (n *Node) restartPresence() {
	n.mu.Lock()
	if n.presenceCancel != nil {
		n.presenceCancel()
	}
	if n.runCtx == nil {
		n.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(n.runCtx)
	n.presenceCancel = cancel
	n.mu.Unlock()

	n.emitStatus(false, nil) // "connecting"
	go n.presenceLoop(ctx)
}

func (n *Node) presenceLoop(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		p, err := n.rendezvous().StartPresence(ctx, n.port, n.registrationAddrs(), n.onConnectOffer)
		if err == nil {
			backoff = time.Second
			n.presence.Store(p)
			n.linksURL.Store(p.LinksURL)
			go n.probeLoop(p)
			go n.reportNetwork(ctx, p)
			if ctx.Err() == nil {
				n.emitStatus(true, nil)
			}
			<-p.Done()
			err = p.Err()
			n.presence.CompareAndSwap(p, (*rendezvous.Presence)(nil))
			n.emitNetwork()
		}
		if ctx.Err() != nil {
			return
		}
		n.emitStatus(false, err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (n *Node) emitStatus(online bool, err error) {
	if n.events.OnServerStatus != nil {
		n.events.OnServerStatus(online, err)
	}
}

func (n *Node) handleIncoming(ctx context.Context, conn *quic.Conn, relayed bool) {
	defer conn.CloseWithError(0, "")
	var raw [][]byte
	for _, c := range conn.ConnectionState().TLS.PeerCertificates {
		raw = append(raw, c.Raw)
	}
	pub, err := identity.PeerKey(raw)
	if err != nil {
		return
	}
	peer, ok := n.Peers.ByKey(pub)
	if !ok {
		return
	}
	ctrl, err := transfer.AcceptControl(ctx, conn)
	if err != nil {
		return
	}
	if ctrl.Hello.Join != "" {
		n.joinIncoming(ctx, peer, conn, ctrl, relayed)
		return
	}
	if ctrl.Hello.Unpaired {
		ctrl.Answer(true, "")
		n.markRemovedUs(peer)
		<-time.After(time.Second) // let the answer go out before hanging up
		return
	}
	if ctrl.Hello.Cancel != "" {
		if n.events.OnCancelNotice != nil {
			n.events.OnCancelNotice(peer, ctrl.Hello.Cancel)
		}
		ctrl.Answer(true, "")
		<-time.After(time.Second) // let the answer go out before hanging up
		return
	}
	if ctrl.Hello.Resume != "" {
		ok, reason := false, "nothing here can resume it"
		if n.events.OnResumeRequest != nil {
			ok, reason = n.events.OnResumeRequest(peer, ctrl.Hello.Resume)
		}
		ctrl.Answer(ok, reason)
		<-time.After(time.Second) // let the answer go out before hanging up
		return
	}

	saveDir := peer.SaveDir
	if saveDir == "" {
		saveDir = n.Settings().DownloadDir
	}

	// The decision ends when the sender hangs up (so a pending "accept?"
	// prompt closes) or the node stops. The transfer itself may outlive this
	// first connection (it can move to a better one), so it ends only when
	// the node stops or the user cancels.
	decideCtx, cancelDecide := context.WithCancel(conn.Context())
	defer cancelDecide()
	stopDecide := context.AfterFunc(ctx, cancelDecide)
	defer stopDecide()
	recvCtx, cancelRecv := context.WithCancelCause(ctx)
	defer cancelRecv(nil)
	stopDecide2 := context.AfterFunc(recvCtx, cancelDecide)
	defer stopDecide2()
	var userCancelled, userPaused atomic.Bool
	userCancel := func() { userCancelled.Store(true); cancelRecv(transfer.ErrCancelled) }
	userPause := func() { userPaused.Store(true); cancelRecv(transfer.ErrPaused) }

	in := transfer.NewIncoming(ctrl)
	in.Parts = n.partials.forPeer(peer.ID)
	key := recvKey(peer.ID, in.Offer.TransferID)
	sess := &recvSession{in: in, ctx: recvCtx, done: make(chan struct{}), finished: make(chan struct{}), stop: cancelRecv}
	// The sender is trying again while we still wait on its old attempt
	// (its connection hasn't timed out here yet): end that one first, so
	// they don't both write the same part file.
	n.supersede(key)
	n.registerRecv(key, sess)
	defer func() {
		n.unregisterRecv(key, sess)
		close(sess.done)
	}()
	resuming := n.partials.accepted(peer.ID, in.Offer)

	decide := func(ctx context.Context, offer protocol.Offer) (bool, string) {
		accept := false
		switch peer.Decide(offer.TotalSize()) {
		case peers.DecisionAccept:
			accept = true
		case peers.DecisionReject:
			return false, "" // don't tell a blocked peer it is blocked
		default:
			if resuming {
				accept = true // accepted the first time; carry on without asking
				break
			}
			if n.events.OnOffer == nil {
				return false, "nobody is available to accept"
			}
			accept = n.events.OnOffer(ctx, peer, offer)
		}
		if accept && n.events.OnAccepted != nil {
			n.events.OnAccepted(peer, offer, saveDir, relayed, userCancel, userPause)
		}
		return accept, ""
	}
	report := func(p transfer.Progress) {
		if n.events.OnProgress != nil {
			n.events.OnProgress(peer, p)
		}
	}

	files, err := in.Receive(recvCtx, decideCtx, saveDir, decide, report)
	if err != nil && !userCancelled.Load() && !userPaused.Load() && conn.Context().Err() != nil && !transfer.IsCancelledByPeer(err) {
		// Our context ended because the first connection closed: report why.
		err = context.Cause(conn.Context())
	}
	if err != nil && userPaused.Load() {
		err = transfer.ErrPaused
	}
	var rejected *transfer.RejectedError
	switch {
	case err == nil:
		n.partials.forget(peer.ID, in.Offer.TransferID)
	case userCancelled.Load() || transfer.IsCancelledByPeer(err):
		n.partials.discard(peer.ID, in.Offer.TransferID)
	case errors.As(err, &rejected):
	default:
		// Interrupted: the parts stay for the sender's next attempt.
	}
	superseded := errors.Is(context.Cause(recvCtx), errSuperseded)
	if n.events.OnReceived != nil && !superseded {
		n.events.OnReceived(peer, in.Offer, files, err)
	}
	close(sess.finished)
	if superseded {
		return
	}
	// Closing now could discard our last message before the sender reads it,
	// so let the sender hang up first.
	select {
	case <-conn.Context().Done():
	case <-ctx.Done():
	case <-time.After(10 * time.Second):
	}
}

// Invite creates a pairing code (passed to onCode) and waits for another
// device to enter it. The new peer starts with the "ask" policy.
func (n *Node) Invite(ctx context.Context, onCode func(code string)) (peers.Peer, error) {
	res, err := pairing.Invite(ctx, n.rendezvous(), onCode)
	if err != nil {
		return peers.Peer{}, err
	}
	return n.addPeer(res)
}

// Join pairs with the device showing code.
func (n *Node) Join(ctx context.Context, code string) (peers.Peer, error) {
	res, err := pairing.Join(ctx, n.rendezvous(), code)
	if err != nil {
		return peers.Peer{}, err
	}
	return n.addPeer(res)
}

func (n *Node) addPeer(res *pairing.Result) (peers.Peer, error) {
	if res.PublicKey.Equal(n.Identity.PublicKey) {
		return peers.Peer{}, errors.New("that's this device's own code")
	}
	if err := n.Peers.Add(peers.Peer{ID: res.ID, Name: res.Name, PublicKey: res.PublicKey, Policy: peers.PolicyAsk}); err != nil {
		return peers.Peer{}, err
	}
	return n.Peers.Get(res.ID)
}

// SendPhase marks the steps of an outgoing transfer before bytes flow.
type SendPhase int

const (
	PhaseConnecting SendPhase = iota // looking the peer up and dialing it
	PhaseWaiting                     // connected; waiting for the peer to accept
)

// SendOptions observes an outgoing transfer. Both callbacks are optional.
type SendOptions struct {
	OnPhase    func(SendPhase)
	OnProgress func(transfer.Progress)
	// OnConnected reports the path once a connection exists.
	OnConnected func(relayed bool)
}

// OfflineError means the peer isn't registered with the rendezvous server.
type OfflineError struct{ Name string }

func (e *OfflineError) Error() string { return e.Name + " is offline" }

// Send transfers the files at paths to a paired peer. See SendSources.
func (n *Node) Send(ctx context.Context, peerID, transferID string, paths []string, opts SendOptions) error {
	files, err := transfer.FileSources(paths)
	if err != nil {
		return err
	}
	return n.SendSources(ctx, peerID, transferID, files, opts)
}

// SendSources transfers files to a paired peer. Run must be active.
// Cancelling ctx aborts the transfer; the receiver discards partial files.
func (n *Node) SendSources(ctx context.Context, peerID, transferID string, files []transfer.Source, opts SendOptions) error {
	select {
	case <-n.ready:
	default:
		return errors.New("node is not running")
	}
	peer, err := n.Peers.Get(peerID)
	if err != nil {
		return err
	}
	if peer.RemovedUs {
		return &RemovedUsError{Name: peer.Name}
	}
	if opts.OnPhase != nil {
		opts.OnPhase(PhaseConnecting)
	}
	conn, relayed, err := n.connect(ctx, peer, true)
	if err != nil {
		return n.refused(peer, err)
	}
	defer conn.CloseWithError(0, "")
	if opts.OnConnected != nil {
		opts.OnConnected(relayed)
	}

	topts := transfer.SendOptions{Report: opts.OnProgress}
	if relayed {
		// Keep looking for a direct path; move over when one appears.
		upgrades := make(chan *quic.Conn)
		u := &upgrader{}
		uctx, stopUpgrading := context.WithCancel(ctx)
		defer func() {
			stopUpgrading()
			u.closeAll()
		}()
		go u.run(uctx, n, peer, upgrades)
		topts.Upgrades = upgrades
		topts.OnUpgraded = func() {
			if opts.OnConnected != nil {
				opts.OnConnected(false)
			}
		}
	}

	if opts.OnPhase != nil {
		opts.OnPhase(PhaseWaiting)
	}
	return n.refused(peer, transfer.Send(ctx, conn, transferID, files, topts))
}

// DiscardPartials deletes what an interrupted or paused incoming transfer
// kept for later (transferID is the sender's).
func (n *Node) DiscardPartials(peerID, transferID string) {
	n.partials.discard(peerID, transferID)
}

// NotifyCancel tells a peer that a transfer between us (transferID is the
// sender's ID) was cancelled here while we weren't connected.
func (n *Node) NotifyCancel(ctx context.Context, peerID, transferID string) error {
	return n.ask(ctx, peerID, func(conn *quic.Conn) error { return transfer.NotifyCancel(ctx, conn, transferID) })
}

// RequestResume asks a peer that sent us files to continue transferID (its
// ID for the transfer), after either side paused it or it broke off.
func (n *Node) RequestResume(ctx context.Context, peerID, transferID string) error {
	return n.ask(ctx, peerID, func(conn *quic.Conn) error { return transfer.RequestResume(ctx, conn, transferID) })
}

// ask connects to a peer just to deliver one request.
func (n *Node) ask(ctx context.Context, peerID string, do func(*quic.Conn) error) error {
	select {
	case <-n.ready:
	default:
		return errors.New("node is not running")
	}
	peer, err := n.Peers.Get(peerID)
	if err != nil {
		return err
	}
	if peer.RemovedUs {
		return &RemovedUsError{Name: peer.Name}
	}
	conn, _, err := n.connect(ctx, peer, true)
	if err != nil {
		return n.refused(peer, err)
	}
	defer conn.CloseWithError(0, "")
	return n.refused(peer, do(conn))
}
