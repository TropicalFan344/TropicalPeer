package appcore

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
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"TropicalPeer/internal/node"
	"TropicalPeer/internal/peers"
	"TropicalPeer/internal/protocol"
	"TropicalPeer/internal/server"
	"TropicalPeer/internal/transfer"
)

// gatedReader passes through the first n bytes, then blocks until open is
// closed: a send that stalls mid-file, so the test can pull the plug.
type gatedReader struct {
	r    io.Reader
	n    int64
	open chan struct{}
}

func (g *gatedReader) Read(p []byte) (int, error) {
	if g.n <= 0 {
		<-g.open
		return g.r.Read(p)
	}
	if int64(len(p)) > g.n {
		p = p[:g.n]
	}
	k, err := g.r.Read(p)
	g.n -= int64(k)
	return k, err
}

func (g *gatedReader) Close() error { return nil }

// The receiving app quits partway through a file and starts again: the
// sender reconnects by itself, the receiver carries on without asking, and
// only the rest of the file crosses the network.
func TestResumeAfterReceiverRestart(t *testing.T) {
	saved := resumeWaits
	if os.Getenv("REAL_WAITS") == "" {
		resumeWaits = []time.Duration{300 * time.Millisecond, 300 * time.Millisecond, 500 * time.Millisecond, time.Second, time.Second, 2 * time.Second, 2 * time.Second, 3 * time.Second}
	}
	defer func() { resumeWaits = saved }()
	startAt := time.Now()
	defer func() { t.Logf("total %s", time.Since(startAt)) }()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.New(log.New(io.Discard, "", 0)).Serve(ctx, ln)
	addr := ln.Addr().String()

	// The receiver: a device that asks before accepting (and says yes).
	friendDir := t.TempDir()
	var asked atomic.Int32
	var firstProgress sync.Map // run -> the video's first FileDone seen
	friendEvents := func(run string) node.Events {
		return node.Events{
			OnOffer: func(context.Context, peers.Peer, protocol.Offer) bool { asked.Add(1); return true },
			OnProgress: func(_ peers.Peer, p transfer.Progress) {
				if p.FileName == "video.mp4" {
					firstProgress.LoadOrStore(run, p.FileDone)
				}
			},
		}
	}
	friendCtx, stopFriend := context.WithCancel(ctx)
	friend, downloads := startNodeIn(t, friendCtx, friendDir, addr, "Friend", friendEvents("first"))

	rec := &recorder{}
	app := New(t.TempDir(), nil)
	app.ctx = ctx
	app.emitFn = rec.emit
	app.node, _ = startNode(t, ctx, addr, "Me", node.Events{})
	pairNodes(t, ctx, app.node, friend)
	friendID := friend.Identity.ID()

	data := make([]byte, 48<<20)
	rand.Read(data)
	const cut = 20 << 20
	gate := make(chan struct{})
	var opens atomic.Int32
	src := transfer.Source{
		Name: "video.mp4",
		Size: int64(len(data)),
		Open: func() (io.ReadCloser, error) {
			if opens.Add(1) == 1 {
				return &gatedReader{r: bytes.NewReader(data), n: cut, open: gate}, nil
			}
			return io.NopCloser(bytes.NewReader(data)), nil
		},
	}
	notes := []byte("sent before the video")
	first := transfer.Source{Name: "notes.txt", Size: int64(len(notes)), Open: func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(notes)), nil
	}}
	d, err := app.SendSources(friendID, []transfer.Source{first, src})
	if err != nil {
		t.Fatal(err)
	}
	rec.waitFor(t, d.ID, func(d TransferDTO) bool { return d.Done > 0 })
	time.Sleep(time.Second) // the rest up to the cut reaches the friend
	if asked.Load() != 1 {
		t.Fatalf("asked %d times before the cut, want 1", asked.Load())
	}

	// The receiving app quits mid-file.
	stopFriend()
	time.Sleep(500 * time.Millisecond)
	close(gate)
	part := filepath.Join(downloads, "video.mp4.part")
	st, err := os.Stat(part)
	if err != nil || st.Size() < cut/2 {
		t.Fatalf("the part file wasn't kept: %v", err)
	}
	rec.waitFor(t, d.ID, func(d TransferDTO) bool { return d.Resuming })

	// It starts again; the sender finds it and carries on.
	_, downloads2 := startNodeIn(t, ctx, friendDir, addr, "Friend", friendEvents("second"))
	if downloads2 != downloads {
		t.Fatalf("download folder moved: %s", downloads2)
	}
	final := rec.waitFor(t, d.ID, func(d TransferDTO) bool { return d.Status == StatusDone || d.Status == StatusFailed })
	if final.Status != StatusDone {
		t.Fatalf("status %q: %s (history %v)", final.Status, final.Error, rec.history(d.ID))
	}
	got, err := os.ReadFile(filepath.Join(downloads, "video.mp4"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("received %d bytes (err %v), want the %d sent, identical", len(got), err, len(data))
	}
	if _, err := os.Stat(part); err == nil {
		t.Fatal("the part file is still there")
	}
	if asked.Load() != 1 {
		t.Fatalf("asked %d times, want only the first", asked.Load())
	}
	if v, ok := firstProgress.Load("second"); !ok || v.(int64) < cut/2 {
		t.Fatalf("the second attempt started at %v bytes, want where the first stopped (~%d)", v, cut)
	} else {
		t.Logf("first attempt kept %d bytes; the second started at %d of %d", st.Size(), v, len(data))
	}
	// The file that arrived before the cut isn't sent again.
	entries, _ := os.ReadDir(downloads)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 2 || names[0] != "notes.txt" || names[1] != "video.mp4" {
		t.Fatalf("download folder holds %v, want notes.txt and video.mp4", names)
	}
	if len(app.ListTransfers()) != 1 {
		t.Fatalf("the retry made a new row: %d transfers", len(app.ListTransfers()))
	}
}

// A part left by some other file with the same name and size: the hashes
// differ, so the file starts over and arrives intact.
func TestResumeWithMismatchedPart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.New(log.New(io.Discard, "", 0)).Serve(ctx, ln)
	addr := ln.Addr().String()

	friendDir := t.TempDir()
	friendCtx, stopFriend := context.WithCancel(ctx)
	friend, downloads := startNodeIn(t, friendCtx, friendDir, addr, "Friend", node.Events{})
	rec := &recorder{}
	app := New(t.TempDir(), nil)
	app.ctx = ctx
	app.emitFn = rec.emit
	app.node, _ = startNode(t, ctx, addr, "Me", node.Events{})
	pairNodes(t, ctx, app.node, friend)
	friend.Peers.Update(app.node.Identity.ID(), func(p *peers.Peer) { p.Policy = peers.PolicyAutoAccept })
	stopFriend()
	time.Sleep(300 * time.Millisecond)

	// Plant someone else's half-received file of the same name and size.
	data := make([]byte, 3<<20)
	rand.Read(data)
	os.MkdirAll(downloads, 0o755)
	part := filepath.Join(downloads, "report.pdf.part")
	os.WriteFile(part, bytes.Repeat([]byte{7}, 1<<20), 0o644)
	os.WriteFile(filepath.Join(friendDir, "resume.json"), []byte(`[{"peer":"`+app.node.Identity.ID()+`","transfer":"old","index":0,"name":"report.pdf","size":`+
		itoa(len(data))+`,"part":`+jsonString(part)+`,"final":`+jsonString(filepath.Join(downloads, "report.pdf"))+`,"updated":"`+time.Now().Format(time.RFC3339)+`"}]`), 0o600)
	startNodeIn(t, ctx, friendDir, addr, "Friend", node.Events{})

	srcPath := filepath.Join(t.TempDir(), "report.pdf")
	os.WriteFile(srcPath, data, 0o644)
	d, err := app.SendFiles(friend.Identity.ID(), []string{srcPath})
	if err != nil {
		t.Fatal(err)
	}
	final := rec.waitFinal(t, d.ID)
	if final.Status != StatusDone {
		t.Fatalf("status %q: %s", final.Status, final.Error)
	}
	got, err := os.ReadFile(filepath.Join(downloads, "report.pdf"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("received %d bytes (err %v), want the %d sent, identical", len(got), err, len(data))
	}
	if _, err := os.Stat(part); err == nil {
		t.Fatal("the stale part is still there")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// startNodeIn is startNode with a data folder that survives a restart.
func startNodeIn(t *testing.T, ctx context.Context, dir, serverAddr, name string, ev node.Events) (*node.Node, string) {
	t.Helper()
	online := make(chan struct{}, 1)
	ev.OnServerStatus = func(ok bool, err error) {
		if ok {
			select {
			case online <- struct{}{}:
			default:
			}
		}
	}
	n, err := node.New(dir, ev)
	if err != nil {
		t.Fatal(err)
	}
	downloads := filepath.Join(dir, "downloads")
	n.SetName(name)
	if err := n.UpdateSettings(node.Settings{ServerAddr: serverAddr, Port: 0, DownloadDir: downloads}); err != nil {
		t.Fatal(err)
	}
	go n.Run(ctx)
	select {
	case <-online:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s never came online", name)
	}
	return n, downloads
}
