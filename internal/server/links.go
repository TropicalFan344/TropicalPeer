package server

import (
	"crypto/ed25519"
	"encoding/json"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"TropicalPeer/internal/rendezvous"
)

// Share by link. A sender's app "hosts" a link on its own TCP connection,
// proving with the link's key that the link is its own; browsers that open
// the link reach /ws, and the server relays between the two. A browser
// first proves to the sender that it has the link's secret (it lives in the
// URL fragment, which the server never sees); only then does the sender
// admit it, and only an admitted session gets STUN/TURN servers and WebRTC
// signaling. On the DataChannel the browser proves the secret again and the
// sender signs with the link's key, both bound to the DTLS fingerprints, so
// relaying gives the server nothing to tamper with unnoticed.

const (
	maxVisitorsPerLink = 50
	maxSignalSize      = 64 * 1024
	wsPingEvery        = 25 * time.Second
	wsIdleTimeout      = 60 * time.Second
	opensPerMinute     = 30 // per IP

	// An admitted session's TURN credentials work while its link is hosted
	// (or for linkGrace after the host went away, while it reconnects),
	// until the sender drops the session, and for turnSessionMax at most.
	linkGrace      = 15 * time.Minute
	turnSessionMax = 12 * time.Hour
)

// Error codes a sender may pass to a page when dropping it.
var dropCodes = []string{"auth", "used", "busy", "gone", "cancel"}

// turnSession is a browser session the sender admitted: its TURN
// credentials work, and its relay may reach the two ends' own addresses.
type turnSession struct {
	link    string
	ips     []net.IP // the sender's and the browser's addresses as this server sees them
	created time.Time
	dropped bool
}

type linkHost struct {
	link string
	ips  []net.IP // where the sender connects from (see ownAddrs)
	wmu  sync.Mutex
	conn *rendezvous.Conn
	nc   net.Conn

	visitors map[string]*visitor // under Server.mu
}

func (h *linkHost) send(m rendezvous.Msg) error {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	return h.conn.Send(m)
}

type visitor struct {
	session  string
	ips      []net.IP // where the browser connects from (see ownAddrs)
	host     *linkHost
	ws       *websocket.Conn
	wmu      sync.Mutex
	admitted bool // under Server.mu
}

func (v *visitor) send(m wsMsg) error {
	v.wmu.Lock()
	defer v.wmu.Unlock()
	v.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return v.ws.WriteJSON(m)
}

// wsMsg is the browser side of the protocol.
type wsMsg struct {
	Type    string                 `json:"type"` // open, opened, signal, admitted, ended, error
	Link    string                 `json:"link,omitempty"`
	Session string                 `json:"session,omitempty"`
	ICE     []rendezvous.ICEServer `json:"ice,omitempty"`
	Data    json.RawMessage        `json:"data,omitempty"`
	Error   string                 `json:"error,omitempty"`
}

// Links configures share by link. Without it the server doesn't take
// link_host and has no /ws.
type Links struct {
	// WebURL is where links point, ending in "/" (e.g. https://host/r/).
	WebURL string
	// ICE returns the STUN/TURN servers for one browser session.
	ICE func(session string) []rendezvous.ICEServer
	// Origins are the page origins allowed to open /ws besides WebURL's.
	Origins []string
}

func (s *Server) handleLinkHost(nc net.Conn, conn *rendezvous.Conn, m rendezvous.Msg) error {
	cfg := s.Links
	if cfg == nil {
		return errString("this server doesn't support links")
	}
	if !rendezvous.ValidLinkID(m.Link) {
		return errString("invalid link")
	}
	// Only the holder of the link's key may host it: the ID is a hash of
	// the key, and the request is signed with it.
	if len(m.PublicKey) != ed25519.PublicKeySize || rendezvous.LinkIDFor(m.PublicKey) != m.Link {
		return errString("link ID does not match its key")
	}
	if d := time.Since(time.Unix(m.Time, 0)); d > maxClockSkew || d < -maxClockSkew {
		return errString("link timestamp too far off; check the device clock")
	}
	if !ed25519.Verify(m.PublicKey, rendezvous.LinkHostPayload(m.Link, m.Time), m.Sig) {
		return errString("bad link signature")
	}
	h := &linkHost{link: m.Link, ips: ownAddrs(remoteIP(nc.RemoteAddr())), conn: conn, nc: nc, visitors: map[string]*visitor{}}
	s.mu.Lock()
	if s.links == nil {
		s.links = map[string]*linkHost{}
	}
	if _, taken := s.links[m.Link]; taken {
		s.mu.Unlock()
		return errString("link already in use")
	}
	s.links[m.Link] = h
	delete(s.linkLost, m.Link)
	s.mu.Unlock()
	s.Logger.Printf("link    %s open", m.Link)

	defer func() {
		s.mu.Lock()
		if s.links[m.Link] == h {
			delete(s.links, m.Link)
			if s.linkLost == nil {
				s.linkLost = map[string]time.Time{}
			}
			s.linkLost[m.Link] = time.Now()
		}
		vs := h.visitors
		h.visitors = map[string]*visitor{}
		s.mu.Unlock()
		for _, v := range vs {
			v.send(wsMsg{Type: "ended"})
			v.ws.Close()
		}
		s.Logger.Printf("link    %s closed", m.Link)
	}()

	if err := h.send(rendezvous.Msg{Type: rendezvous.TypeLinkHosted, Link: m.Link, WebURL: cfg.WebURL}); err != nil {
		return nil
	}
	for {
		var req rendezvous.Msg
		if err := conn.Recv(&req); err != nil {
			return nil
		}
		if req.Type == rendezvous.TypeLinkDrop {
			s.dropTURN(h.link, req.Session) // the page may have left /ws long ago
		}
		s.mu.Lock()
		v := h.visitors[req.Session]
		admit := v != nil && req.Type == rendezvous.TypeLinkAdmit && !v.admitted
		if admit {
			v.admitted = true
			s.addTURN(req.Session, &turnSession{link: h.link, ips: append(slices.Clone(h.ips), v.ips...), created: time.Now()})
		}
		s.mu.Unlock()
		if v == nil {
			continue
		}
		switch req.Type {
		case rendezvous.TypeLinkSignal:
			if len(req.Data) <= maxSignalSize {
				v.send(wsMsg{Type: "signal", Data: req.Data})
			}
		case rendezvous.TypeLinkAdmit:
			if admit {
				ice := cfg.ICE(v.session)
				if h.send(rendezvous.Msg{Type: rendezvous.TypeLinkAdmitted, Session: v.session, ICE: ice}) != nil {
					return nil
				}
				v.send(wsMsg{Type: "admitted", ICE: ice})
			}
		case rendezvous.TypeLinkDrop:
			if slices.Contains(dropCodes, req.Error) {
				v.send(wsMsg{Type: "error", Error: req.Error})
			} else {
				v.send(wsMsg{Type: "ended"})
			}
			v.ws.Close()
		}
	}
}

// addTURN records an admitted session (under s.mu), clearing out ones
// that no longer count.
func (s *Server) addTURN(session string, ts *turnSession) {
	if s.turn == nil {
		s.turn = map[string]*turnSession{}
	}
	for id, old := range s.turn {
		if !s.turnValid(old) {
			delete(s.turn, id)
		}
	}
	for link, at := range s.linkLost {
		if time.Since(at) > linkGrace {
			delete(s.linkLost, link)
		}
	}
	s.turn[session] = ts
}

// dropTURN ends a session's TURN credentials: its sender is done with it.
func (s *Server) dropTURN(link, session string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ts := s.turn[session]; ts != nil && ts.link == link {
		ts.dropped = true
	}
}

// turnValid says whether a session's credentials still work (under s.mu).
func (s *Server) turnValid(ts *turnSession) bool {
	if ts.dropped || time.Since(ts.created) > turnSessionMax {
		return false
	}
	if s.links[ts.link] != nil {
		return true
	}
	lost, ok := s.linkLost[ts.link]
	return ok && time.Since(lost) < linkGrace
}

// TURNSession is for the TURN relay: whether session's credentials still
// work, and the private addresses its relay may reach (the sender's and the
// browser's own, when they're on the server's network).
func (s *Server) TURNSession(session string) ([]net.IP, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ts := s.turn[session]
	if ts == nil || !s.turnValid(ts) {
		return nil, false
	}
	return ts.ips, true
}

// ownAddrs is where a peer that connected from ip can be reached: ip
// itself, or, for a program on this very machine (the sender's app next to
// the server), this machine's own network addresses.
func ownAddrs(ip net.IP) []net.IP {
	if ip == nil || !ip.IsLoopback() {
		return []net.IP{ip}
	}
	var out []net.IP
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() {
			out = append(out, n.IP)
		}
	}
	return out
}

func remoteIP(a net.Addr) net.IP {
	host, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return nil
	}
	return net.ParseIP(host)
}

type errString string

func (e errString) Error() string { return string(e) }

// ServeLinkWS handles a browser's signaling WebSocket.
func (s *Server) ServeLinkWS(w http.ResponseWriter, r *http.Request) {
	cfg := s.Links
	if cfg == nil {
		http.NotFound(w, r)
		return
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !s.allowOpen(ip) {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return s.originAllowed(r) }}
	ws, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	ws.SetReadLimit(maxSignalSize)
	ws.SetReadDeadline(time.Now().Add(wsIdleTimeout))
	ws.SetPongHandler(func(string) error {
		ws.SetReadDeadline(time.Now().Add(wsIdleTimeout))
		return nil
	})

	var open wsMsg
	if err := ws.ReadJSON(&open); err != nil || open.Type != "open" {
		return
	}
	v := &visitor{session: randomHex(16), ips: ownAddrs(net.ParseIP(ip)), ws: ws}
	s.mu.Lock()
	h := s.links[open.Link]
	full := h != nil && len(h.visitors) >= maxVisitorsPerLink
	if h != nil && !full {
		v.host = h
		h.visitors[v.session] = v
	}
	s.mu.Unlock()
	switch {
	case h == nil:
		v.send(wsMsg{Type: "error", Error: "gone"})
		return
	case full:
		v.send(wsMsg{Type: "error", Error: "busy"})
		return
	}
	defer func() {
		s.mu.Lock()
		delete(h.visitors, v.session)
		s.mu.Unlock()
		h.send(rendezvous.Msg{Type: rendezvous.TypeLinkDrop, Session: v.session})
	}()

	// No STUN/TURN servers yet: the page gets them once the sender has
	// checked its key (link_admit).
	if err := h.send(rendezvous.Msg{Type: rendezvous.TypeLinkVisitor, Session: v.session}); err != nil {
		v.send(wsMsg{Type: "error", Error: "gone"})
		return
	}
	if err := v.send(wsMsg{Type: "opened", Session: v.session}); err != nil {
		return
	}

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		t := time.NewTicker(wsPingEvery)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				v.wmu.Lock()
				err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
				v.wmu.Unlock()
				if err != nil {
					return
				}
			}
		}
	}()

	for {
		var m wsMsg
		if err := ws.ReadJSON(&m); err != nil {
			return
		}
		ws.SetReadDeadline(time.Now().Add(wsIdleTimeout))
		if m.Type == "signal" && len(m.Data) > 0 {
			if h.send(rendezvous.Msg{Type: rendezvous.TypeLinkSignal, Session: v.session, Data: m.Data}) != nil {
				return
			}
		}
	}
}

func (s *Server) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // not a browser
	}
	if origin == "http://"+r.Host || origin == "https://"+r.Host {
		return true
	}
	for _, o := range s.Links.Origins {
		if origin == o {
			return true
		}
	}
	return false
}

// allowOpen limits how often one address may open links.
func (s *Server) allowOpen(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.opens == nil || now.Sub(s.opensReset) > time.Minute {
		s.opens = map[string]int{}
		s.opensReset = now
	}
	s.opens[ip]++
	return s.opens[ip] <= opensPerMinute
}
