package portmap

import (
	"context"
	"errors"
	"time"
)

// Spec is one port to forward. The external port always equals the internal
// one: a server's address is shared with people, so it must not move.
type Spec struct {
	Protocol string // "TCP" or "UDP"
	Port     int
}

// Forwards is a set of port forwards that live and die together.
type Forwards struct {
	Router   *Router
	Mappings []*Mapping
	lifetime time.Duration
}

// ForwardAll forwards every spec on the router, all or nothing: if one fails,
// the ones already made are removed again and the error is returned.
func ForwardAll(ctx context.Context, specs []Spec, lifetime time.Duration) (*Forwards, error) {
	r, err := Discover(ctx)
	if err != nil {
		return nil, err
	}
	f := &Forwards{Router: r, lifetime: lifetime}
	for _, s := range specs {
		m, err := r.Forward(ctx, s.Protocol, s.Port, s.Port, lifetime)
		if err == nil && m.ExternalPort != s.Port {
			// NAT-PMP may hand out a different port; a server can't use that.
			m.Remove(ctx)
			err = errors.New("the router would only forward " + s.Protocol + " on a different port number")
		}
		if err != nil {
			f.Remove(context.WithoutCancel(ctx))
			return nil, err
		}
		f.Mappings = append(f.Mappings, m)
	}
	return f, nil
}

// KeepAlive renews the leases until ctx ends, then removes every forward
// (giving the router a few seconds) and returns. onRenewError reports a
// renewal that failed (nil to ignore).
func (f *Forwards) KeepAlive(ctx context.Context, onRenewError func(*Mapping, error)) {
	t := time.NewTicker(f.lifetime / 2)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			f.Remove(context.WithoutCancel(ctx))
			return
		case <-t.C:
			for _, m := range f.Mappings {
				rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				if err := m.Renew(rctx, f.lifetime); err != nil && onRenewError != nil {
					onRenewError(m, err)
				}
				cancel()
			}
		}
	}
}

// Remove deletes every forward from the router, waiting at most 5 seconds.
func (f *Forwards) Remove(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for _, m := range f.Mappings {
		m.Remove(ctx)
	}
	f.Mappings = nil
}
