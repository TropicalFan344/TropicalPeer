package appcore

import (
	"context"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"TropicalPeer/internal/node"
	"TropicalPeer/internal/peers"
	"TropicalPeer/internal/protocol"
	"TropicalPeer/internal/server"
)

func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

// Restart applies a new listening port without quitting, keeps the paired
// devices, and leaves the app able to send.
func TestRestartAppliesPort(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.New(log.New(io.Discard, "", 0)).Serve(ctx, ln)
	addr := ln.Addr().String()

	friend, friendDownloads := startNode(t, ctx, addr, "Friend", node.Events{
		OnOffer: func(context.Context, peers.Peer, protocol.Offer) bool { return true },
	})

	dir := t.TempDir()
	first, second := freeUDPPort(t), freeUDPPort(t)
	os.WriteFile(filepath.Join(dir, "settings.json"),
		[]byte(`{"serverAddr":"`+addr+`","port":`+strconv.Itoa(first)+`,"downloadDir":"`+filepath.ToSlash(filepath.Join(dir, "dl"))+`","disablePortMapping":true}`), 0o600)
	rec := &recorder{}
	app := New(dir, rec.emit)
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitPort := func(want int) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if app.n() != nil && app.GetDevice().Port == want && app.GetServerStatus().Online {
				return
			}
		}
		t.Fatalf("port %d, online %v; want port %d online", app.GetDevice().Port, app.GetServerStatus().Online, want)
	}
	waitPort(first)
	pairNodes(t, ctx, app.n(), friend)
	friendID := friend.Identity.ID()

	s := app.GetSettings()
	s.ListenPort = second
	if err := app.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	if app.GetDevice().Port != first {
		t.Fatal("the port changed before a restart")
	}
	if err := app.Restart(); err != nil {
		t.Fatal(err)
	}
	waitPort(second)

	if peers := app.ListPeers(); len(peers) != 1 || peers[0].ID != friendID {
		t.Fatalf("peers after restart: %+v", peers)
	}
	src := filepath.Join(t.TempDir(), "after-restart.txt")
	os.WriteFile(src, []byte("still works"), 0o644)
	d, err := app.SendFiles(friendID, []string{src})
	if err != nil {
		t.Fatal(err)
	}
	if final := rec.waitFinal(t, d.ID); final.Status != StatusDone {
		t.Fatalf("send after restart: %s %s", final.Status, final.Error)
	}
	if got, _ := os.ReadFile(filepath.Join(friendDownloads, "after-restart.txt")); string(got) != "still works" {
		t.Fatalf("friend got %q", got)
	}
}
