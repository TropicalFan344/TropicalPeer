package appcore

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"TropicalPeer/internal/node"
	"TropicalPeer/internal/update"
)

// fakeUpdater records what the updater asked the platform to do.
type fakeUpdater struct {
	dir        string
	mu         sync.Mutex
	installed  []string
	relaunched int
}

func (f *fakeUpdater) AssetName(v string) string { return "TropicalPeer-" + v + ".exe" }
func (f *fakeUpdater) Dir() string               { return f.dir }
func (f *fakeUpdater) InstallOnQuit() bool       { return true }
func (f *fakeUpdater) Install(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installed = append(f.installed, path)
	return nil
}
func (f *fakeUpdater) Relaunch() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.relaunched++
	return nil
}
func (f *fakeUpdater) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.installed), f.relaunched
}

// fakeReleases serves one signed release, v0.9.0.
func fakeReleases(t *testing.T) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	oldKey, oldBase := update.PublicKey, update.APIBase
	update.PublicKey = base64.StdEncoding.EncodeToString(pub)
	exe := []byte("TropicalPeer 0.9.0")
	sum := sha256.Sum256(exe)
	sums := fmt.Sprintf("%s  TropicalPeer-0.9.0.exe\n", hex.EncodeToString(sum[:]))
	files := map[string][]byte{
		"TropicalPeer-0.9.0.exe": exe,
		update.ChecksumsFile:     []byte(sums),
		update.SignatureFile:     []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(sums)))),
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/"+update.Repo+"/releases/latest" {
			var assets []map[string]string
			for name := range files {
				assets = append(assets, map[string]string{"name": name, "browser_download_url": srv.URL + "/dl/" + name})
			}
			json.NewEncoder(w).Encode(map[string]any{"tag_name": "v0.9.0", "assets": assets})
			return
		}
		if data, ok := files[r.URL.Path[len("/dl/"):]]; ok {
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	update.APIBase = srv.URL
	t.Cleanup(func() { srv.Close(); update.PublicKey, update.APIBase = oldKey, oldBase })
}

func TestAutomaticUpdate(t *testing.T) {
	fakeReleases(t)
	oldVersion, oldFirst, oldRestart := Version, updateFirstCheck, updateRestartIn
	Version, updateFirstCheck, updateRestartIn = "0.1.7", 10*time.Millisecond, 300*time.Millisecond
	defer func() { Version, updateFirstCheck, updateRestartIn = oldVersion, oldFirst, oldRestart }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rec := &recorder{}
	c := New(t.TempDir(), nil)
	c.ctx = ctx
	c.emitFn = rec.emit
	n, err := node.New(t.TempDir(), node.Events{})
	if err != nil {
		t.Fatal(err)
	}
	c.node = n
	up := &fakeUpdater{dir: t.TempDir()}
	c.SetUpdater(up)

	// A transfer is running: the update downloads but waits.
	busy := &transferState{dto: TransferDTO{ID: "t", Status: StatusSending}}
	c.mu.Lock()
	c.transfers["t"] = busy
	c.mu.Unlock()
	c.startUpdates()

	waitUpdate := func(what string, cond func(UpdateDTO) bool) UpdateDTO {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if d := c.GetUpdate(); cond(d) {
				return d
			}
		}
		t.Fatalf("%s: never happened (now %+v)", what, c.GetUpdate())
		return UpdateDTO{}
	}
	d := waitUpdate("download", func(d UpdateDTO) bool { return d.State == UpdateReady })
	if d.Version != "0.9.0" {
		t.Fatalf("ready with %+v", d)
	}
	time.Sleep(2500 * time.Millisecond)
	if d := c.GetUpdate(); d.RestartAt != 0 {
		t.Fatal("scheduled a restart while a transfer was running")
	}
	if i, r := up.counts(); i != 0 || r != 0 {
		t.Fatal("installed while a transfer was running")
	}

	// It finishes: notice, then install and relaunch.
	c.mu.Lock()
	busy.dto.Status = StatusDone
	c.mu.Unlock()
	waitUpdate("notice", func(d UpdateDTO) bool { return d.RestartAt != 0 })
	waitUpdate("install", func(d UpdateDTO) bool { return d.State == UpdateInstalling })
	if i, r := up.counts(); i != 1 || r != 1 {
		t.Fatalf("installed %d, relaunched %d; want 1, 1", i, r)
	}
	if got, _ := os.ReadFile(up.installed[0]); string(got) != "TropicalPeer 0.9.0" {
		t.Fatalf("installed %q", got)
	}
}

func TestPostponedUpdateInstallsOnQuit(t *testing.T) {
	fakeReleases(t)
	oldVersion, oldFirst, oldRestart := Version, updateFirstCheck, updateRestartIn
	Version, updateFirstCheck, updateRestartIn = "0.1.7", 10*time.Millisecond, 10*time.Second
	defer func() { Version, updateFirstCheck, updateRestartIn = oldVersion, oldFirst, oldRestart }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := New(t.TempDir(), nil)
	c.ctx = ctx
	c.emitFn = func(string, any) {}
	n, _ := node.New(t.TempDir(), node.Events{})
	c.node = n
	up := &fakeUpdater{dir: t.TempDir()}
	c.SetUpdater(up)
	c.startUpdates()
	for deadline := time.Now().Add(10 * time.Second); c.GetUpdate().RestartAt == 0; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no restart notice: %+v", c.GetUpdate())
		}
	}
	c.PostponeUpdate() // "Not now"
	time.Sleep(2500 * time.Millisecond)
	if i, r := up.counts(); i != 0 || r != 0 {
		t.Fatal("installed after 'Not now'")
	}
	c.installOnQuit()
	if i, r := up.counts(); i != 1 || r != 0 {
		t.Fatalf("on quit: installed %d, relaunched %d; want 1, 0", i, r)
	}
}

func TestDevBuildsDontUpdate(t *testing.T) {
	old := Version
	Version = "dev"
	defer func() { Version = old }()
	c := New(t.TempDir(), nil)
	c.ctx = context.Background()
	c.emitFn = func(string, any) {}
	c.SetUpdater(&fakeUpdater{dir: t.TempDir()})
	c.startUpdates()
	if s := c.GetUpdate().State; s != UpdateUnsupported {
		t.Fatalf("dev build: %q", s)
	}
}
