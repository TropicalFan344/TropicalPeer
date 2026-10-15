package main

import (
	"context"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/pion/stun/v4"
)

// A port check: can this server be reached at its public address? It
// connects to itself through the router. That isn't quite the view from
// outside (some routers can't loop back at all), but on routers that can,
// it catches forwards that were accepted but don't work, which some routers'
// UPnP produces.

type reachCheck struct {
	proto string // "TCP", or "STUN" for a UDP port that answers STUN
	port  int
}

func (c reachCheck) String() string {
	if c.proto == "STUN" {
		return "UDP " + strconv.Itoa(c.port)
	}
	return c.proto + " " + strconv.Itoa(c.port)
}

func checkReach(ctx context.Context, logger *log.Logger, public net.IP, local net.IP, checks []reachCheck) {
	var bad []string
	for _, c := range checks {
		if ctx.Err() != nil {
			return
		}
		if !reachable(ctx, public, c) {
			bad = append(bad, c.String())
		}
	}
	switch {
	case len(bad) == 0:
		logger.Printf("ports: all reachable at %s", public)
	case len(bad) == len(checks):
		logger.Printf("ports: couldn't reach this server at its public address %s from inside your network. Your router may just not allow that; if devices elsewhere can connect, all is well.", public)
	default:
		// local is set when we asked the router (UPnP): the forward exists
		// but doesn't work. Otherwise there's simply no forward yet.
		why := "Your router accepted the forward but doesn't pass the traffic. Forward them by hand"
		if local == nil {
			why = "Forward them"
			local = outboundIP()
		}
		to := "this computer"
		if local != nil {
			to = local.String()
		}
		logger.Printf("ports: NOT reachable at %s: %s. %s on your router's port forwarding page, to %s (same port numbers).",
			public, strings.Join(bad, ", "), why, to)
	}
}

func reachable(ctx context.Context, ip net.IP, c reachCheck) bool {
	addr := net.JoinHostPort(ip.String(), strconv.Itoa(c.port))
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if c.proto == "TCP" {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp4", addr)
		if err != nil {
			return false
		}
		conn.Close()
		return true
	}
	conn, err := net.Dial("udp4", addr)
	if err != nil {
		return false
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	req := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
	buf := make([]byte, 1500)
	for i := 0; i < 3; i++ {
		if _, err := conn.Write(req.Raw); err != nil {
			return false
		}
		conn.SetReadDeadline(time.Now().Add(time.Second))
		n, err := conn.Read(buf)
		if err == nil && stun.IsMessage(buf[:n]) {
			return true
		}
		if ctx.Err() != nil {
			break
		}
	}
	return false
}

// outboundIP is this computer's address on its network (no packet is sent).
func outboundIP() net.IP {
	c, err := net.Dial("udp4", "192.0.2.1:9")
	if err != nil {
		return nil
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP
}
