package linkshare

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/logging"
	"github.com/pion/webrtc/v4"

	"TropicalPeer/internal/portmap"
	"TropicalPeer/internal/rendezvous"
)

// Direct paths to browsers. Many home routers are symmetric: they give every
// destination its own public port, so the address STUN reports is useless
// to a browser behind another such router, and only the TURN relay works.
// A port the router forwards (UPnP / NAT-PMP) can be reached anyway, so with
// PortMapping on, each visitor gets a UDP socket of its own, the router
// forwards its port while the visitor is connected, and the browser is told
// the forwarded address as one more candidate.
//
// Routers are particular about such forwards. Some carry replies for one
// flow only: the first one we answer, and only if the forwarded port hasn't
// sent anything before; replies to anyone else leave from a new random port,
// which the browser's router drops. Hence a socket per visitor rather than
// per link, mappedHeadStart, and skipFrom: the browser's checks relayed by
// the TURN server (very quick when it runs next to us) must not take the
// one flow from the browser's direct ones.
//
// Only people who proved they have the link get candidates at all, and they
// already learn this network's public address from the others; the port
// answers nothing but ICE checks signed with that visitor's credentials.

// mappedHeadStart is how long a visitor's candidates are kept back while
// its port is forwarded, so the browser's checks reach the forwarded address
// before we send anything from it; our own checks (and the relay) follow.
const mappedHeadStart = 2 * time.Second

const mapLease = time.Hour

// findRouter looks for a router that forwards ports, once per link: it
// takes seconds, too long to wait for with every visitor.
func (s *Share) findRouter() {
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	r, err := portmap.Discover(ctx)
	if err != nil {
		if s.ctx.Err() == nil {
			s.opts.Log("link: no router forwarding for direct connections: %v", err)
		}
		return
	}
	s.router.Store(r)
}

// newAPI sets up WebRTC for one visitor: on a forwarded socket of its own
// when the router allows, offering only the addresses keepLocalIP allows.
func (v *visitor) newAPI(servers []rendezvous.ICEServer) *webrtc.API {
	se := webrtc.SettingEngine{}
	se.SetICETimeouts(10*time.Second, 30*time.Second, 2*time.Second)
	se.SetIPFilter(v.s.keep)
	if r := v.s.router.Load(); r != nil {
		if err := v.listenMapped(&se, r, servers); err != nil {
			v.s.opts.Log("link: %s: no router forward: %v", v.info.Name, err)
		}
	}
	return webrtc.NewAPI(webrtc.WithSettingEngine(se))
}

// listenMapped opens the visitor's socket, has r forward its port until the
// visitor is gone, and routes the visitor's ICE through it.
func (v *visitor) listenMapped(se *webrtc.SettingEngine, r *portmap.Router, servers []rendezvous.ICEServer) error {
	conn, err := net.ListenPacket("udp", ":0")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(v.ctx, 3*time.Second)
	m, err := r.Map(ctx, conn.LocalAddr().(*net.UDPAddr).Port, mapLease)
	var skip []net.IP
	if err == nil {
		skip = serverIPs(ctx, servers)
	}
	cancel()
	if err != nil {
		conn.Close()
		return err
	}
	lf := logging.NewDefaultLoggerFactory()
	lf.DefaultLogLevel = logging.LogLevelError // not the "unspecified address" warning
	mux := webrtc.NewICEUDPMux(lf.NewLogger("ice"), skipFrom{conn, skip})
	se.SetICEUDPMux(filteredMux{mux, v.s.keep})
	v.mapping = m
	go func() {
		defer conn.Close()
		for {
			select {
			case <-v.ctx.Done():
				rctx, c := context.WithTimeout(context.Background(), 3*time.Second)
				m.Remove(rctx)
				c()
				return
			case <-time.After(mapLease / 2):
				rctx, c := context.WithTimeout(v.ctx, 10*time.Second)
				m.Renew(rctx, mapLease)
				c()
			}
		}
	}()
	return nil
}

// skipFrom drops what arrives from the skip addresses (the STUN/TURN
// servers), so we never answer them from the forwarded port.
type skipFrom struct {
	net.PacketConn
	skip []net.IP
}

func (c skipFrom) ReadFrom(b []byte) (int, net.Addr, error) {
	for {
		n, from, err := c.PacketConn.ReadFrom(b)
		if ua, ok := from.(*net.UDPAddr); ok && err == nil && slices.ContainsFunc(c.skip, ua.IP.Equal) {
			continue
		}
		return n, from, err
	}
}

// serverIPs are the addresses of the STUN/TURN servers in servers.
func serverIPs(ctx context.Context, servers []rendezvous.ICEServer) []net.IP {
	var ips []net.IP
	for _, s := range servers {
		for _, u := range s.URLs {
			_, rest, _ := strings.Cut(u, ":") // "turn:host:port?transport=udp"
			rest, _, _ = strings.Cut(rest, "?")
			host, _, err := net.SplitHostPort(rest)
			if err != nil {
				host = rest
			}
			if ip := net.ParseIP(host); ip != nil {
				ips = append(ips, ip)
			} else if found, err := net.DefaultResolver.LookupIP(ctx, "ip", host); err == nil {
				ips = append(ips, found...)
			}
		}
	}
	return ips
}

// filteredMux offers only the addresses keep allows: the socket listens on
// all of them and would otherwise list every one, ignoring the setting
// engine's IP filter.
type filteredMux struct {
	ice.UDPMux
	keep func(net.IP) bool
}

func (m filteredMux) GetListenAddresses() []net.Addr {
	var out []net.Addr
	for _, a := range m.UDPMux.GetListenAddresses() {
		if ua, ok := a.(*net.UDPAddr); ok && !ua.IP.IsLoopback() && m.keep(ua.IP) {
			out = append(out, a)
		}
	}
	return out
}

// mappedCandidate is the forwarded address as a candidate for the browser,
// in the same media section as like, one of our own gathered candidates.
func mappedCandidate(m *portmap.Mapping, like webrtc.ICECandidateInit) webrtc.ICECandidateInit {
	// Server reflexive, at the priority STUN's candidates get (type
	// preference 100, component 1).
	const priority = 100<<24 | 65535<<8 | 255
	return webrtc.ICECandidateInit{
		Candidate: fmt.Sprintf("candidate:tpmap 1 udp %d %s %d typ srflx raddr 0.0.0.0 rport 0",
			priority, m.ExternalIP, m.ExternalPort),
		SDPMid:        like.SDPMid,
		SDPMLineIndex: like.SDPMLineIndex,
	}
}

// keepLocalIP keeps only the addresses this device reaches the internet
// from, so a link doesn't hand every visitor the addresses of virtual
// adapters (VMs, WSL, VPNs). No packets are sent to find them.
func keepLocalIP() func(net.IP) bool {
	var primary []net.IP
	for _, probe := range [][2]string{{"udp4", "8.8.8.8:53"}, {"udp6", "[2001:4860:4860::8888]:53"}} {
		c, err := net.Dial(probe[0], probe[1])
		if err != nil {
			continue
		}
		primary = append(primary, c.LocalAddr().(*net.UDPAddr).IP)
		c.Close()
	}
	return func(ip net.IP) bool {
		if len(primary) == 0 {
			return true // no route out: nothing to tell apart
		}
		for _, p := range primary {
			if p.Equal(ip) {
				return true
			}
		}
		return false
	}
}
