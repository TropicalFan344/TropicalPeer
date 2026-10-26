// Package portmap asks the home router to forward ports to this machine,
// using UPnP-IGD and, failing that, NAT-PMP. A forwarded port makes a device
// (or a home-hosted rendezvous server) reachable from anywhere, whatever kind
// of NAT the router otherwise is.
package portmap

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/huin/goupnp/dcps/internetgateway2"
	"github.com/jackpal/gateway"
	natpmp "github.com/jackpal/go-nat-pmp"
)

const description = "TropicalPeer"

// Mapping is an active port forward on the router.
type Mapping struct {
	Method       string // "UPnP" or "NAT-PMP"
	Protocol     string // "UDP" or "TCP"
	ExternalIP   netip.Addr
	ExternalPort int
	InternalPort int

	renew  func(ctx context.Context, lifetime time.Duration) error
	remove func(ctx context.Context) error
}

// Addr is the public address peers can reach.
func (m *Mapping) Addr() string {
	return netip.AddrPortFrom(m.ExternalIP, uint16(m.ExternalPort)).String()
}

// Renew extends the lease. Routers drop mappings when leases run out.
func (m *Mapping) Renew(ctx context.Context, lifetime time.Duration) error {
	return m.renew(ctx, lifetime)
}

// Remove deletes the mapping from the router.
func (m *Mapping) Remove(ctx context.Context) error { return m.remove(ctx) }

// ErrNotPublic means the router's own external address is private or
// carrier-grade NAT, so forwarding a port on it can't help anyone reach us.
var ErrNotPublic = errors.New("the router itself is behind another network (carrier-grade NAT), so forwarding a port on it doesn't help")

// ErrNoRouterSupport means neither UPnP nor NAT-PMP answered.
var ErrNoRouterSupport = errors.New("the router doesn't offer UPnP or NAT-PMP port forwarding (or it is turned off)")

// Router is a home router that accepts port forwarding requests.
type Router struct {
	Method     string     // "UPnP" or "NAT-PMP"
	ExternalIP netip.Addr // the router's internet-facing address
	LocalIP    net.IP     // this machine's address as the router sees it

	igd igd            // set for UPnP
	pmp *natpmp.Client // set for NAT-PMP
}

// Discover finds the router, trying UPnP first and then NAT-PMP, and reads
// its external address. It fails with ErrNotPublic when that address isn't
// reachable from the internet.
func Discover(ctx context.Context) (*Router, error) {
	r, upnpErr := discoverUPnP(ctx)
	if upnpErr != nil {
		var pmpErr error
		r, pmpErr = discoverNATPMP(ctx)
		if pmpErr != nil {
			return nil, fmt.Errorf("%w (UPnP: %v; NAT-PMP: %v)", ErrNoRouterSupport, upnpErr, pmpErr)
		}
	}
	if !IsPublic(r.ExternalIP) {
		return r, ErrNotPublic
	}
	return r, nil
}

// Forward asks the router to forward protocol ("UDP" or "TCP") port
// externalPort to internalPort on this machine for lifetime.
func (r *Router) Forward(ctx context.Context, protocol string, internalPort, externalPort int, lifetime time.Duration) (*Mapping, error) {
	protocol = strings.ToUpper(protocol)
	m := &Mapping{Method: r.Method, Protocol: protocol, ExternalIP: r.ExternalIP, InternalPort: internalPort}
	if r.igd != nil {
		add := func(ctx context.Context, lifetime time.Duration) error {
			return r.igd.AddPortMappingCtx(ctx, "", uint16(externalPort), protocol, uint16(internalPort),
				r.LocalIP.String(), true, description, uint32(lifetime.Seconds()))
		}
		if err := add(ctx, lifetime); err != nil {
			return nil, describeUPnPError(err, protocol, externalPort)
		}
		m.ExternalPort = externalPort
		m.renew = add
		m.remove = func(ctx context.Context) error {
			return r.igd.DeletePortMappingCtx(ctx, "", uint16(externalPort), protocol)
		}
		return m, nil
	}

	proto := strings.ToLower(protocol)
	res, err := withContext(ctx, func() (*natpmp.AddPortMappingResult, error) {
		return r.pmp.AddPortMapping(proto, internalPort, externalPort, int(lifetime.Seconds()))
	})
	if err != nil {
		return nil, err
	}
	m.ExternalPort = int(res.MappedExternalPort)
	m.renew = func(ctx context.Context, lifetime time.Duration) error {
		_, err := withContext(ctx, func() (*natpmp.AddPortMappingResult, error) {
			return r.pmp.AddPortMapping(proto, internalPort, m.ExternalPort, int(lifetime.Seconds()))
		})
		return err
	}
	m.remove = func(ctx context.Context) error {
		_, err := withContext(ctx, func() (*natpmp.AddPortMappingResult, error) {
			return r.pmp.AddPortMapping(proto, internalPort, 0, 0)
		})
		return err
	}
	return m, nil
}

// Map forwards UDP internalPort for a device, preferring the same external
// port and falling back to random ones when it's taken. lifetime is the
// lease; call Renew before it ends.
func Map(ctx context.Context, internalPort int, lifetime time.Duration) (*Mapping, error) {
	r, err := Discover(ctx)
	if err != nil {
		return nil, err
	}
	return r.Map(ctx, internalPort, lifetime)
}

// Map is the package's Map on a router already found, which saves the
// seconds discovery takes. The router's external address is read again
// first: it may have changed since discovery.
func (r *Router) Map(ctx context.Context, internalPort int, lifetime time.Duration) (*Mapping, error) {
	if err := r.refreshExternalIP(ctx); err != nil {
		return nil, err
	}
	ports := []int{internalPort}
	for range 3 {
		ports = append(ports, 20000+rand.IntN(40000))
	}
	var lastErr error
	for _, ext := range ports {
		m, err := r.Forward(ctx, "UDP", internalPort, ext, lifetime)
		if err == nil {
			return m, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// IsPublic reports whether ip is a globally routable address: not private,
// loopback, link-local, or carrier-grade NAT (100.64.0.0/10).
func IsPublic(ip netip.Addr) bool {
	if !ip.IsValid() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	if ip.Is4() && netip.MustParsePrefix("100.64.0.0/10").Contains(ip) {
		return false
	}
	return true
}

// igd is what the three UPnP service flavours routers expose have in common.
type igd interface {
	AddPortMappingCtx(ctx context.Context, remoteHost string, externalPort uint16, protocol string,
		internalPort uint16, internalClient string, enabled bool, description string, lease uint32) error
	GetExternalIPAddressCtx(ctx context.Context) (string, error)
	DeletePortMappingCtx(ctx context.Context, remoteHost string, externalPort uint16, protocol string) error
	LocalAddr() net.IP
}

func discoverUPnP(ctx context.Context) (*Router, error) {
	c, err := discoverIGD(ctx)
	if err != nil {
		return nil, err
	}
	local := c.LocalAddr()
	if local == nil {
		return nil, errors.New("couldn't tell which local address faces the router")
	}
	ipStr, err := c.GetExternalIPAddressCtx(ctx)
	if err != nil {
		return nil, err
	}
	extIP, err := netip.ParseAddr(ipStr)
	if err != nil {
		return nil, fmt.Errorf("router reported external address %q", ipStr)
	}
	return &Router{Method: "UPnP", ExternalIP: extIP, LocalIP: local, igd: c}, nil
}

var errNoGateway = errors.New("no UPnP gateway answered")

func discoverIGD(ctx context.Context) (igd, error) {
	dctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if ips, given := platformIPv4(); given {
		return discoverIGDFrom(dctx, ips)
	}
	type found struct {
		c   igd
		err error
	}
	ch := make(chan found, 3)
	go func() {
		cs, _, err := internetgateway2.NewWANIPConnection2ClientsCtx(dctx)
		if len(cs) > 0 {
			ch <- found{c: cs[0]}
			return
		}
		ch <- found{err: err}
	}()
	go func() {
		cs, _, err := internetgateway2.NewWANIPConnection1ClientsCtx(dctx)
		if len(cs) > 0 {
			ch <- found{c: cs[0]}
			return
		}
		ch <- found{err: err}
	}()
	go func() {
		cs, _, err := internetgateway2.NewWANPPPConnection1ClientsCtx(dctx)
		if len(cs) > 0 {
			ch <- found{c: cs[0]}
			return
		}
		ch <- found{err: err}
	}()
	var lastErr error = errNoGateway
	for range 3 {
		f := <-ch
		if f.c != nil {
			return f.c, nil
		}
		if f.err != nil {
			lastErr = f.err
		}
	}
	return nil, lastErr
}

func discoverNATPMP(ctx context.Context) (*Router, error) {
	gw, local := platformRouter(), net.IP(nil)
	if ips, given := platformIPv4(); given {
		if gw == nil {
			return nil, errors.New("the router's address isn't known")
		}
		if len(ips) > 0 {
			local = ips[0]
		}
	} else {
		var err error
		if gw, err = gateway.DiscoverGateway(); err != nil {
			return nil, err
		}
		local, _ = gateway.DiscoverInterface()
	}
	c := natpmp.NewClientWithTimeout(gw, 2*time.Second)
	res, err := withContext(ctx, c.GetExternalAddress)
	if err != nil {
		return nil, err
	}
	return &Router{Method: "NAT-PMP", ExternalIP: netip.AddrFrom4(res.ExternalIPAddress), LocalIP: local, pmp: c}, nil
}

func (r *Router) refreshExternalIP(ctx context.Context) error {
	var ip netip.Addr
	if r.igd != nil {
		s, err := r.igd.GetExternalIPAddressCtx(ctx)
		if err != nil {
			return err
		}
		if ip, err = netip.ParseAddr(s); err != nil {
			return fmt.Errorf("router reported external address %q", s)
		}
	} else {
		res, err := withContext(ctx, r.pmp.GetExternalAddress)
		if err != nil {
			return err
		}
		ip = netip.AddrFrom4(res.ExternalIPAddress)
	}
	if !IsPublic(ip) {
		return ErrNotPublic
	}
	r.ExternalIP = ip
	return nil
}

// withContext runs a blocking NAT-PMP call, giving up when ctx ends.
func withContext[T any](ctx context.Context, f func() (T, error)) (T, error) {
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, err := f()
		ch <- result{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// describeUPnPError turns the router's SOAP fault codes into something a
// person can act on.
func describeUPnPError(err error, protocol string, port int) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "718"): // ConflictInMappingEntry
		return fmt.Errorf("%s port %d is already forwarded to another device on your network", protocol, port)
	case strings.Contains(msg, "725"): // OnlyPermanentLeasesSupported
		return fmt.Errorf("the router only accepts permanent forwards for %s port %d", protocol, port)
	case strings.Contains(msg, "606"), strings.Contains(msg, "Action not authorized"):
		return fmt.Errorf("the router refused to forward %s port %d (UPnP may be read-only)", protocol, port)
	}
	return fmt.Errorf("forwarding %s port %d: %w", protocol, port, err)
}
