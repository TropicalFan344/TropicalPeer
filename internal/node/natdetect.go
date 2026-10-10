package node

import (
	"context"
	"net"
	"time"

	"TropicalPeer/internal/portmap"
	"TropicalPeer/internal/rendezvous"
)

// Router detection, done once per run:
//
//  1. Probe the server's main UDP port. The server answers and, from its
//     second port, also sends a "filter test" packet. Our router has never
//     seen that second port, so the test only arrives if the router lets in
//     packets from ports it hasn't sent to (lenient filtering).
//  2. Then probe the second port. If the router shows the server a different
//     public port for it than for the main port, the router is symmetric:
//     it gives every destination its own port, so peers can't aim at us.
//
// The result goes to the server, which passes it to devices that want to
// reach us, so they can skip straight to the relay when a direct path can't
// work (see rendezvous.DirectHopeless).
const (
	probeAckWait   = 1500 * time.Millisecond
	filterTestWait = 400 * time.Millisecond
	// relayDelayHopeless replaces relayDelay when routers say a direct path
	// can't work. It still gives same-LAN addresses a moment to win.
	relayDelayHopeless = 300 * time.Millisecond
	portMapLease       = time.Hour
)

// NetworkInfo summarizes how reachable this device is, for the UI.
type NetworkInfo struct {
	ServerOnline bool                  // registered with the rendezvous server
	ServerRTT    time.Duration         // round trip of the last probe (0 = unknown)
	Relay        bool                  // the server relays when direct paths fail
	Detection    bool                  // the server supports router detection
	PublicAddr   string                // our UDP address as the server sees it
	NAT          rendezvous.NATProfile // what our router is like
	Mapping      string                // "UPnP 203.0.113.7:7778" when the router forwards our port
	MappingErr   string                // why port forwarding isn't active ("" when it is, or when off)
	Checking     bool                  // a check is running
}

func (n *Node) Network() NetworkInfo {
	info := NetworkInfo{PublicAddr: n.PublicAddr(), ServerRTT: time.Duration(n.serverRTT.Load()), Checking: n.checking.Load()}
	if p, ok := n.natProfile.Load().(rendezvous.NATProfile); ok {
		info.NAT = p
	}
	if p, _ := n.presence.Load().(*rendezvous.Presence); p != nil {
		info.ServerOnline, info.Relay, info.Detection = true, p.Relay, p.AltPort != 0
	}
	n.mu.RLock()
	if n.mapping != nil {
		info.Mapping = n.mapping.Method + " " + n.mapping.Addr()
	}
	info.MappingErr = n.mappingErr
	n.mu.RUnlock()
	return info
}

func (n *Node) emitNetwork() {
	if n.events.OnNetwork != nil {
		n.events.OnNetwork(n.Network())
	}
}

// altServerUDP is the server's second UDP port, on the same host.
func (n *Node) altServerUDP(altPort int) (net.Addr, error) {
	main, err := n.serverUDP()
	if err != nil {
		return nil, err
	}
	ua, ok := main.(*net.UDPAddr)
	if !ok {
		return nil, net.UnknownNetworkError("not UDP")
	}
	alt := *ua
	alt.Port = altPort
	return &alt, nil
}

func waitFor(ctx context.Context, d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
	return true
}

// detectNAT measures our router (once per run) and returns its profile.
func (n *Node) detectNAT(ctx context.Context, altPort int) rendezvous.NATProfile {
	n.detectMu.Lock()
	defer n.detectMu.Unlock()
	if n.detected != nil {
		return *n.detected
	}
	var profile rendezvous.NATProfile
	if altPort == 0 {
		return profile // server can't help; don't cache, a later server might
	}
	alt, err := n.altServerUDP(altPort)
	if err != nil {
		return profile
	}

	n.mainAck.Store("")
	n.altAck.Store("")
	n.sendProbe()
	if !waitFor(ctx, probeAckWait, func() bool { a, _ := n.mainAck.Load().(string); return a != "" }) {
		return profile
	}
	// The filter test leaves the server right after the ack. It only means
	// something before we first contact the second port (after that our own
	// router lets it in), so a re-check reuses the first answer.
	var lenient bool
	if n.altContacted.Load() {
		lenient = n.lenientSeen
	} else {
		waitFor(ctx, filterTestWait, n.filterSeen.Load)
		lenient = n.filterSeen.Load()
		n.lenientSeen = lenient
	}

	n.altContacted.Store(true)
	n.sendProbeTo(alt)
	if !waitFor(ctx, probeAckWait, func() bool { a, _ := n.altAck.Load().(string); return a != "" }) {
		return profile
	}
	mainAddr, _ := n.mainAck.Load().(string)
	altAddr, _ := n.altAck.Load().(string)
	profile = rendezvous.NATProfile{Known: true, Symmetric: mainAddr != altAddr, Lenient: lenient}
	n.detected = &profile
	return profile
}

// Recheck runs the network checks again: router type, port forwarding and
// server round trip. Progress and the result arrive through OnNetwork.
func (n *Node) Recheck() {
	if !n.checking.CompareAndSwap(false, true) {
		return // already checking
	}
	n.emitNetwork()
	go func() {
		defer func() {
			n.checking.Store(false)
			n.emitNetwork()
		}()
		n.detectMu.Lock()
		n.detected = nil
		n.detectMu.Unlock()
		n.restartPortMapping()
		if p, _ := n.presence.Load().(*rendezvous.Presence); p != nil {
			n.mu.RLock()
			ctx := n.runCtx
			n.mu.RUnlock()
			if ctx != nil {
				n.reportNetwork(ctx, p)
			}
		}
	}()
}

// NetworkChanged is for platforms that report network switches (Android:
// Wi-Fi to mobile data and back). The public address and router change with
// the network, so it registers with the server again from the new one and
// reruns the checks.
func (n *Node) NetworkChanged() {
	n.restartPresence()
	n.Recheck()
}

// reportNetwork measures (if needed) and tells the server about our router.
func (n *Node) reportNetwork(ctx context.Context, p *rendezvous.Presence) {
	profile := n.detectNAT(ctx, p.AltPort)
	n.mu.RLock()
	profile.Mapped = n.mapping != nil
	n.mu.RUnlock()
	n.natProfile.Store(profile)
	p.SendNAT(profile)
	n.emitNetwork()
}

// registrationAddrs lists the addresses we register with the server: the
// router-forwarded one first (it works from anywhere), then LAN addresses.
func (n *Node) registrationAddrs() []string {
	addrs := localAddrs(n.port)
	n.mu.RLock()
	if n.mapping != nil {
		addrs = append([]string{n.mapping.Addr()}, addrs...)
	}
	n.mu.RUnlock()
	return addrs
}

// portMapLoop keeps a router port forward alive while port mapping is on.
func (n *Node) portMapLoop(ctx context.Context, done chan struct{}) {
	defer close(done)
	defer func() {
		n.mu.Lock()
		m := n.mapping
		n.mapping, n.mappingErr = nil, ""
		n.mu.Unlock()
		if m != nil {
			rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			m.Remove(rctx)
			cancel()
			n.restartPresence() // re-register without the forwarded address
		}
		n.emitNetwork()
	}()

	retry := time.Minute
	for {
		mctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		m, err := portmap.Map(mctx, n.port, portMapLease)
		cancel()
		if ctx.Err() != nil {
			if m != nil {
				rctx, c := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
				m.Remove(rctx)
				c()
			}
			return
		}
		n.mu.Lock()
		n.mapping = m
		n.mappingErr = ""
		if err != nil {
			n.mappingErr = err.Error()
		}
		n.mu.Unlock()
		n.emitNetwork()

		if m == nil {
			// No router support (or carrier-grade NAT); check again later in
			// case the network changes, backing off to once an hour.
			select {
			case <-ctx.Done():
				return
			case <-time.After(retry):
			}
			retry = min(retry*2, time.Hour)
			continue
		}

		n.restartPresence() // register the forwarded address
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(portMapLease / 2):
			}
			rctx, c := context.WithTimeout(ctx, 10*time.Second)
			err := m.Renew(rctx, portMapLease)
			c()
			if err != nil {
				break // lost it (router rebooted?): map again from scratch
			}
		}
		n.mu.Lock()
		n.mapping = nil
		n.mu.Unlock()
		retry = time.Minute
	}
}

// restartPortMapping starts or stops router forwarding to match settings.
func (n *Node) restartPortMapping() {
	n.mu.Lock()
	if n.portMapCancel != nil {
		n.portMapCancel()
		n.portMapCancel = nil
	}
	if n.portMapDone != nil {
		done := n.portMapDone
		n.mu.Unlock()
		<-done // the old loop removes its mapping first
		n.mu.Lock()
	}
	ctx := n.runCtx
	enabled := !n.settings.DisablePortMapping && n.ListenPacket == nil // real sockets only
	if ctx == nil || !enabled {
		n.mu.Unlock()
		return
	}
	pctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	n.portMapCancel, n.portMapDone = cancel, done
	n.mu.Unlock()
	go n.portMapLoop(pctx, done)
}

// StopPortMapping removes any router port forward and waits (up to the
// context) until the router has been told. Call it before the process exits;
// otherwise the forward lingers on the router until its lease runs out.
func (n *Node) StopPortMapping(ctx context.Context) {
	n.mu.Lock()
	cancel, done := n.portMapCancel, n.portMapDone
	n.portMapCancel, n.portMapDone = nil, nil
	n.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	select {
	case <-done:
	case <-ctx.Done():
	}
}
