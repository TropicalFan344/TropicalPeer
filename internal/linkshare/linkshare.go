// Package linkshare shares files by link: a browser that opens the link
// downloads them straight from this device over a WebRTC DataChannel. The
// rendezvous server relays signaling (and, if needed, DTLS ciphertext
// through TURN); the link's secret, kept in the URL fragment, lets both sides
// check that nobody sits between them.
package linkshare

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v4"

	"TropicalPeer/internal/pairing"
	"TropicalPeer/internal/rendezvous"
	"TropicalPeer/internal/transfer"
)

const (
	chunkSize      = 64 * 1024 // Safari's limit on one message is the tightest
	highWater      = 4 << 20
	lowWater       = 1 << 20
	authTimeout    = 30 * time.Second
	connectTimeout = 45 * time.Second
	authContext    = "tropicalpeer-link-v1"
)

// Limit says how many people may download from a link.
type Limit string

const (
	FirstOnly Limit = "one"
	Unlimited Limit = "unlimited"
)

// Status of one visitor.
type Status string

const (
	Connecting Status = "connecting"
	Waiting    Status = "waiting" // connected; the person hasn't pressed Download yet
	Sending    Status = "sending"
	Done       Status = "done"
	Cancelled  Status = "cancelled"
	Failed     Status = "failed"
	Refused    Status = "refused" // came after the first person on a FirstOnly link
)

// Visitor is one browser that opened the link.
type Visitor struct {
	Session string
	Name    string
	Status  Status
	Done    int64
	Total   int64
	Error   string
}

// Options for Start.
type Options struct {
	Limit      Limit
	SenderName string                        // shown on the page ("from Moon Laptop")
	OnUpdate   func(Visitor)                 // every status change and progress tick
	OnServer   func(connected bool)          // the link lost or regained the server
	Log        func(format string, a ...any) // optional
}

// How long a link keeps trying to get back to the server before it ends.
// Downloads already running don't need the server and carry on meanwhile.
var reconnectWaits = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second, 30 * time.Second}

const reconnectFor = 15 * time.Minute

// Share is an open link.
type Share struct {
	ID  string
	URL string

	files  []transfer.Source
	total  int64
	opts   Options
	secret []byte
	client *rendezvous.Client
	api    *webrtc.API

	hostMu sync.Mutex
	host   *rendezvous.LinkHost

	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	visitors map[string]*visitor
	holder   string // session of the person a FirstOnly link is for ("" = free)
	used     bool   // that person finished downloading: the link is spent
	names    map[string]bool
}

// Start opens a link for files on the rendezvous server c talks to.
func Start(ctx context.Context, c *rendezvous.Client, files []transfer.Source, opts Options) (*Share, error) {
	if len(files) == 0 {
		return nil, errors.New("no files to share")
	}
	if opts.Limit == "" {
		opts.Limit = FirstOnly
	}
	if opts.Log == nil {
		opts.Log = func(string, ...any) {}
	}
	id := rendezvous.NewLinkID()
	host, err := c.HostLink(ctx, id)
	if err != nil {
		return nil, err
	}
	secret := make([]byte, 16)
	rand.Read(secret)

	se := webrtc.SettingEngine{}
	se.SetICETimeouts(10*time.Second, 30*time.Second, 2*time.Second)
	sctxCtx, cancel := context.WithCancel(context.Background())
	s := &Share{
		ID:       id,
		URL:      host.WebURL + id + "#" + base64.RawURLEncoding.EncodeToString(secret),
		files:    files,
		opts:     opts,
		secret:   secret,
		client:   c,
		host:     host,
		api:      webrtc.NewAPI(webrtc.WithSettingEngine(se)),
		ctx:      sctxCtx,
		cancel:   cancel,
		visitors: map[string]*visitor{},
		names:    map[string]bool{},
	}
	for _, f := range files {
		s.total += f.Size
	}
	go s.loop()
	return s, nil
}

// Stop closes the link and every visitor's connection.
func (s *Share) Stop() {
	s.cancel()
	s.currentHost().Close()
}

func (s *Share) currentHost() *rendezvous.LinkHost {
	s.hostMu.Lock()
	defer s.hostMu.Unlock()
	return s.host
}

// Done is closed once the link is closed: Stop, or the server stayed out
// of reach for reconnectFor.
func (s *Share) Done() <-chan struct{} { return s.ctx.Done() }

// SetLimit changes who may still download: switching to FirstOnly after
// someone already has the link turns everyone after them away.
func (s *Share) SetLimit(l Limit) {
	s.mu.Lock()
	s.opts.Limit = l
	s.mu.Unlock()
}

// CancelVisitor stops sending to one visitor.
func (s *Share) CancelVisitor(session string) {
	s.mu.Lock()
	v := s.visitors[session]
	s.mu.Unlock()
	if v != nil {
		v.finish(Cancelled, "")
	}
}

func (s *Share) loop() {
	defer s.cancel()
	for {
		s.serveHost(s.currentHost())
		if s.ctx.Err() != nil {
			return
		}
		s.opts.Log("link: lost the server; reconnecting")
		if s.opts.OnServer != nil {
			s.opts.OnServer(false)
		}
		host := s.reconnect()
		if host == nil {
			return
		}
		s.hostMu.Lock()
		s.host = host
		s.hostMu.Unlock()
		s.opts.Log("link: back on the server")
		if s.opts.OnServer != nil {
			s.opts.OnServer(true)
		}
	}
}

// reconnect hosts the same link again, or gives up (nil).
func (s *Share) reconnect() *rendezvous.LinkHost {
	deadline := time.Now().Add(reconnectFor)
	for i := 0; time.Now().Before(deadline); i++ {
		select {
		case <-s.ctx.Done():
			return nil
		case <-time.After(reconnectWaits[min(i, len(reconnectWaits)-1)]):
		}
		ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
		h, err := s.client.HostLink(ctx, s.ID)
		cancel()
		if err == nil {
			return h
		}
	}
	return nil
}

// serveHost handles what the server sends until its connection ends.
func (s *Share) serveHost(host *rendezvous.LinkHost) {
	for {
		select {
		case <-s.ctx.Done():
			return
		case ev, ok := <-host.Events:
			if !ok {
				return
			}
			switch ev.Type {
			case rendezvous.TypeLinkVisitor:
				s.addVisitor(ev)
			case rendezvous.TypeLinkSignal:
				s.mu.Lock()
				v := s.visitors[ev.Session]
				s.mu.Unlock()
				if v != nil {
					v.signal(ev.Data)
				}
			case rendezvous.TypeLinkDrop:
				// The page closed its signaling socket. That's normal once
				// WebRTC is up; only a visitor still connecting is lost.
				s.mu.Lock()
				v := s.visitors[ev.Session]
				s.mu.Unlock()
				if v != nil && !v.opened.Load() {
					v.finish(Failed, "the page was closed")
				}
			}
		}
	}
}

func (s *Share) newName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for range 20 {
		n := pairing.RandomName()
		if !s.names[n] {
			s.names[n] = true
			return n
		}
	}
	return pairing.RandomName()
}

func (s *Share) addVisitor(ev rendezvous.LinkEvent) {
	cfg := webrtc.Configuration{}
	for _, ice := range ev.ICE {
		cfg.ICEServers = append(cfg.ICEServers, webrtc.ICEServer{URLs: ice.URLs, Username: ice.Username, Credential: ice.Credential})
	}
	pc, err := s.api.NewPeerConnection(cfg)
	if err != nil {
		s.opts.Log("link: peer connection: %v", err)
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	v := &visitor{s: s, pc: pc, ctx: ctx, cancel: cancel, info: Visitor{Session: ev.Session, Name: s.newName(), Status: Connecting, Total: s.total}}
	s.mu.Lock()
	s.visitors[ev.Session] = v
	s.mu.Unlock()
	v.update()
	go v.run()
}

type visitor struct {
	s      *Share
	pc     *webrtc.PeerConnection
	ctx    context.Context
	cancel context.CancelFunc

	opened atomic.Bool // the DataChannel opened; signaling isn't needed any more

	mu       sync.Mutex
	info     Visitor
	lastTick time.Time
	pending  []webrtc.ICECandidateInit // candidates that came before the offer
	haveSDP  bool
}

func (v *visitor) status() Status {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.info.Status
}

func (v *visitor) update() {
	v.mu.Lock()
	info := v.info
	v.mu.Unlock()
	if v.s.opts.OnUpdate != nil {
		v.s.opts.OnUpdate(info)
	}
}

func (v *visitor) setStatus(st Status) {
	v.mu.Lock()
	v.info.Status = st
	v.mu.Unlock()
	v.update()
}

// finish ends the visitor with a final status (the first one wins).
func (v *visitor) finish(st Status, msg string) {
	v.mu.Lock()
	switch v.info.Status {
	case Done, Cancelled, Failed, Refused:
		v.mu.Unlock()
		return
	}
	v.info.Status, v.info.Error = st, msg
	v.mu.Unlock()
	v.s.mu.Lock()
	if v.s.holder == v.info.Session {
		if st == Done {
			v.s.used = true
		} else {
			v.s.holder = ""
		}
	}
	v.s.mu.Unlock()
	v.cancel()
	v.update()
	v.s.opts.Log("link: %s %s %s", v.info.Name, st, msg)
}

// signalMsg is what the page and we exchange through the server.
type signalMsg struct {
	SDP       *webrtc.SessionDescription `json:"sdp,omitempty"`
	Candidate *webrtc.ICECandidateInit   `json:"candidate,omitempty"`
}

func (v *visitor) signal(data json.RawMessage) {
	var m signalMsg
	if json.Unmarshal(data, &m) != nil {
		return
	}
	switch {
	case m.SDP != nil && m.SDP.Type == webrtc.SDPTypeOffer:
		if err := v.pc.SetRemoteDescription(*m.SDP); err != nil {
			v.finish(Failed, "bad offer: "+err.Error())
			return
		}
		answer, err := v.pc.CreateAnswer(nil)
		if err == nil {
			err = v.pc.SetLocalDescription(answer)
		}
		if err != nil {
			v.finish(Failed, err.Error())
			return
		}
		v.s.currentHost().Signal(v.info.Session, signalMsg{SDP: v.pc.LocalDescription()})
		v.mu.Lock()
		v.haveSDP = true
		pending := v.pending
		v.pending = nil
		v.mu.Unlock()
		for _, c := range pending {
			v.pc.AddICECandidate(c)
		}
	case m.Candidate != nil:
		v.mu.Lock()
		if !v.haveSDP {
			v.pending = append(v.pending, *m.Candidate)
			v.mu.Unlock()
			return
		}
		v.mu.Unlock()
		v.pc.AddICECandidate(*m.Candidate)
	}
}

func (v *visitor) run() {
	defer v.pc.Close()
	defer func() { v.s.currentHost().Drop(v.info.Session) }() // the host may change meanwhile
	defer func() {
		v.s.mu.Lock()
		delete(v.s.visitors, v.info.Session)
		v.s.mu.Unlock()
	}()
	v.pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		init := c.ToJSON()
		v.s.currentHost().Signal(v.info.Session, signalMsg{Candidate: &init})
	})
	v.pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		v.s.opts.Log("link: %s connection %s", v.info.Name, st)
		if st == webrtc.PeerConnectionStateFailed {
			v.finish(Failed, "couldn't connect to the browser")
		}
	})
	// The handlers go on in the callback itself: the page sends its key
	// check the moment the channel opens, and a message that arrives before
	// OnMessage is set is lost.
	chans := make(chan *channel, 1)
	v.pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		select {
		case chans <- newChannel(dc):
		default:
		}
	})

	var ch *channel
	select {
	case ch = <-chans:
	case <-time.After(connectTimeout):
		v.finish(Failed, "couldn't connect to the browser")
		return
	case <-v.ctx.Done():
		return
	}
	select {
	case <-ch.open:
	case <-time.After(connectTimeout):
		v.finish(Failed, "couldn't connect to the browser")
		return
	case <-v.ctx.Done():
		return
	}
	v.opened.Store(true)
	go func() { <-v.ctx.Done(); ch.dc.Close() }()

	err := v.serve(ch)
	if errors.Is(err, errCancelled) {
		v.finish(Cancelled, err.Error())
		return
	}
	if err != nil {
		if v.ctx.Err() != nil {
			return // already finished (cancelled or link stopped)
		}
		v.finish(Failed, err.Error())
		return
	}
}

// pageMsg is a control message on the DataChannel.
type pageMsg struct {
	Type   string     `json:"type"` // auth, hello, get, end, cancel, error
	MAC    string     `json:"mac,omitempty"`
	Name   string     `json:"name,omitempty"`
	From   string     `json:"from,omitempty"`
	Files  []fileInfo `json:"files,omitempty"`
	Total  int64      `json:"total,omitempty"`
	Index  int        `json:"index"`
	SHA256 string     `json:"sha256,omitempty"`
	Error  string     `json:"error,omitempty"`
}

type fileInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

func (v *visitor) serve(ch *channel) error {
	// Both sides prove they know the link's secret, bound to the DTLS
	// fingerprints each actually sees, so a relay in the middle fails.
	localFP, err1 := fingerprint(v.pc.LocalDescription())
	remoteFP, err2 := fingerprint(v.pc.RemoteDescription())
	if err1 != nil || err2 != nil {
		return errors.New("no DTLS fingerprint in the session description")
	}
	m, err := ch.recv(v.ctx, authTimeout)
	if err != nil {
		return err
	}
	want := v.s.mac("browser", v.info.Session, localFP, remoteFP)
	got, _ := base64.RawURLEncoding.DecodeString(m.MAC)
	if m.Type != "auth" || !hmac.Equal(got, want) {
		ch.sendJSON(pageMsg{Type: "error", Error: "auth"})
		ch.flush()
		return errors.New("the browser doesn't have this link's key")
	}
	if err := ch.sendJSON(pageMsg{Type: "auth", MAC: base64.RawURLEncoding.EncodeToString(v.s.mac("sender", v.info.Session, localFP, remoteFP))}); err != nil {
		return err
	}

	// First person only: the first to get here holds the link. If they
	// leave or fail before finishing, it's free for the next one.
	v.s.mu.Lock()
	taken := v.s.opts.Limit == FirstOnly && (v.s.used || v.s.holder != "")
	if !taken && v.s.holder == "" {
		v.s.holder = v.info.Session
	}
	v.s.mu.Unlock()
	{
		if taken {
			ch.sendJSON(pageMsg{Type: "error", Error: "used"})
			ch.flush()
			v.finish(Refused, "")
			return nil
		}
	}

	hello := pageMsg{Type: "hello", Name: v.info.Name, From: v.s.opts.SenderName, Total: v.s.total}
	for _, f := range v.s.files {
		hello.Files = append(hello.Files, fileInfo{Name: f.Name, Size: f.Size})
	}
	if err := ch.sendJSON(hello); err != nil {
		return err
	}
	v.setStatus(Waiting)

	sent := map[int]bool{}
	for {
		m, err := ch.recv(v.ctx, 0)
		if err != nil {
			if errors.Is(err, errClosed) {
				if len(sent) == len(v.s.files) {
					v.finish(Done, "")
					return nil
				}
				v.finish(Cancelled, "the receiver closed the page")
				return nil
			}
			return err
		}
		switch m.Type {
		case "get":
			if m.Index < 0 || m.Index >= len(v.s.files) {
				return fmt.Errorf("asked for file %d of %d", m.Index, len(v.s.files))
			}
			v.setStatus(Sending)
			if err := v.sendFile(ch, m.Index); err != nil {
				return err
			}
			sent[m.Index] = true
		case "done":
			v.finish(Done, "")
			time.Sleep(500 * time.Millisecond)
			return nil
		case "cancel":
			v.finish(Cancelled, "the receiver cancelled")
			return nil
		}
	}
}

func (v *visitor) sendFile(ch *channel, index int) error {
	src := v.s.files[index]
	f, err := src.Open()
	if err != nil {
		return fmt.Errorf("can't read %s: %v", src.Name, err)
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, chunkSize)
	var n int64
	for n < src.Size {
		k, err := io.ReadFull(f, buf[:min(int64(chunkSize), src.Size-n)])
		if err != nil {
			return fmt.Errorf("can't read %s: %v", src.Name, err)
		}
		h.Write(buf[:k])
		if err := ch.sendBinary(v.ctx, buf[:k]); err != nil {
			return err
		}
		n += int64(k)
		v.progress(int64(k))
	}
	v.update()
	return ch.sendJSON(pageMsg{Type: "end", Index: index, SHA256: hex.EncodeToString(h.Sum(nil))})
}

func (v *visitor) progress(n int64) {
	v.mu.Lock()
	v.info.Done += n
	tick := time.Since(v.lastTick) >= 100*time.Millisecond
	if tick {
		v.lastTick = time.Now()
	}
	v.mu.Unlock()
	if tick {
		v.update()
	}
}

func (s *Share) mac(role, session, senderFP, browserFP string) []byte {
	m := hmac.New(sha256.New, s.secret)
	fmt.Fprintf(m, "%s\n%s\n%s\n%s\n%s\n%s", authContext, role, s.ID, session, senderFP, browserFP)
	return m.Sum(nil)
}

// fingerprint returns the sha-256 DTLS fingerprint in a description,
// upper-case hex with colons.
func fingerprint(d *webrtc.SessionDescription) (string, error) {
	if d == nil {
		return "", errors.New("no description")
	}
	for _, line := range strings.Split(d.SDP, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "a=fingerprint:"); ok {
			alg, fp, ok := strings.Cut(v, " ")
			if ok && strings.EqualFold(alg, "sha-256") {
				return strings.ToUpper(strings.TrimSpace(fp)), nil
			}
		}
	}
	return "", errors.New("no sha-256 fingerprint")
}
