// Command tpcli is a command-line TropicalPeer client, used to exercise the
// core before the GUI exists. Run two of them with different -data folders
// to simulate two devices on one machine.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"TropicalPeer/internal/node"
	"TropicalPeer/internal/peers"
	"TropicalPeer/internal/protocol"
	"TropicalPeer/internal/transfer"
)

const usage = `usage: tpcli [flags] <command> [args]

commands:
  info                          show this device's name and ID
  name <new name>               change this device's name
  invite                        show a pairing code and wait for another device
  join <code>                   pair with the device showing <code>
  peers                         list paired devices
  policy <peer> ask|auto|block  set how files from <peer> are accepted
         [maxAutoSize]          optional: auto-accept only up to this size (e.g. 500MB)
  remove <peer>                 forget a paired device
  run                           stay online and receive files
  send <peer> <file>...         send files to a paired device
  network                       check how reachable this device is (router type,
                                port forwarding); forwarding is removed on exit

<peer> is a device ID or name. Flags (saved to settings when given):
`

func main() {
	dataDir := flag.String("data", node.DefaultDataDir(), "folder holding identity, peers and settings")
	serverAddr := flag.String("server", "", "rendezvous server host:port")
	port := flag.Int("port", -1, "UDP port to listen on (0 = random)")
	downloads := flag.String("downloads", "", "folder for received files")
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, usage)
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ui := newConsole()
	n, err := node.New(*dataDir, ui.events())
	if err != nil {
		fatal(err)
	}
	s := n.Settings()
	if *serverAddr != "" {
		s.ServerAddr = *serverAddr
	}
	if *port >= 0 {
		s.Port = *port
	}
	if *downloads != "" {
		s.DownloadDir = *downloads
	}
	if s != n.Settings() {
		if err := n.UpdateSettings(s); err != nil {
			fatal(err)
		}
	}

	if err := runCommand(ctx, n, ui, flag.Arg(0), flag.Args()[1:]); err != nil {
		if ctx.Err() != nil {
			fatal(errors.New("cancelled"))
		}
		fatal(err)
	}
}

func runCommand(ctx context.Context, n *node.Node, ui *console, cmd string, args []string) error {
	switch cmd {
	case "info":
		s := n.Settings()
		fmt.Printf("Name:      %s\nDevice ID: %s\nServer:    %s\nUDP port:  %d\nDownloads: %s\n",
			n.Identity.Name, n.Identity.ID(), s.ServerAddr, s.Port, s.DownloadDir)

	case "name":
		if len(args) == 0 {
			return errors.New("usage: tpcli name <new name>")
		}
		return n.SetName(strings.Join(args, " "))

	case "invite":
		p, err := n.Invite(ctx, func(code string) {
			fmt.Printf("Pairing code:  %s\nEnter it on the other device (tpcli join %s). Waiting...\n", code, code)
		})
		if err != nil {
			return err
		}
		fmt.Printf("Paired with %q (%s). Files from it will be asked about; change with: tpcli policy %s auto\n", p.Name, p.ID, p.ID)

	case "join":
		if len(args) == 0 {
			return errors.New("usage: tpcli join <code>")
		}
		p, err := n.Join(ctx, strings.Join(args, "-"))
		if err != nil {
			return err
		}
		fmt.Printf("Paired with %q (%s).\n", p.Name, p.ID)

	case "peers":
		list := n.Peers.List()
		if len(list) == 0 {
			fmt.Println("No paired devices yet. Use 'tpcli invite' on one device and 'tpcli join <code>' on the other.")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tID\tPOLICY\tAUTO-ACCEPT LIMIT")
		for _, p := range list {
			limit := "-"
			if p.MaxAutoSize > 0 {
				limit = formatSize(p.MaxAutoSize)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.Name, p.ID, p.Policy, limit)
		}
		return w.Flush()

	case "policy":
		if len(args) < 2 {
			return errors.New("usage: tpcli policy <peer> ask|auto|block [maxAutoSize]")
		}
		p, err := n.Peers.Find(args[0])
		if err != nil {
			return err
		}
		policy, err := peers.ParsePolicy(args[1])
		if err != nil {
			return err
		}
		var limit int64
		if len(args) > 2 {
			if limit, err = parseSize(args[2]); err != nil {
				return err
			}
		}
		return n.Peers.Update(p.ID, func(p *peers.Peer) { p.Policy, p.MaxAutoSize = policy, limit })

	case "remove":
		if len(args) == 0 {
			return errors.New("usage: tpcli remove <peer>")
		}
		p, err := n.Peers.Find(args[0])
		if err != nil {
			return err
		}
		return n.Peers.Remove(p.ID)

	case "network":
		go n.Run(ctx)
		<-n.Ready()
		fmt.Println("Checking your network (takes a few seconds)...")
		deadline := time.After(15 * time.Second)
		for {
			info := n.Network()
			mappingSettled := info.Mapping != "" || info.MappingErr != "" || n.Settings().DisablePortMapping
			if info.NAT.Known && mappingSettled {
				break
			}
			select {
			case <-deadline:
				fmt.Println("(some checks didn't finish in time)")
				goto report
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}
	report:
		info := n.Network()
		fmt.Printf("Public address: %s\n", orDash(info.PublicAddr))
		switch {
		case !info.NAT.Known:
			fmt.Println("Router:         unknown (the server has no second UDP port, or didn't answer)")
		case info.NAT.Symmetric:
			fmt.Println("Router:         strict (symmetric NAT): peers can't reach you directly; transfers use the relay unless your router forwards a port")
		case info.NAT.Lenient:
			fmt.Println("Router:         open: direct connections should work")
		default:
			fmt.Println("Router:         port-restricted: direct connections work with most peers")
		}
		switch {
		case info.Mapping != "":
			fmt.Printf("Forwarding:     %s (removed when this command exits)\n", info.Mapping)
		case n.Settings().DisablePortMapping:
			fmt.Println("Forwarding:     off in settings")
		default:
			fmt.Printf("Forwarding:     not available: %s\n", orDash(info.MappingErr))
		}
		cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		n.StopPortMapping(cctx)
		cancel()

	case "run":
		go func() {
			<-n.Ready()
			fmt.Printf("%s is online on UDP port %d. Saving to %s. Ctrl+C to stop.\n",
				n.Identity.Name, n.Port(), n.Settings().DownloadDir)
		}()
		err := n.Run(ctx)
		cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		n.StopPortMapping(cctx)
		cancel()
		return err

	case "send":
		if len(args) < 2 {
			return errors.New("usage: tpcli send <peer> <file>...")
		}
		p, err := n.Peers.Find(args[0])
		if err != nil {
			return err
		}
		errc := make(chan error, 1)
		go func() { errc <- n.Run(ctx) }()
		select {
		case <-n.Ready():
		case err := <-errc:
			return err
		}
		fmt.Printf("Sending to %s... (waiting for it to accept)\n", p.Name)
		progress := ui.events().OnProgress
		err = n.Send(ctx, p.ID, transfer.NewTransferID(), args[1:], node.SendOptions{
			OnProgress: func(pr transfer.Progress) { progress(p, pr) },
			OnConnected: func(relayed bool) {
				if relayed {
					fmt.Println("Connected through the server's relay (still end-to-end encrypted).")
				} else {
					fmt.Println("Connected directly.")
				}
			},
		})
		if err != nil {
			ui.endLine()
			return err
		}
		ui.endLine()
		fmt.Println("Done.")

	default:
		return fmt.Errorf("unknown command %q (run tpcli without arguments for help)", cmd)
	}
	return nil
}

// console renders node events in the terminal.
type console struct {
	mu     sync.Mutex
	stdin  *bufio.Reader
	inLine bool
}

func newConsole() *console { return &console{stdin: bufio.NewReader(os.Stdin)} }

func (c *console) events() node.Events {
	return node.Events{
		OnServerStatus: func(online bool, err error) {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.endLineLocked()
			switch {
			case online:
				fmt.Println("[server] connected")
			case err == nil:
				fmt.Println("[server] connecting...")
			default:
				fmt.Printf("[server] disconnected: %v (retrying)\n", err)
			}
		},
		OnOffer: func(ctx context.Context, peer peers.Peer, offer protocol.Offer) bool {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.endLineLocked()
			fmt.Printf("\n%s wants to send %d file(s), %s:\n", peer.Name, len(offer.Files), formatSize(offer.TotalSize()))
			for _, f := range offer.Files {
				fmt.Printf("  %s  (%s)\n", f.Name, formatSize(f.Size))
			}
			fmt.Print("Accept? [y/N] ")
			answer, _ := c.stdin.ReadString('\n')
			answer = strings.ToLower(strings.TrimSpace(answer))
			return answer == "y" || answer == "yes"
		},
		OnProgress: func(peer peers.Peer, p transfer.Progress) {
			c.mu.Lock()
			defer c.mu.Unlock()
			verb := "Sending"
			if p.Direction == transfer.Receiving {
				verb = "Receiving"
			}
			pct := 100.0
			if p.Total > 0 {
				pct = float64(p.Done) * 100 / float64(p.Total)
			}
			fmt.Printf("\r%s %s  %5.1f%%  %s / %s   ", verb, p.FileName, pct, formatSize(p.Done), formatSize(p.Total))
			c.inLine = true
		},
		OnReceived: func(peer peers.Peer, offer protocol.Offer, files []transfer.Result, err error) {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.endLineLocked()
			var rejected *transfer.RejectedError
			switch {
			case errors.As(err, &rejected):
				fmt.Printf("Declined files from %s.\n", peer.Name)
			case err != nil:
				fmt.Printf("Transfer from %s failed: %v\n", peer.Name, err)
			default:
				for _, f := range files {
					fmt.Printf("Received from %s: %s\n", peer.Name, f.Path)
				}
			}
		},
	}
}

func (c *console) endLine() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.endLineLocked()
}

func (c *console) endLineLocked() {
	if c.inLine {
		fmt.Println()
		c.inLine = false
	}
}

func formatSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func parseSize(s string) (int64, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	mult := int64(1)
	for _, u := range []struct {
		suffix string
		mult   int64
	}{{"TB", 1 << 40}, {"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}, {"B", 1}} {
		if strings.HasSuffix(s, u.suffix) {
			s, mult = strings.TrimSpace(strings.TrimSuffix(s, u.suffix)), u.mult
			break
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("invalid size %q (try 500MB or 2GB)", s)
	}
	return int64(v * float64(mult)), nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
