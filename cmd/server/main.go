// Command server runs the TropicalPeer rendezvous server.
//
//	server -listen :7777
//	server -upnp          (hosting at home: forward the ports on your router)
//
// It listens on TCP (presence, pairing, introductions) and UDP on the same
// port (address probes and relaying), plus a second UDP port (default +100)
// that lets devices detect strict routers. Open all three on your firewall,
// or pass -upnp to have a home router forward them automatically.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"TropicalPeer/internal/portmap"
	"TropicalPeer/internal/server"
)

func main() {
	listen := flag.String("listen", ":7777", "address to listen on (TCP and UDP)")
	relay := flag.Bool("relay", true, "relay traffic for devices that can't reach each other directly")
	relayRate := flag.Int64("relay-rate", 32, "per-transfer relay cap in MB/s (0 = unlimited)")
	altPort := flag.Int("alt-port", 0, "second UDP port for router detection (0 = listen port + 100, -1 = off)")
	upnp := flag.Bool("upnp", false, "ask the home router (UPnP / NAT-PMP) to forward the server's ports while it runs")
	flag.Parse()

	logger := log.New(os.Stdout, "", log.LstdFlags)
	host, portStr, err := net.SplitHostPort(*listen)
	if err != nil {
		logger.Fatalf("bad -listen %q: %v", *listen, err)
	}
	port, _ := strconv.Atoi(portStr)

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		logger.Fatal(err)
	}
	pc, err := net.ListenPacket("udp", *listen)
	if err != nil {
		logger.Fatal(err)
	}
	logger.Printf("TropicalPeer rendezvous server on %s (TCP+UDP), relay %v", ln.Addr(), *relay)

	// Ctrl+C, and on Windows also closing the console window (delivered as
	// SIGTERM), shut down cleanly so router forwards get removed.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := server.New(logger)
	srv.Relay = *relay
	srv.RelayRate = *relayRate << 20
	go func() {
		if err := srv.ServeUDP(ctx, pc); err != nil {
			logger.Printf("udp: %v", err)
		}
	}()

	specs := []portmap.Spec{{Protocol: "TCP", Port: port}, {Protocol: "UDP", Port: port}}
	if *altPort >= 0 {
		ap := *altPort
		if ap == 0 {
			ap = port + 100
		}
		alt, err := net.ListenPacket("udp", net.JoinHostPort(host, strconv.Itoa(ap)))
		if err != nil {
			logger.Printf("router detection off: %v", err)
		} else {
			logger.Printf("router detection on UDP port %d", ap)
			srv.EnableAlt(alt)
			go srv.ServeUDPAlt(ctx, alt)
			specs = append(specs, portmap.Spec{Protocol: "UDP", Port: ap})
		}
	}

	forwardsDone := make(chan struct{})
	if *upnp {
		go func() {
			defer close(forwardsDone)
			forwardPorts(ctx, logger, host, specs)
		}()
	} else {
		close(forwardsDone)
	}

	if err := srv.Serve(ctx, ln); err != nil {
		logger.Fatal(err)
	}
	<-forwardsDone // forwards are removed before we exit
}

// forwardPorts asks the router to forward the server's ports, prints the
// address to share, keeps the leases alive, and removes the forwards when
// ctx ends.
func forwardPorts(ctx context.Context, logger *log.Logger, host string, specs []portmap.Spec) {
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		logger.Printf("upnp: not forwarding: the server only listens on %s, which the router can't reach. Use -listen :%d", host, specs[0].Port)
		return
	}
	logger.Printf("upnp: asking your router to forward the server's ports...")
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	f, err := portmap.ForwardAll(dctx, specs, time.Hour)
	cancel()
	if err != nil {
		if errors.Is(err, portmap.ErrNotPublic) {
			logger.Printf("upnp: %v. People outside your network can't reach a server here; use a VPS instead.", err)
		} else {
			logger.Printf("upnp: %v. Forward these ports by hand instead: %s", err, describe(specs))
		}
		return
	}

	share := net.JoinHostPort(f.Router.ExternalIP.String(), strconv.Itoa(specs[0].Port))
	logger.Printf("upnp: %s forwarded by your router (%s) to %s", describe(specs), f.Router.Method, f.Router.LocalIP)
	fmt.Println()
	fmt.Printf("  Share this server address:    %s\n", share)
	if f.Router.LocalIP != nil {
		fmt.Printf("  Devices on this home network: %s\n", net.JoinHostPort(f.Router.LocalIP.String(), strconv.Itoa(specs[0].Port)))
	}
	fmt.Println("  (Your public address can change; a free dynamic DNS name, e.g. from DuckDNS, keeps it stable.)")
	fmt.Println()

	f.KeepAlive(ctx, func(m *portmap.Mapping, err error) {
		logger.Printf("upnp: renewing %s %d failed: %v", m.Protocol, m.ExternalPort, err)
	})
	logger.Printf("upnp: forwards removed from your router")
}

func describe(specs []portmap.Spec) string {
	s := ""
	for i, p := range specs {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("%s %d", p.Protocol, p.Port)
	}
	return s
}
