//go:build devhooks

package appcore

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"TropicalPeer/internal/node"
	"TropicalPeer/internal/transfer"
	"TropicalPeer/internal/update"
)

// Dev-only test hooks, compiled in only with `-tags devhooks`. They exist so
// UI states that need real-world conditions can be captured on one machine.
//
//   - TROPICALPEER_DEV_SEND="<peerID>|<path>[|<path>...]" starts a send once
//     the node is ready (no native file picker needed).
//   - TROPICALPEER_DEV_ROUTER="strict" | "restricted" reports that router
//     type on the Network page (with port forwarding unavailable).
//   - TROPICALPEER_DEV_LOG=<file> writes the log there (a GUI app has no console).
//   - TROPICALPEER_DEV_DEMO_TRANSFERS=1 adds two finished sample transfers:
//     one that stayed on the relay and one that moved to a direct path.
//   - TROPICALPEER_DEV_ACCEPT=1 accepts every offer as if a person clicked.
//   - TROPICALPEER_DEV_UPDATE_API=<url> looks for updates there instead of
//     GitHub (a local test server; releases must still be properly signed).
//   - TROPICALPEER_DEV_RELAY_ONLY=1 sends through the relay even when a
//     direct path exists (the upgrader still looks for one).
//   - TROPICALPEER_DEV_PAUSE_AT=<bytes> pauses the first transfer (either
//     direction) once that much has moved; TROPICALPEER_DEV_CANCEL_AFTER=<dur>
//     (e.g. 5s) then cancels it that long after it paused.
//
// With a log file, every transfer's status changes are logged too.
func (c *Core) runDevHooks() {
	if path := os.Getenv("TROPICALPEER_DEV_LOG"); path != "" {
		if f, err := os.Create(path); err == nil {
			log.SetOutput(f)
			log.SetFlags(log.Ltime | log.Lmicroseconds)
			go c.devWatchTransfers()
		}
	}
	if api := os.Getenv("TROPICALPEER_DEV_UPDATE_API"); api != "" {
		update.APIBase = api
		log.Printf("devhooks: looking for updates at %s", api)
	}
	if os.Getenv("TROPICALPEER_DEV_RELAY_ONLY") == "1" {
		node.DevRelayOnly = true
		log.Printf("devhooks: outgoing connections use the relay only")
	}
	if os.Getenv("TROPICALPEER_DEV_ACCEPT") == "1" {
		go c.devAcceptOffers()
	}
	if at, err := strconv.ParseInt(os.Getenv("TROPICALPEER_DEV_PAUSE_AT"), 10, 64); err == nil {
		after, _ := time.ParseDuration(os.Getenv("TROPICALPEER_DEV_CANCEL_AFTER"))
		go c.devPauseThenCancel(at, after)
	}
	if os.Getenv("TROPICALPEER_DEV_DEMO_TRANSFERS") == "1" {
		c.addDemoTransfers()
	}
	spec := os.Getenv("TROPICALPEER_DEV_SEND")
	if spec == "" {
		return
	}
	parts := strings.Split(spec, "|")
	if len(parts) < 2 {
		log.Printf("devhooks: TROPICALPEER_DEV_SEND wants peerID|path")
		return
	}
	if _, err := c.SendFiles(parts[0], parts[1:]); err != nil {
		log.Printf("devhooks: send: %v", err)
	}
}

// devTick runs fn every 100ms until the app stops or fn returns false.
func (c *Core) devTick(fn func() bool) {
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
		if !fn() {
			return
		}
	}
}

func (c *Core) devWatchTransfers() {
	seen := map[string]string{}
	c.devTick(func() bool {
		for _, d := range c.ListTransfers() {
			state := fmt.Sprintf("%s %s pausedByPeer=%v relayed=%v upgraded=%v done=%d/%d error=%q", d.Direction, d.Status, d.PausedByPeer, d.Relayed, d.Upgraded, d.Done, d.Total, d.Error)
			key := fmt.Sprint(d.Direction, d.Status, d.Error, d.PausedByPeer, d.Relayed, d.Upgraded)
			if seen[d.ID] != key {
				seen[d.ID] = key
				log.Printf("devhooks: transfer %s %s", d.ID, state)
			}
		}
		return true
	})
}

func (c *Core) devAcceptOffers() {
	c.devTick(func() bool {
		for _, o := range c.ListOffers() {
			log.Printf("devhooks: accepting %s from %s", o.ID, o.PeerName)
			c.RespondOffer(o.ID, true)
		}
		return true
	})
}

func (c *Core) devPauseThenCancel(at int64, after time.Duration) {
	var id string
	c.devTick(func() bool {
		for _, d := range c.ListTransfers() {
			if d.active() && d.Done >= at {
				id = d.ID
				log.Printf("devhooks: pausing %s at %d bytes", id, d.Done)
				c.PauseTransfer(id)
				return false
			}
		}
		return true
	})
	if id == "" || after <= 0 {
		return
	}
	select {
	case <-time.After(after):
	case <-c.ctx.Done():
		return
	}
	log.Printf("devhooks: cancelling %s", id)
	c.CancelTransfer(id)
}

func (c *Core) devAdjustNetwork(out *NetworkDTO) {
	switch r := os.Getenv("TROPICALPEER_DEV_ROUTER"); r {
	case "strict", "restricted":
		out.Router = r
		out.Forwarded = ""
		out.MappingErr = "the router doesn't offer UPnP or NAT-PMP port forwarding (or it is turned off)"
	}
}

func (c *Core) addDemoTransfers() {
	list := c.n().Peers.List()
	if len(list) == 0 {
		return
	}
	peer := list[0]
	now := time.Now()
	demo := []TransferDTO{
		{
			ID: transfer.NewTransferID(), PeerID: peer.ID, PeerName: peer.Name, Direction: string(transfer.Sending),
			Status: StatusDone, Files: []FileDTO{{Name: "family-photos.zip", Size: 734 << 20}}, Total: 734 << 20, Done: 734 << 20,
			Relayed: false, Upgraded: true, StartedAt: now.Add(-3 * time.Minute).UnixMilli(), EndedAt: now.Add(-2 * time.Minute).UnixMilli(),
		},
		{
			ID: transfer.NewTransferID(), PeerID: peer.ID, PeerName: peer.Name, Direction: string(transfer.Receiving),
			Status: StatusDone, Files: []FileDTO{{Name: "notes.pdf", Size: 2 << 20}}, Total: 2 << 20, Done: 2 << 20,
			Relayed: true, SaveDir: c.n().Settings().DownloadDir,
			Saved: []string{filepath.Join(c.n().Settings().DownloadDir, "notes.pdf")}, StartedAt: now.Add(-6 * time.Minute).UnixMilli(), EndedAt: now.Add(-6 * time.Minute).UnixMilli(),
		},
	}
	for _, d := range demo {
		st := &transferState{dto: d, cancel: func() {}}
		c.mu.Lock()
		c.transfers[d.ID] = st
		c.mu.Unlock()
		c.emitTransfer(st)
	}
}
