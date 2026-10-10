package mobile

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"TropicalPeer/internal/appcore"
	"TropicalPeer/internal/transfer"
)

type handler func(c *appcore.Core, args []json.RawMessage) (any, error)

// methods mirrors the desktop App's bound methods (app.go), minus the ones
// only a desktop has (native dialogs, the file manager). SendDocuments
// replaces SendFiles, since Android hands over documents, not paths.
var methods = map[string]handler{
	"GetDevice":       noArgs(func(c *appcore.Core) any { return c.GetDevice() }),
	"GetServerStatus": noArgs(func(c *appcore.Core) any { return c.GetServerStatus() }),
	"GetNetwork":      noArgs(func(c *appcore.Core) any { return c.GetNetwork() }),
	"RecheckNetwork":  noArgs(func(c *appcore.Core) any { c.RecheckNetwork(); return nil }),
	"GetSettings":     noArgs(func(c *appcore.Core) any { return c.GetSettings() }),
	"Restart":         func(c *appcore.Core, _ []json.RawMessage) (any, error) { return nil, c.Restart() },
	"SetLanguage":     oneString(func(c *appcore.Core, lang string) (any, error) { c.SetLanguage(lang); return nil, nil }),
	"SetDeviceName": func(c *appcore.Core, a []json.RawMessage) (any, error) {
		name, err := arg[string](a, 0)
		if err != nil {
			return nil, err
		}
		return nil, c.SetDeviceName(name)
	},
	"SaveSettings": func(c *appcore.Core, a []json.RawMessage) (any, error) {
		s, err := arg[appcore.SettingsDTO](a, 0)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		s.DownloadDir = dlDir // fixed on Android; see Start
		mu.Unlock()
		return nil, c.SaveSettings(s)
	},

	"ListPeers": noArgs(func(c *appcore.Core) any { return c.ListPeers() }),
	"UpdatePeer": func(c *appcore.Core, a []json.RawMessage) (any, error) {
		id, err := arg[string](a, 0)
		if err != nil {
			return nil, err
		}
		u, err := arg[appcore.PeerUpdate](a, 1)
		if err != nil {
			return nil, err
		}
		u.SaveDir = "" // per-peer folders are a desktop feature
		return c.UpdatePeer(id, u)
	},
	"ReorderPeers": func(c *appcore.Core, a []json.RawMessage) (any, error) {
		ids, err := arg[[]string](a, 0)
		if err != nil {
			return nil, err
		}
		return nil, c.ReorderPeers(ids)
	},
	"RemovePeer":   oneString(func(c *appcore.Core, id string) (any, error) { return nil, c.RemovePeer(id) }),
	"StartInvite":  func(c *appcore.Core, _ []json.RawMessage) (any, error) { return c.StartInvite() },
	"CancelInvite": noArgs(func(c *appcore.Core) any { c.CancelInvite(); return nil }),
	"JoinWithCode": oneString(func(c *appcore.Core, code string) (any, error) { return c.JoinWithCode(code) }),

	"SendDocuments": func(c *appcore.Core, a []json.RawMessage) (any, error) {
		peerID, err := arg[string](a, 0)
		if err != nil {
			return nil, err
		}
		list, err := arg[[]document](a, 1)
		if err != nil {
			return nil, err
		}
		sources, err := documentSources(list)
		if err != nil {
			return nil, err
		}
		return c.SendSources(peerID, sources)
	},
	"CancelTransfer":         oneString(func(c *appcore.Core, id string) (any, error) { c.CancelTransfer(id); return nil, nil }),
	"RetryTransfer":          oneString(func(c *appcore.Core, id string) (any, error) { return c.RetryTransfer(id) }),
	"PauseTransfer":          oneString(func(c *appcore.Core, id string) (any, error) { c.PauseTransfer(id); return nil, nil }),
	"ResumeTransfer":         oneString(func(c *appcore.Core, id string) (any, error) { return c.ResumeTransfer(id) }),
	"ListTransfers":          noArgs(func(c *appcore.Core) any { return c.ListTransfers() }),
	"ClearFinishedTransfers": noArgs(func(c *appcore.Core) any { c.ClearFinishedTransfers(); return nil }),
	"ListOffers":             noArgs(func(c *appcore.Core) any { return c.ListOffers() }),
	"RespondOffer": func(c *appcore.Core, a []json.RawMessage) (any, error) {
		id, err := arg[string](a, 0)
		if err != nil {
			return nil, err
		}
		accept, err := arg[bool](a, 1)
		if err != nil {
			return nil, err
		}
		return nil, c.RespondOffer(id, accept)
	},
}

// document is a file picked on the phone. Size is -1 when Android doesn't
// know it (OpenableColumns.SIZE was null); it is then read from the
// descriptor.
type document struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// documentSources turns picked documents into transfer sources that open
// through Documents.OpenFD, so a retry opens the document again.
func documentSources(list []document) ([]transfer.Source, error) {
	mu.Lock()
	d := docs
	mu.Unlock()
	if d == nil {
		return nil, fmt.Errorf("TropicalPeer isn't running")
	}
	out := make([]transfer.Source, 0, len(list))
	for _, doc := range list {
		open := func() (*os.File, error) {
			fd, err := d.OpenFD(doc.URI)
			if err != nil {
				return nil, fmt.Errorf("can't open %s: %w", doc.Name, err)
			}
			return os.NewFile(uintptr(fd), doc.Name), nil
		}
		size := doc.Size
		if size < 0 {
			f, err := open()
			if err != nil {
				return nil, err
			}
			st, err := f.Stat()
			f.Close()
			if err != nil || !st.Mode().IsRegular() {
				return nil, fmt.Errorf("can't tell how big %s is", doc.Name)
			}
			size = st.Size()
		}
		name := strings.TrimSpace(doc.Name)
		if name == "" {
			name = "file"
		}
		out = append(out, transfer.Source{
			Name: name,
			Size: size,
			Open: func() (io.ReadCloser, error) { return open() },
		})
	}
	return out, nil
}

func noArgs(f func(c *appcore.Core) any) handler {
	return func(c *appcore.Core, _ []json.RawMessage) (any, error) { return f(c), nil }
}

func oneString(f func(c *appcore.Core, s string) (any, error)) handler {
	return func(c *appcore.Core, a []json.RawMessage) (any, error) {
		s, err := arg[string](a, 0)
		if err != nil {
			return nil, err
		}
		return f(c, s)
	}
}

// arg decodes argument i.
func arg[T any](args []json.RawMessage, i int) (T, error) {
	var v T
	if i >= len(args) {
		return v, fmt.Errorf("missing argument %d", i+1)
	}
	if err := json.Unmarshal(args[i], &v); err != nil {
		return v, fmt.Errorf("argument %d: %w", i+1, err)
	}
	return v, nil
}
