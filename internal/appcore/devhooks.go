//go:build devhooks

package appcore

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"TropicalPeer/internal/transfer"
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
func (c *Core) runDevHooks() {
	if path := os.Getenv("TROPICALPEER_DEV_LOG"); path != "" {
		if f, err := os.Create(path); err == nil {
			log.SetOutput(f)
			log.SetFlags(log.Ltime | log.Lmicroseconds)
		}
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
