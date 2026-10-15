// Package linkshare shares files by link: a browser that opens the link
// downloads them straight from this device over a WebRTC DataChannel. The
// rendezvous server relays signaling (and, if needed, DTLS ciphertext
// through TURN). Each link has its own Ed25519 key, and its ID is a hash of
// the public key. The URL fragment holds a secret the server never sees:
//
//  1. Over the server, the browser first proves it has the secret ("hello").
//     Only then do we admit it: the server hands out STUN/TURN servers and
//     we start WebRTC, so someone who only knows the link ID learns nothing,
//     not even this device's addresses.
//  2. On the DataChannel the browser proves the secret again, and we sign
//     with the link's key; both are bound to the DTLS fingerprints. People
//     who had the link know the secret but not the key, so none of them can
//     pose as the sender, and a server in the middle fails both checks.
package linkshare

import (
	"context"
	"crypto/ed25519"
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
	connectTimeout = 45 * time.Second
	authContext    = "tropicalpeer-link-v2"
)

// authTimeout is how long a page has to prove it has the link's secret
// (a variable for tests).
var authTimeout = 30 * time.Second

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
	key    ed25519.PrivateKey // the link's own key; ID is derived from it
	client *rendezvous.Client
	api    *webrtc.API

	hostMu sync.Mutex
	host   *rendezvous.LinkHost

	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	visitors map[string]*visitor
	knocking map[string]time.Time // opened the link, haven't proved the secret yet
	holder   string               // session of the person a FirstOnly link is for ("" = free)
	used     bool                 // that person finished downloading: the link is spent
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
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	host, err := c.HostLink(ctx, key)
	if err != nil {
		return nil, err
	}
	id := host.Link
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
		key:      key,
		client:   c,
		host:     host,
		api:      webrtc.NewAPI(webrtc.WithSettingEngine(se)),
		ctx:      sctxCtx,
		cancel:   cancel,
		visitors: map[string]*visitor{},
		knocking: map[string]time.Time{},
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
		if !v.started() {
			// Still waiting to connect: tell the page, and let it go.
			s.currentHost().Drop(session, "cancel")
			s.forget(session)
		}
	}
}

// started reports whether WebRTC with the visitor began (it was admitted).
func (v *visitor) started() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.pc != nil
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
		h, err := s.client.HostLink(ctx, s.key)
		cancel()
		if err == nil {
			return h
		}
	}
	return nil
}

// serveHost handles what the server sends until its connection ends.
func (s *Share) serveHost(host *rendezvous.LinkHost) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-tick.C:
			s.dropSilentKnocks(host)
		case ev, ok := <-host.Events:
			if !ok {
				return
			}
			switch ev.Type {
			case rendezvous.TypeLinkVisitor:
				// Someone opened the link. Nothing more happens until the
				// page proves it has the secret.
				s.mu.Lock()
				s.knocking[ev.Session] = time.Now()
				s.mu.Unlock()
			case rendezvous.TypeLinkSignal:
				s.mu.Lock()
				v := s.visitors[ev.Session]
				_, knocking := s.knocking[ev.Session]
				delete(s.knocking, ev.Session)
				s.mu.Unlock()
				switch {
				case v != nil:
					v.signal(ev.Data)
				case knocking:
					s.hello(host, ev.Session, ev.Data)
				}
			case rendezvous.TypeLinkAdmitted:
				s.mu.Lock()
				v := s.visitors[ev.Session]
				s.mu.Unlock()
				if v != nil {
					v.connect(ev.ICE)
				}
			case rendezvous.TypeLinkDrop:
				// The page closed its signaling socket. That's normal once
				// WebRTC is up; only a visitor still connecting is lost.
				s.mu.Lock()
				v := s.visitors[ev.Session]
				delete(s.knocking, ev.Session)
				s.mu.Unlock()
				if v != nil && !v.opened.Load() {
					v.finish(Failed, "the page was closed")
					if !v.started() {
						s.forget(ev.Session)
					}
				}
			}
		}
	}
}

// dropSilentKnocks turns away pages that opened the link but never proved
// they have its secret.
func (s *Share) dropSilentKnocks(host *rendezvous.LinkHost) {
	var gone []string
	s.mu.Lock()
	for session, at := range s.knocking {
		if time.Since(at) > authTimeout {
			gone = append(gone, session)
			delete(s.knocking, session)
		}
	}
	s.mu.Unlock()
	for _, session := range gone {
		host.Drop(session, "auth")
	}
}

// helloMsg is the page's first message through the server: proof that it
// has the link's secret, before anything else happens.
type helloMsg struct {
	Hello string `json:"hello"`
}

// hello checks a page's proof of the secret; a page that has it becomes a
// visitor, and the server lets it connect.
func (s *Share) hello(host *rendezvous.LinkHost, session string, data json.RawMessage) {
	var m helloMsg
	got, err := []byte(nil), json.Unmarshal(data, &m)
	if err == nil {
		got, err = base64.RawURLEncoding.DecodeString(m.Hello)
	}
	if err != nil || !hmac.Equal(got, s.helloMAC(session)) {
		s.opts.Log("link: a page without this link's key was turned away")
		host.Drop(session, "auth")
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	v := &visitor{s: s, ctx: ctx, cancel: cancel, info: Visitor{Session: session, Name: s.newName(), Status: Connecting, Total: s.total}}
	s.mu.Lock()
	spent := s.opts.Limit == FirstOnly && s.used
	s.visitors[session] = v
	s.mu.Unlock()
	v.update()
	if spent {
		host.Drop(session, "used")
		v.finish(Refused, "")
		s.forget(session)
		return
	}
	if err := host.Admit(session); err != nil {
		v.finish(Failed, "lost the server")
		s.forget(session)
	}
}

func (s *Share) forget(session string) {
	s.mu.Lock()
	delete(s.visitors, session)
	s.mu.Unlock()
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

// connect starts WebRTC with an admitted visitor, through the STUN/TURN
// servers the server gave for its session.
func (v *visitor) connect(servers []rendezvous.ICEServer) {
	cfg := webrtc.Configuration{}
	for _, ice := range servers {
		cfg.ICEServers = append(cfg.ICEServers, webrtc.ICEServer{URLs: ice.URLs, Username: ice.Username, Credential: ice.Credential})
	}
	pc, err := v.s.api.NewPeerConnection(cfg)
	if err != nil {
		v.s.opts.Log("link: peer connection: %v", err)
		v.finish(Failed, "couldn't start a connection")
		return
	}
	v.mu.Lock()
	if v.pc != nil || v.ctx.Err() != nil {
		v.mu.Unlock()
		pc.Close()
		return
	}
	v.pc = pc
	v.mu.Unlock()
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
	v.mu.Lock()
	started := v.pc != nil
	v.mu.Unlock()
	if !started {
		return // not admitted yet: the page has no business signaling
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
	defer func() { v.s.currentHost().Drop(v.info.Session, "") }() // the host may change meanwhile
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
	Pub    string     `json:"pub,omitempty"` // auth from us: the link's public key
	Sig    string     `json:"sig,omitempty"` // auth from us: its signature
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
	// The page proves it knows the link's secret and we sign with the link's
	// key, both bound to the DTLS fingerprints each side actually sees, so a
	// relay in the middle fails. Ours is the certificate in our own
	// description; the page's is the certificate DTLS actually got, not what
	// its description says (one with several fingerprints could pass DTLS
	// with one and the check with another).
	localFP, err1 := fingerprint(v.pc.LocalDescription())
	remoteFP, err2 := remoteFingerprint(v.pc)
	if err1 != nil || err2 != nil {
		return errors.New("no DTLS fingerprint to check")
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
	sig := ed25519.Sign(v.s.key, v.s.authPayload("sender", v.info.Session, localFP, remoteFP))
	pub := v.s.key.Public().(ed25519.PublicKey)
	if err := ch.sendJSON(pageMsg{Type: "auth", Pub: base64.RawURLEncoding.EncodeToString(pub), Sig: base64.RawURLEncoding.EncodeToString(sig)}); err != nil {
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

// authPayload is what each side's DataChannel check covers.
func (s *Share) authPayload(role, session, senderFP, browserFP string) []byte {
	return []byte(strings.Join([]string{authContext, role, s.ID, session, senderFP, browserFP}, "\n"))
}

// mac is the page's proof of the secret on the DataChannel.
func (s *Share) mac(role, session, senderFP, browserFP string) []byte {
	m := hmac.New(sha256.New, s.secret)
	m.Write(s.authPayload(role, session, senderFP, browserFP))
	return m.Sum(nil)
}

// helloMAC is the page's proof of the secret through the server, before
// anything else.
func (s *Share) helloMAC(session string) []byte {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte(strings.Join([]string{authContext, "hello", s.ID, session}, "\n")))
	return m.Sum(nil)
}

// remoteFingerprint is the sha-256 fingerprint of the certificate the page
// presented in the DTLS handshake.
func remoteFingerprint(pc *webrtc.PeerConnection) (string, error) {
	sctp := pc.SCTP()
	if sctp == nil {
		return "", errors.New("no SCTP transport")
	}
	der := sctp.Transport().GetRemoteCertificate()
	if len(der) == 0 {
		return "", errors.New("no remote certificate")
	}
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":"), nil
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
