package portmap

import (
	"context"
	"net"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/huin/goupnp/dcps/internetgateway2"
	"github.com/huin/goupnp/httpu"
	"github.com/huin/goupnp/ssdp"
)

// Android 11+ doesn't let apps list network interfaces or read the routing
// table, which is how goupnp finds where to search and how NAT-PMP finds the
// router. The app passes both in from Android's ConnectivityManager instead.
var (
	platformIPs     atomic.Pointer[[]net.IP]
	platformGateway atomic.Pointer[net.IP]
)

// SetPlatformNetwork supplies this device's addresses and its router's
// address for platforms where Go can't look them up. A nil gateway means
// unknown.
func SetPlatformNetwork(ips []net.IP, gateway net.IP) {
	platformIPs.Store(&ips)
	platformGateway.Store(&gateway)
}

// platformIPv4 returns the supplied IPv4 addresses, or nil when Go may look
// them up itself.
func platformIPv4() (ips []net.IP, given bool) {
	p := platformIPs.Load()
	if p == nil {
		return nil, false
	}
	for _, ip := range *p {
		if v4 := ip.To4(); v4 != nil && !v4.IsLoopback() && !v4.IsLinkLocalUnicast() {
			ips = append(ips, v4)
		}
	}
	return ips, true
}

func platformRouter() net.IP {
	if p := platformGateway.Load(); p != nil {
		return (*p).To4()
	}
	return nil
}

// withLocal is a UPnP client that knows which of our addresses found it
// (goupnp only records that when it ran the search itself).
type withLocal struct {
	igd
	local net.IP
}

func (w withLocal) LocalAddr() net.IP { return w.local }

// discoverIGDFrom runs the UPnP router search from the given addresses: the
// same three searches goupnp would run, but without asking the OS which
// interfaces exist.
func discoverIGDFrom(ctx context.Context, ips []net.IP) (igd, error) {
	type found struct {
		c   igd
		err error
	}
	searches := []struct {
		urn  string
		open func(context.Context, *url.URL) ([]igd, error)
	}{
		{internetgateway2.URN_WANIPConnection_2, func(ctx context.Context, u *url.URL) ([]igd, error) {
			cs, err := internetgateway2.NewWANIPConnection2ClientsByURLCtx(ctx, u)
			return asIGD(cs), err
		}},
		{internetgateway2.URN_WANIPConnection_1, func(ctx context.Context, u *url.URL) ([]igd, error) {
			cs, err := internetgateway2.NewWANIPConnection1ClientsByURLCtx(ctx, u)
			return asIGD(cs), err
		}},
		{internetgateway2.URN_WANPPPConnection_1, func(ctx context.Context, u *url.URL) ([]igd, error) {
			cs, err := internetgateway2.NewWANPPPConnection1ClientsByURLCtx(ctx, u)
			return asIGD(cs), err
		}},
	}
	ch := make(chan found, len(searches))
	for _, s := range searches {
		go func() {
			c, err := searchOne(ctx, ips, s.urn, s.open)
			ch <- found{c, err}
		}()
	}
	var lastErr error = errNoGateway
	for range searches {
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

func searchOne(ctx context.Context, ips []net.IP, urn string, open func(context.Context, *url.URL) ([]igd, error)) (igd, error) {
	var clients []httpu.ClientInterfaceCtx
	for _, ip := range ips {
		c, err := httpu.NewHTTPUClientAddr(ip.String())
		if err != nil {
			continue
		}
		defer c.Close()
		clients = append(clients, c)
	}
	if len(clients) == 0 {
		return nil, errNoGateway
	}
	sctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	responses, err := ssdp.RawSearch(sctx, httpu.NewMultiClientCtx(clients), urn, 2)
	if err != nil {
		return nil, err
	}
	for _, r := range responses {
		loc, err := r.Location()
		if err != nil {
			continue
		}
		cs, err := open(ctx, loc)
		if err != nil || len(cs) == 0 {
			continue
		}
		return withLocal{cs[0], net.ParseIP(r.Header.Get(httpu.LocalAddressHeader))}, nil
	}
	return nil, errNoGateway
}

func asIGD[T igd](cs []T) []igd {
	out := make([]igd, len(cs))
	for i, c := range cs {
		out[i] = c
	}
	return out
}
