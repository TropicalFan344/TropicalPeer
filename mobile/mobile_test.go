package mobile

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"TropicalPeer/internal/appcore"
	"TropicalPeer/internal/node"
	"TropicalPeer/internal/peers"
	"TropicalPeer/internal/protocol"
	"TropicalPeer/internal/server"
	"TropicalPeer/internal/transfer"
)

// events records what the phone UI would receive.
type events struct {
	mu  sync.Mutex
	got []struct{ name, json string }
}

func (e *events) OnEvent(name, data string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.got = append(e.got, struct{ name, json string }{name, data})
}

// wait returns the first event called name whose payload satisfies ok.
func (e *events) wait(t *testing.T, name string, ok func(string) bool) string {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		e.mu.Lock()
		for _, ev := range e.got {
			if ev.name == name && ok(ev.json) {
				e.mu.Unlock()
				return ev.json
			}
		}
		e.mu.Unlock()
	}
	t.Fatalf("no %q event arrived", name)
	return ""
}

// diskDocuments opens "content URIs" that are really paths, the way Android
// hands over a descriptor the Go side then owns.
type diskDocuments struct{ opened int }

func (d *diskDocuments) OpenFD(uri string) (int, error) {
	d.opened++
	fd, err := syscall.Open(uri, syscall.O_RDONLY, 0)
	return int(fd), err
}

func call[T any](t *testing.T, method string, args ...any) T {
	t.Helper()
	b, _ := json.Marshal(args)
	out, err := Call(method, string(b))
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	var v T
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("%s returned %q: %v", method, out, err)
	}
	return v
}

func TestPhoneSendsAndReceives(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.New(log.New(io.Discard, "", 0)).Serve(ctx, ln)
	addr := ln.Addr().String()

	// The friend's desktop: accepts everything.
	friendDir := t.TempDir()
	friendDownloads := filepath.Join(friendDir, "downloads")
	online := make(chan struct{}, 1)
	friend, err := node.New(friendDir, node.Events{
		OnOffer: func(context.Context, peers.Peer, protocol.Offer) bool { return true },
		OnServerStatus: func(ok bool, _ error) {
			if ok {
				select {
				case online <- struct{}{}:
				default:
				}
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	friend.SetName("Friend's PC")
	friend.UpdateSettings(node.Settings{ServerAddr: addr, Port: 0, DownloadDir: friendDownloads})
	go friend.Run(ctx)
	<-online

	// The phone. Android would pass getFilesDir() and an app downloads folder;
	// the server address is preset as if chosen in Settings earlier.
	dataDir, phoneDownloads := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(dataDir, "settings.json"),
		[]byte(`{"serverAddr":"`+addr+`","port":0,"downloadDir":"elsewhere"}`), 0o600)
	ev := &events{}
	docs := &diskDocuments{}
	SetLocalIPs("192.0.2.10\nfe80::1\nnot-an-ip", "")
	if err := Start(dataDir, phoneDownloads, "Pixel 8", ev, docs); err != nil {
		t.Fatal(err)
	}
	defer Stop()
	if err := Start(dataDir, phoneDownloads, "Pixel 8", ev, docs); err == nil {
		t.Fatal("a second Start should fail")
	}
	ev.wait(t, "server:status", func(s string) bool { return strings.Contains(s, `"online":true`) })

	t.Run("device and settings", func(t *testing.T) {
		dev := call[appcore.DeviceInfo](t, "GetDevice")
		if dev.Name == "" || dev.ID == "" {
			t.Fatalf("device = %+v", dev)
		}
		if isPlaceholderName(dev.Name) {
			t.Fatalf("device kept a placeholder name %q", dev.Name)
		}
		if s := call[appcore.SettingsDTO](t, "GetSettings"); s.DownloadDir != phoneDownloads {
			t.Fatalf("download folder = %q, want the app's %q", s.DownloadDir, phoneDownloads)
		}
		if _, err := Call("NoSuchMethod", ""); err == nil {
			t.Fatal("unknown method should fail")
		}
		if _, err := Call("JoinWithCode", `"not an array"`); err == nil {
			t.Fatal("non-array arguments should fail")
		}
		if _, err := Call("JoinWithCode", `[]`); err == nil {
			t.Fatal("a missing argument should fail")
		}
	})

	// Pair: the phone shows a code, the friend types it.
	inv := call[appcore.Invite](t, "StartInvite")
	if _, err := friend.Join(ctx, inv.Code); err != nil {
		t.Fatal(err)
	}
	ev.wait(t, "pair:done", func(string) bool { return true })
	friendID := friend.Identity.ID()

	t.Run("send a picked document", func(t *testing.T) {
		data := make([]byte, 3<<20+17)
		rand.Read(data)
		src := filepath.Join(t.TempDir(), "IMG_0042.jpg")
		os.WriteFile(src, data, 0o644)

		// Size unknown (-1): read from the descriptor.
		tr := call[appcore.TransferDTO](t, "SendDocuments", friendID,
			[]document{{URI: src, Name: "IMG_0042.jpg", Size: -1}})
		if tr.Total != int64(len(data)) {
			t.Fatalf("total = %d, want %d", tr.Total, len(data))
		}
		ev.wait(t, "transfer:update", func(s string) bool {
			return strings.Contains(s, `"id":"`+tr.ID+`"`) && strings.Contains(s, `"status":"done"`)
		})
		got, err := os.ReadFile(filepath.Join(friendDownloads, "IMG_0042.jpg"))
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("friend received %d bytes (err %v), want %d identical", len(got), err, len(data))
		}

		// Retry opens the document again.
		before := docs.opened
		call[appcore.TransferDTO](t, "RetryTransfer", tr.ID)
		ev.wait(t, "transfer:update", func(s string) bool {
			return !strings.Contains(s, `"id":"`+tr.ID+`"`) && strings.Contains(s, `"status":"done"`)
		})
		if docs.opened == before {
			t.Fatal("retry didn't reopen the document")
		}
	})

	t.Run("receive after accepting", func(t *testing.T) {
		data := []byte("notes from the friend")
		src := filepath.Join(t.TempDir(), "notes.txt")
		os.WriteFile(src, data, 0o644)
		myID := call[appcore.DeviceInfo](t, "GetDevice").ID

		errc := make(chan error, 1)
		go func() { errc <- friend.Send(ctx, myID, transfer.NewTransferID(), []string{src}, node.SendOptions{}) }()

		raw := ev.wait(t, "offer:new", func(string) bool { return true })
		var offer appcore.OfferDTO
		json.Unmarshal([]byte(raw), &offer)
		if offer.PeerName != "Friend's PC" || len(offer.Files) != 1 || offer.Files[0].Name != "notes.txt" {
			t.Fatalf("offer = %+v", offer)
		}
		call[any](t, "RespondOffer", offer.ID, true)
		if err := <-errc; err != nil {
			t.Fatal(err)
		}
		done := ev.wait(t, "transfer:update", func(s string) bool {
			return strings.Contains(s, `"direction":"receive"`) && strings.Contains(s, `"status":"done"`)
		})
		var tr appcore.TransferDTO
		json.Unmarshal([]byte(done), &tr)
		if len(tr.Saved) != 1 || filepath.Dir(tr.Saved[0]) != phoneDownloads {
			t.Fatalf("saved = %v, want one file in %s", tr.Saved, phoneDownloads)
		}
		if got, _ := os.ReadFile(tr.Saved[0]); !bytes.Equal(got, data) {
			t.Fatalf("received %q", got)
		}
	})

	// Events without a payload arrive as JSON null.
	call[any](t, "ClearFinishedTransfers")
	if got := ev.wait(t, "transfers:cleared", func(string) bool { return true }); got != "null" {
		t.Fatalf("transfers:cleared payload = %q, want null", got)
	}
}
