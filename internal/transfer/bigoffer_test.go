package transfer

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"TropicalPeer/internal/identity"
	"TropicalPeer/internal/protocol"
	"TropicalPeer/internal/wire"
)

// slowLink delays every packet it sends, like a long route.
type slowLink struct {
	net.PacketConn
	delay time.Duration
}

func (l *slowLink) WriteTo(p []byte, addr net.Addr) (int, error) {
	b := append([]byte(nil), p...)
	time.AfterFunc(l.delay, func() { l.PacketConn.WriteTo(b, addr) })
	return len(p), nil
}

// quicPair connects two QUIC endpoints on loopback.
func quicPair(t *testing.T, ctx context.Context) (client, server *quic.Conn) {
	return quicPairDelayed(t, ctx, 0)
}

// quicPairDelayed is quicPair with each direction delayed by oneWay.
func quicPairDelayed(t *testing.T, ctx context.Context, oneWay time.Duration) (client, server *quic.Conn) {
	t.Helper()
	a, b := &identity.Identity{}, &identity.Identity{}
	for _, id := range []*identity.Identity{a, b} {
		loaded, err := identity.LoadOrCreate(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		*id = *loaded
	}
	srvTLS, err := b.ServerTLS(func(ed25519.PublicKey) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	srvUDP, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := (&quic.Transport{Conn: &slowLink{srvUDP, oneWay}}).Listen(srvTLS, &quic.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	cliTLS, err := a.ClientTLS(b.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan *quic.Conn, 1)
	go func() {
		c, _ := ln.Accept(ctx)
		accepted <- c
	}()

	cliUDP, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	client, err = (&quic.Transport{Conn: &slowLink{cliUDP, oneWay}}).Dial(ctx, ln.Addr(), cliTLS, &quic.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return client, <-accepted
}

// Many small files don't each wait a round trip: 400 files over a 40 ms
// round trip would take 16 s one by one.
func TestManySmallFilesAreQuick(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, server := quicPairDelayed(t, ctx, 20*time.Millisecond)
	dir := t.TempDir()
	received := make(chan error, 1)
	go func() {
		c, err := AcceptControl(ctx, server)
		if err != nil {
			received <- err
			return
		}
		_, err = NewIncoming(c).Receive(ctx, ctx, dir, func(context.Context, protocol.Offer) (bool, string) { return true, "" }, func(Progress) {})
		received <- err
	}()
	start := time.Now()
	if err := Send(ctx, client, NewTransferID(), manyFiles(400), SendOptions{}); err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	if err := <-received; err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadDir(filepath.Join(dir, "Camera", "2026", "October"))
	if len(got) != 400 {
		t.Fatalf("received %d files", len(got))
	}
	t.Logf("400 files in %s", took)
	if took > 4*time.Second {
		t.Fatalf("400 small files took %s", took)
	}
}

func manyFiles(n int) []Source {
	files := make([]Source, n)
	for i := range files {
		files[i] = Source{
			Name: fmt.Sprintf("Camera/2026/October/IMG_2026101%04d_with_a_long_name.jpg", i),
			Size: 1,
			Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader([]byte("x"))), nil },
		}
	}
	return files
}

// A receiver from before folders could read at most wire.MaxLine of an
// offer, and hung up (code 0) on a longer one. The sender says why.
func TestOldReceiverHangsUpOnALongOffer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, server := quicPair(t, ctx)
	go func() {
		st, err := server.AcceptStream(ctx)
		if err != nil {
			return
		}
		var hello any
		if err := wire.NewConn(st).Recv(&hello); err != nil {
			server.CloseWithError(0, "")
		}
	}()
	err := Send(ctx, client, NewTransferID(), manyFiles(1500), SendOptions{})
	if !errors.Is(err, ErrOfferTooBigForPeer) {
		t.Fatalf("Send = %v, want ErrOfferTooBigForPeer", err)
	}
}

// A current receiver takes the same offer.
func TestLongOfferIsAccepted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, server := quicPair(t, ctx)
	got := make(chan int, 1)
	go func() {
		c, err := AcceptControl(ctx, server)
		if err != nil {
			got <- -1
			return
		}
		NewIncoming(c).Receive(ctx, ctx, t.TempDir(), func(_ context.Context, o protocol.Offer) (bool, string) {
			got <- len(o.Files)
			return false, "just counting"
		}, func(Progress) {})
	}()
	err := Send(ctx, client, NewTransferID(), manyFiles(1500), SendOptions{})
	var rejected *RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("Send = %v, want the answer", err)
	}
	if n := <-got; n != 1500 {
		t.Fatalf("receiver read %d files", n)
	}
}

// dialStranger connects to a device that doesn't trust this one, as one
// that removed us. Its refusal shows on the dial or on first use.
func dialStranger(t *testing.T, ctx context.Context) (*quic.Conn, error) {
	t.Helper()
	me, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	other, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srvTLS, err := other.ServerTLS(func(ed25519.PublicKey) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	ln, err := quic.ListenAddr("127.0.0.1:0", srvTLS, &quic.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go ln.Accept(ctx)
	cliTLS, err := me.ClientTLS(other.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return quic.DialAddr(ctx, ln.Addr().String(), cliTLS, &quic.Config{})
}

// A device that removed us refuses our certificate (devices before 0.1.10
// say nothing else). That is told apart from other failures.
func TestRefusedCertificateIsRecognised(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := dialStranger(t, ctx)
	if err == nil {
		err = Send(ctx, client, NewTransferID(), manyFiles(1), SendOptions{})
	}
	if !IsRefusedByPeer(err) {
		t.Fatalf("not recognised as refused: %v", err)
	}
	if IsRefusedByPeer(errors.New("timeout: no recent network activity")) {
		t.Fatal("a timeout counted as refused")
	}
}

// Files that are slow to open (Android documents take ~20 ms) are opened
// ahead, so the waits overlap: 200 such files one by one would take 4 s.
func TestSlowToOpenFilesOverlap(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, server := quicPair(t, ctx)
	files := manyFiles(200)
	for i := range files {
		open := files[i].Open
		files[i].Open = func() (io.ReadCloser, error) {
			time.Sleep(20 * time.Millisecond)
			return open()
		}
	}
	received := make(chan error, 1)
	go func() {
		c, err := AcceptControl(ctx, server)
		if err != nil {
			received <- err
			return
		}
		_, err = NewIncoming(c).Receive(ctx, ctx, t.TempDir(), func(context.Context, protocol.Offer) (bool, string) { return true, "" }, func(Progress) {})
		received <- err
	}()
	start := time.Now()
	if err := Send(ctx, client, NewTransferID(), files, SendOptions{}); err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	if err := <-received; err != nil {
		t.Fatal(err)
	}
	t.Logf("200 slow-to-open files in %s", took)
	if took > 2*time.Second {
		t.Fatalf("took %s", took)
	}
}
