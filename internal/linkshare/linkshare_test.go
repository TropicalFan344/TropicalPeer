package linkshare

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
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

// browser plays the receiver page: it opens the link and, like the page,
// sends its key check the moment the DataChannel opens.
type browser struct {
	t      *testing.T
	pc     *webrtc.PeerConnection
	dc     *webrtc.DataChannel
	msgs   chan pageMsg
	chunks chan []byte
	closed chan struct{}
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
	}
	if err := ws.ReadJSON(&opened); err != nil || opened.Type != "opened" {
		t.Fatalf("open: %v %+v", err, opened)
	}
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	b := &browser{t: t, pc: pc, msgs: make(chan pageMsg, 64), chunks: make(chan []byte, 4096), closed: make(chan struct{})}
	b.dc, _ = pc.CreateDataChannel("files", nil)
	secret, _ := base64.RawURLEncoding.DecodeString(secretB64)
	mac := func(role string) []byte {
		senderFP, _ := fingerprint(pc.RemoteDescription())
		browserFP, _ := fingerprint(pc.LocalDescription())
		m := hmac.New(sha256.New, secret)
		fmt.Fprintf(m, "%s\n%s\n%s\n%s\n%s\n%s", authContext, role, link, opened.Session, senderFP, browserFP)
		return m.Sum(nil)
	}
	b.dc.OnOpen(func() {
		b.dc.SendText(fmt.Sprintf(`{"type":"auth","mac":%q}`, base64.RawURLEncoding.EncodeToString(mac("browser"))))
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
			got, _ := base64.RawURLEncoding.DecodeString(pm.MAC)
			if !hmac.Equal(got, mac("sender")) {
				t.Errorf("the sender's key check doesn't match")
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
	sendWS(map[string]any{"type": "signal", "data": map[string]any{"sdp": pc.LocalDescription()}})
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
				pc.SetRemoteDescription(*m.Data.SDP)
			} else if m.Data.Candidate != nil {
				pc.AddICECandidate(*m.Data.Candidate)
			}
		}
	}()
	return b
}

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

	// First person only: the next one is turned away.
	b2 := openLink(t, wsURL, s.ID, secret)
	b2.next("auth")
	if m := b2.next("error"); m.Error != "used" {
		t.Fatalf("second visitor: %+v", m)
	}
	st.waitFor(t, Refused)
}

func TestWrongKey(t *testing.T) {
	s, st, wsURL, _ := share(t, []transfer.Source{memFile("a.bin", []byte("hello"))}, Unlimited)
	b := openLink(t, wsURL, s.ID, base64.RawURLEncoding.EncodeToString(make([]byte, 16)))
	if m := b.next("error"); m.Error != "auth" {
		t.Fatalf("got %+v", m)
	}
	st.waitFor(t, Failed)
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
	b3.next("auth")
	if m := b3.next("error"); m.Error != "used" {
		t.Fatalf("after a finished download: %+v", m)
	}
}
