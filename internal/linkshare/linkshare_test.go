package linkshare

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"

	"TropicalPeer/internal/rendezvous"
	"TropicalPeer/internal/server"
	"TropicalPeer/internal/transfer"
)

// A rendezvous server with links on, in this process.
func startServer(t *testing.T) (*rendezvous.Client, string) {
	t.Helper()
	srv := server.New(log.New(io.Discard, "", 0))
	srv.Links = &server.Links{WebURL: "http://test/r/", ICE: func(string) []rendezvous.ICEServer { return nil }}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.Serve(ctx, ln)
	web := httptest.NewServer(srv.WebHandler())
	t.Cleanup(web.Close)
	return &rendezvous.Client{Addr: ln.Addr().String()}, "ws" + strings.TrimPrefix(web.URL, "http") + "/ws"
}

func memFile(name string, data []byte) transfer.Source {
	return transfer.Source{Name: name, Size: int64(len(data)), Open: func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}}
}

// browser plays the receiver page: it opens the link, proves it has the
// secret through the server, and once admitted connects; like the page, it
// sends its key check the moment the DataChannel opens, and checks the
// sender's signature against the link ID.
type browser struct {
	t      *testing.T
	pc     *webrtc.PeerConnection
	dc     *webrtc.DataChannel
	msgs   chan pageMsg
	chunks chan []byte
	closed chan struct{}

	signaled atomic.Bool // the sender sent WebRTC signaling (an answer or candidates)
}

func openLink(t *testing.T, wsURL, link, secretB64 string) *browser {
	t.Helper()
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })
	var wmu sync.Mutex
	sendWS := func(v any) {
		wmu.Lock()
		defer wmu.Unlock()
		ws.WriteJSON(v)
	}
	sendWS(map[string]string{"type": "open", "link": link})
	var opened struct {
		Type, Session string
		ICE           []rendezvous.ICEServer
	}
	if err := ws.ReadJSON(&opened); err != nil || opened.Type != "opened" {
		t.Fatalf("open: %v %+v", err, opened)
	}
	if len(opened.ICE) != 0 {
		t.Fatal("the server handed out STUN/TURN servers before the key check")
	}
	b := &browser{t: t, msgs: make(chan pageMsg, 64), chunks: make(chan []byte, 4096), closed: make(chan struct{})}
	secret, _ := base64.RawURLEncoding.DecodeString(secretB64)
	hm := hmac.New(sha256.New, secret)
	hm.Write([]byte(strings.Join([]string{authContext, "hello", link, opened.Session}, "\n")))
	sendWS(map[string]any{"type": "signal", "data": map[string]string{"hello": base64.RawURLEncoding.EncodeToString(hm.Sum(nil))}})

	var admitted struct {
		Type, Error string
		ICE         []rendezvous.ICEServer
		Data        json.RawMessage
	}
	if err := ws.ReadJSON(&admitted); err != nil {
		t.Fatalf("waiting to be admitted: %v", err)
	}
	switch admitted.Type {
	case "admitted":
	case "error":
		b.msgs <- pageMsg{Type: "error", Error: admitted.Error}
		return b
	default:
		t.Fatalf("before admission the server sent %+v", admitted)
	}

	cfg := webrtc.Configuration{}
	if offerCert != nil {
		cfg.Certificates = []webrtc.Certificate{*offerCert}
	}
	pc, err := webrtc.NewPeerConnection(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	b.pc = pc
	b.dc, _ = pc.CreateDataChannel("files", nil)
	payload := func(role string) []byte {
		senderFP, _ := fingerprint(pc.RemoteDescription())
		browserFP, _ := fingerprint(pc.LocalDescription())
		return []byte(strings.Join([]string{authContext, role, link, opened.Session, senderFP, browserFP}, "\n"))
	}
	b.dc.OnOpen(func() {
		m := hmac.New(sha256.New, secret)
		m.Write(payload("browser"))
		b.dc.SendText(fmt.Sprintf(`{"type":"auth","mac":%q}`, base64.RawURLEncoding.EncodeToString(m.Sum(nil))))
	})
	var once sync.Once
	b.dc.OnClose(func() { once.Do(func() { close(b.closed) }) })
	b.dc.OnMessage(func(m webrtc.DataChannelMessage) {
		if !m.IsString {
			b.chunks <- append([]byte(nil), m.Data...)
			return
		}
		var pm pageMsg
		json.Unmarshal(m.Data, &pm)
		if pm.Type == "auth" {
			pub, _ := base64.RawURLEncoding.DecodeString(pm.Pub)
			sig, _ := base64.RawURLEncoding.DecodeString(pm.Sig)
			if len(pub) != ed25519.PublicKeySize || rendezvous.LinkIDFor(pub) != link || !ed25519.Verify(pub, payload("sender"), sig) {
				t.Errorf("the sender's signature doesn't check out")
			}
		}
		b.msgs <- pm
	})
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			init := c.ToJSON()
			sendWS(map[string]any{"type": "signal", "data": map[string]any{"candidate": init}})
		}
	})
	offer, _ := pc.CreateOffer(nil)
	pc.SetLocalDescription(offer)
	sent := *pc.LocalDescription()
	if mungeOffer != nil {
		sent.SDP = mungeOffer(sent.SDP)
	}
	sendWS(map[string]any{"type": "signal", "data": map[string]any{"sdp": sent}})
	go func() {
		for {
			var m struct {
				Type string
				Data struct {
					SDP       *webrtc.SessionDescription `json:"sdp"`
					Candidate *webrtc.ICECandidateInit   `json:"candidate"`
				}
			}
			if ws.ReadJSON(&m) != nil {
				return
			}
			if m.Data.SDP != nil {
				b.signaled.Store(true)
				pc.SetRemoteDescription(*m.Data.SDP)
			} else if m.Data.Candidate != nil {
				b.signaled.Store(true)
				pc.AddICECandidate(*m.Data.Candidate)
			}
		}
	}()
	return b
}

// For TestSenderChecksRealCertificate: the test browser's certificate, and
// a change to the offer as a server in the middle might make it.
var (
	offerCert  *webrtc.Certificate
	mungeOffer func(sdp string) string
)

func (b *browser) next(want string) pageMsg {
	b.t.Helper()
	select {
	case m := <-b.msgs:
		if m.Type != want {
			b.t.Fatalf("got %q (%s), want %q", m.Type, m.Error, want)
		}
		return m
	case <-time.After(15 * time.Second):
		b.t.Fatalf("no %q from the sender", want)
	}
	return pageMsg{}
}

func (b *browser) send(v string) { b.dc.SendText(v) }

// statuses collects each visitor's last status.
type statuses struct {
	mu   sync.Mutex
	last map[string]Visitor
	ch   chan Visitor
}

func newStatuses() *statuses {
	return &statuses{last: map[string]Visitor{}, ch: make(chan Visitor, 1024)}
}

func (s *statuses) update(v Visitor) {
	s.mu.Lock()
	s.last[v.Session] = v
	s.mu.Unlock()
	select {
	case s.ch <- v:
	default:
	}
}

func (s *statuses) waitFor(t *testing.T, st Status) Visitor {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		select {
		case v := <-s.ch:
			if v.Status == st {
				return v
			}
		case <-deadline:
			t.Fatalf("no visitor reached %q", st)
		}
	}
}

func share(t *testing.T, files []transfer.Source, limit Limit) (*Share, *statuses, string, string) {
	t.Helper()
	c, wsURL := startServer(t)
	st := newStatuses()
	s, err := Start(context.Background(), c, files, Options{Limit: limit, SenderName: "Test PC", OnUpdate: st.update})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	_, secret, _ := strings.Cut(s.URL, "#")
	return s, st, wsURL, secret
}

func TestDownload(t *testing.T) {
	data := make([]byte, 3<<20+123)
	rand.Read(data)
	s, st, wsURL, secret := share(t, []transfer.Source{memFile("a.bin", data)}, FirstOnly)

	b := openLink(t, wsURL, s.ID, secret)
	b.next("auth")
	hello := b.next("hello")
	if hello.From != "Test PC" || len(hello.Files) != 1 || hello.Files[0].Size != int64(len(data)) || hello.Name == "" {
		t.Fatalf("hello: %+v", hello)
	}
	b.send(`{"type":"get","index":0}`)
	h := sha256.New()
	var got int
	end := make(chan pageMsg, 1)
	go func() { end <- b.next("end") }()
	for got < len(data) {
		select {
		case c := <-b.chunks:
			h.Write(c)
			got += len(c)
		case <-time.After(15 * time.Second):
			t.Fatalf("stalled at %d of %d bytes", got, len(data))
		}
	}
	m := <-end
	want := sha256.Sum256(data)
	if m.SHA256 != hex.EncodeToString(want[:]) || !bytes.Equal(h.Sum(nil), want[:]) {
		t.Fatal("hash mismatch")
	}
	b.send(`{"type":"done"}`)
	st.waitFor(t, Done)

	// First person only: the next one is turned away, before connecting.
	b2 := openLink(t, wsURL, s.ID, secret)
	if m := b2.next("error"); m.Error != "used" {
		t.Fatalf("second visitor: %+v", m)
	}
	st.waitFor(t, Refused)
}

// Someone who knows the link ID but not its secret is turned away before
// anything starts: no STUN/TURN servers, no WebRTC, so nothing about the
// sender (not even its addresses) reaches them, and the sender's app
// doesn't list them.
func TestWrongKey(t *testing.T) {
	s, st, wsURL, _ := share(t, []transfer.Source{memFile("a.bin", []byte("hello"))}, Unlimited)
	b := openLink(t, wsURL, s.ID, base64.RawURLEncoding.EncodeToString(make([]byte, 16)))
	if m := b.next("error"); m.Error != "auth" {
		t.Fatalf("got %+v", m)
	}
	if b.pc != nil || b.signaled.Load() {
		t.Fatal("a page without the key got as far as WebRTC")
	}
	time.Sleep(200 * time.Millisecond)
	st.mu.Lock()
	n := len(st.last)
	st.mu.Unlock()
	if n != 0 {
		t.Fatalf("the sender listed %d visitors without the key", n)
	}
}

// A page that opens the link and never proves the secret is dropped.
func TestSilentVisitorDropped(t *testing.T) {
	old := authTimeout
	authTimeout = 300 * time.Millisecond
	defer func() { authTimeout = old }()
	s, _, wsURL, _ := share(t, []transfer.Source{memFile("a.bin", []byte("hello"))}, Unlimited)
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	ws.WriteJSON(map[string]string{"type": "open", "link": s.ID})
	ws.SetReadDeadline(time.Now().Add(15 * time.Second))
	for {
		var m struct{ Type, Error string }
		if err := ws.ReadJSON(&m); err != nil {
			t.Fatalf("never dropped: %v", err)
		}
		if m.Type == "error" {
			if m.Error != "auth" {
				t.Fatalf("dropped with %q", m.Error)
			}
			return
		}
	}
}

// Nobody but the holder of a link's key can host its ID: not with another
// key, and not by replaying the real host's request while the link is up.
func TestOnlyOwnerHostsLink(t *testing.T) {
	c, _ := startServer(t)
	s, err := Start(context.Background(), c, []transfer.Source{memFile("a.bin", []byte("x"))}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	host := func(m rendezvous.Msg) string {
		nc, err := net.Dial("tcp", c.Addr)
		if err != nil {
			t.Fatal(err)
		}
		defer nc.Close()
		conn := rendezvous.NewConn(nc)
		if err := conn.Send(m); err != nil {
			t.Fatal(err)
		}
		reply, err := conn.RecvMsg()
		if err != nil {
			return err.Error()
		}
		return reply.Type
	}
	pub, key, _ := ed25519.GenerateKey(nil)
	now := time.Now().Unix()
	// Another key, claiming the link's ID.
	if got := host(rendezvous.Msg{Type: rendezvous.TypeLinkHost, Link: s.ID, PublicKey: pub, Time: now, Sig: ed25519.Sign(key, rendezvous.LinkHostPayload(s.ID, now))}); got == rendezvous.TypeLinkHosted {
		t.Fatal("hosted someone else's link with another key")
	}
	// The real key's ID, without a valid signature.
	own := s.key.Public().(ed25519.PublicKey)
	if got := host(rendezvous.Msg{Type: rendezvous.TypeLinkHost, Link: s.ID, PublicKey: own, Time: now, Sig: ed25519.Sign(key, rendezvous.LinkHostPayload(s.ID, now))}); got == rendezvous.TypeLinkHosted {
		t.Fatal("hosted the link without its key's signature")
	}
	// A correct request (as if replayed) while the link is up.
	if got := host(rendezvous.Msg{Type: rendezvous.TypeLinkHost, Link: s.ID, PublicKey: own, Time: now, Sig: ed25519.Sign(s.key, rendezvous.LinkHostPayload(s.ID, now))}); got == rendezvous.TypeLinkHosted {
		t.Fatal("took over a link that is up")
	}
}

func TestReceiverCancelsMidFile(t *testing.T) {
	data := make([]byte, 64<<20) // big enough to still be sending when cancelled
	s, st, wsURL, secret := share(t, []transfer.Source{memFile("big.bin", data)}, Unlimited)
	b := openLink(t, wsURL, s.ID, secret)
	b.next("auth")
	b.next("hello")
	b.send(`{"type":"get","index":0}`)
	<-b.chunks
	b.send(`{"type":"cancel"}`)
	v := st.waitFor(t, Cancelled)
	if v.Error != errCancelled.Error() {
		t.Fatalf("cancelled with %q", v.Error)
	}
	if v.Done >= v.Total {
		t.Fatal("the sender sent everything anyway")
	}
}

// First person only: if the first person leaves before finishing, the link
// isn't spent; the next person gets the files.
func TestFirstOnlyFreedWhenFirstLeaves(t *testing.T) {
	data := []byte("the files")
	s, st, wsURL, secret := share(t, []transfer.Source{memFile("a.txt", data)}, FirstOnly)

	b1 := openLink(t, wsURL, s.ID, secret)
	b1.next("auth")
	b1.next("hello")
	b1.send(`{"type":"cancel"}`)
	st.waitFor(t, Cancelled)

	b2 := openLink(t, wsURL, s.ID, secret)
	b2.next("auth")
	b2.next("hello")
	b2.send(`{"type":"get","index":0}`)
	b2.next("end")
	b2.send(`{"type":"done"}`)
	st.waitFor(t, Done)

	b3 := openLink(t, wsURL, s.ID, secret)
	if m := b3.next("error"); m.Error != "used" {
		t.Fatalf("after a finished download: %+v", m)
	}
}

// The sender binds its check to the certificate the page actually used in
// DTLS, not to what the page's description says. Here the offer lists the
// real certificate first (by sha-1, which DTLS goes by) and a bogus sha-256
// after it: a check that read the description's sha-256 would cover the
// bogus one, which a server in the middle could pick to suit itself.
func TestSenderChecksRealCertificate(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := webrtc.GenerateCertificate(key)
	if err != nil {
		t.Fatal(err)
	}
	pemText, err := cert.PEM()
	if err != nil {
		t.Fatal(err)
	}
	var der []byte
	for rest := []byte(pemText); ; {
		var blk *pem.Block
		if blk, rest = pem.Decode(rest); blk == nil {
			break
		}
		if blk.Type == "CERTIFICATE" {
			der = blk.Bytes
		}
	}
	sum := sha1.Sum(der)
	hexes := make([]string, len(sum))
	for i, b := range sum {
		hexes[i] = fmt.Sprintf("%02X", b)
	}
	bogus := strings.TrimSuffix(strings.Repeat("AB:", 32), ":")
	offerCert = cert
	mungeOffer = func(sdp string) string {
		return regexp.MustCompile(`a=fingerprint:sha-256 [0-9A-Fa-f:]+`).ReplaceAllString(sdp,
			"a=fingerprint:sha-1 "+strings.Join(hexes, ":")+"\r\na=fingerprint:sha-256 "+bogus)
	}
	defer func() { offerCert, mungeOffer = nil, nil }()

	s, _, wsURL, secret := share(t, []transfer.Source{memFile("a.bin", []byte("hello"))}, Unlimited)
	b := openLink(t, wsURL, s.ID, secret)
	b.next("auth")
	b.next("hello")
}

// A file that can't be read when someone asks for it (moved, deleted) ends
// that download with a reason both sides can show, not a lost connection.
func TestUnreadableFile(t *testing.T) {
	gone := transfer.Source{Name: "moved.bin", Size: 10, Open: func() (io.ReadCloser, error) {
		return nil, errors.New("no such file")
	}}
	s, st, wsURL, secret := share(t, []transfer.Source{gone}, Unlimited)
	b := openLink(t, wsURL, s.ID, secret)
	b.next("auth")
	b.next("hello")
	b.send(`{"type":"get","index":0}`)
	if m := b.next("error"); m.Error != "unreadable" {
		t.Fatalf("the page heard %q", m.Error)
	}
	v := st.waitFor(t, Failed)
	var ue *UnreadableError
	if !errors.As(v.Err, &ue) || ue.Name != "moved.bin" {
		t.Fatalf("visitor error %v", v.Err)
	}
}
