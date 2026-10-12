package desktopapp

import (
	"fmt"
	"log"
	"math"
	"path/filepath"
	"strings"
	"sync"

	"TropicalPeer/internal/appcore"
	"TropicalPeer/internal/desktop"
)

// System notifications, as on the phone: while the window isn't in front,
// an incoming offer shows Accept and Decline, and finished files say so.
// The platform half (notify_windows.go, notify_darwin.go) shows them and
// reports clicks to onNote.

// note is one system notification.
type note struct {
	id      string // "offer:<offer id>" or "received:<transfer id>"
	title   string
	body    string
	actions []noteAction
}

type noteAction struct{ id, title string }

// notes remembers what a click on a "received" notification opens.
type notes struct {
	mu       sync.Mutex
	received map[string]received // by transfer ID
}

type received struct {
	path   string // the file, or the folder when there were several
	folder bool
}

func (a *App) notifyEvent(name string, data any) {
	switch name {
	case "offer:new":
		o, ok := data.(appcore.OfferDTO)
		if !ok || desktop.AppInFront() {
			return
		}
		a.showNote(note{
			id:    "offer:" + o.ID,
			title: a.core.T("offer.wants", "name", o.PeerName, "what", a.filesWhat(o.Files)),
			body:  formatSize(o.Total),
			actions: []noteAction{
				{"decline", a.core.T("offer.decline")},
				{"accept", a.core.T("offer.accept")},
			},
		})
	case "offer:closed":
		if c, ok := data.(appcore.OfferClosed); ok {
			a.removeNote("offer:" + c.ID)
		}
	case "transfer:update":
		t, ok := data.(appcore.TransferDTO)
		if !ok || t.Direction != "receive" || t.Status != appcore.StatusDone || len(t.Saved) == 0 {
			return
		}
		a.notes.mu.Lock()
		if a.notes.received == nil {
			a.notes.received = map[string]received{}
		}
		_, seen := a.notes.received[t.ID]
		r := received{path: t.Saved[0]}
		if len(t.Saved) > 1 {
			r = received{path: t.SaveDir, folder: true}
		}
		a.notes.received[t.ID] = r
		a.notes.mu.Unlock()
		if seen || desktop.AppInFront() {
			return
		}
		what := filepath.Base(t.Saved[0])
		if len(t.Saved) > 1 {
			what = a.core.T("common.files", "count", len(t.Saved))
		}
		a.showNote(note{
			id:    "received:" + t.ID,
			title: a.core.T("android.receivedTitle", "name", what),
			body:  a.core.T("android.receivedFrom", "name", t.PeerName),
		})
	}
}

// showNote shows n, or falls back to the taskbar or Dock when
// notifications aren't available.
func (a *App) showNote(n note) {
	if err := a.sendNote(n); err != nil {
		log.Printf("notification: %v", err)
		if strings.HasPrefix(n.id, "offer:") {
			a.drawAttention()
		}
	}
}

// onNote handles a click on a notification or one of its buttons.
func (a *App) onNote(id, action string) {
	kind, key, _ := strings.Cut(id, ":")
	switch {
	case kind == "offer" && (action == "accept" || action == "decline"):
		// Fails only if the offer already expired or was answered.
		_ = a.core.RespondOffer(key, action == "accept")
	case kind == "received":
		a.notes.mu.Lock()
		r, ok := a.notes.received[key]
		a.notes.mu.Unlock()
		switch {
		case !ok:
			a.showWindow()
		case r.folder:
			_ = a.OpenFolder(r.path)
		default:
			_ = a.ShowFile(r.path)
		}
	default:
		a.showWindow()
	}
}

// filesWhat names what an offer holds: the file, or how many.
func (a *App) filesWhat(files []appcore.FileDTO) string {
	n := len(files)
	if n == 1 {
		return files[0].Name
	}
	return a.core.T("common.files", "count", n)
}

// formatSize matches the app's formatSize (frontend/src/lib/format.ts).
func formatSize(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%d B", b)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	v, u := float64(b)/1024, 0
	for v >= 1024 && u < len(units)-1 {
		v /= 1024
		u++
	}
	if v >= 100 || v == math.Round(v) {
		return fmt.Sprintf("%.0f %s", v, units[u])
	}
	return fmt.Sprintf("%.1f %s", v, units[u])
}
