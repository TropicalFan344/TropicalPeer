package server

import (
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"TropicalPeer/internal/rendezvous"
)

// Share by link. A sender's app "hosts" a link on its own TCP connection;
// browsers that open the link reach /ws, and the server relays WebRTC
// signaling between the two. It never sees the link's secret (it lives in
// the URL fragment) and the two sides authenticate each other with it, so
// relaying signaling gives the server nothing to tamper with unnoticed.

const (
	maxVisitorsPerLink = 50
	maxSignalSize      = 64 * 1024
	wsPingEvery        = 25 * time.Second
	wsIdleTimeout      = 60 * time.Second
	opensPerMinute     = 30 // per IP
)

type linkHost struct {
	link string
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
	session string
	host    *linkHost
	ws      *websocket.Conn
	wmu     sync.Mutex
}

func (v *visitor) send(m wsMsg) error {
	v.wmu.Lock()
	defer v.wmu.Unlock()
	v.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return v.ws.WriteJSON(m)
}

// wsMsg is the browser side of the protocol.
type wsMsg struct {
	Type    string                 `json:"type"` // open, opened, signal, ended, error
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
	h := &linkHost{link: m.Link, conn: conn, nc: nc, visitors: map[string]*visitor{}}
	s.mu.Lock()
	if s.links == nil {
		s.links = map[string]*linkHost{}
	}
	if _, taken := s.links[m.Link]; taken {
		s.mu.Unlock()
		return errString("link already in use")
	}
	s.links[m.Link] = h
	s.mu.Unlock()
	s.Logger.Printf("link    %s open", m.Link)

	defer func() {
		s.mu.Lock()
		if s.links[m.Link] == h {
			delete(s.links, m.Link)
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
		s.mu.Lock()
		v := h.visitors[req.Session]
		s.mu.Unlock()
		if v == nil {
			continue
		}
		switch req.Type {
		case rendezvous.TypeLinkSignal:
			if len(req.Data) <= maxSignalSize {
				v.send(wsMsg{Type: "signal", Data: req.Data})
			}
		case rendezvous.TypeLinkDrop:
			v.send(wsMsg{Type: "ended"})
			v.ws.Close()
		}
	}
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
	v := &visitor{session: randomHex(16), ws: ws}
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

	ice := cfg.ICE(v.session)
	if err := h.send(rendezvous.Msg{Type: rendezvous.TypeLinkVisitor, Session: v.session, ICE: ice}); err != nil {
		v.send(wsMsg{Type: "error", Error: "gone"})
		return
	}
	if err := v.send(wsMsg{Type: "opened", Session: v.session, ICE: ice}); err != nil {
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
