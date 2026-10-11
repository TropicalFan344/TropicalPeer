// Package appcore is TropicalPeer's app layer, shared by the desktop app
// (Wails, package main) and the Android app (package mobile): transfers with
// progress and speed, incoming offers waiting for an answer, pairing, settings
// and network status, with errors worded for people.
//
// The shell around it supplies the UI. Core pushes updates through an Emit
// function as events named "<area>:<what>" (the frontend's EventMap in
// frontend/src/lib/api.ts) and is called through its exported methods.
package appcore

import (
	"context"
	"errors"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"TropicalPeer/internal/node"
	"TropicalPeer/internal/peers"
	"TropicalPeer/locales"
)

// Core is the app layer for one device.
type Core struct {
	ctx     context.Context
	dataDir string

	// The running node. Restart swaps it, so read it through n().
	nodeMu   sync.RWMutex
	node     *node.Node
	stopNode context.CancelFunc
	nodeDone chan struct{} // closed when the running node's Run returns
	restarts sync.Mutex    // one restart at a time

	mu           sync.RWMutex
	status       ServerStatus
	inviteCancel context.CancelFunc
	transfers    map[string]*transferState
	incoming     map[string]*transferState // by incomingKey, while running
	stopped      map[string]*transferState // by incomingKey: interrupted, may resume in the same row
	upd          updates
	offers       map[string]*pendingOffer

	// emitFn pushes an event to the UI. data is nil for events without a payload.
	emitFn Emit

	// lang is the UI's language, for the messages this layer writes.
	lang atomic.Pointer[string]
}

// SetLanguage sets the language of messages and errors (a locales code such
// as "zh-TW"). The UI calls it at start and whenever the user switches.
func (c *Core) SetLanguage(lang string) {
	if locales.Supported(lang) {
		c.lang.Store(&lang)
	}
}

// T is the message for key in the UI's language; params are name, value
// pairs filling its {placeholders}.
func (c *Core) T(key string, params ...any) string {
	lang := locales.Fallback
	if p := c.lang.Load(); p != nil {
		lang = *p
	}
	m := make(map[string]any, len(params)/2)
	for i := 0; i+1 < len(params); i += 2 {
		if k, ok := params[i].(string); ok {
			m[k] = params[i+1]
		}
	}
	return locales.T(lang, key, m)
}

// errT is T as an error.
func (c *Core) errT(key string, params ...any) error { return errors.New(c.T(key, params...)) }

// Emit delivers an event to the UI shell.
type Emit func(name string, data any)

type DeviceInfo struct {
	Name string `json:"name"`
	ID   string `json:"id"`
	Port int    `json:"port"`
	// Version is this app's version (see Version).
	Version string `json:"version"`
}

type ServerStatus struct {
	Address string `json:"address"`
	Online  bool   `json:"online"`
	Error   string `json:"error"`
}

// SettingsDTO is the editable configuration, split into fields for the form.
type SettingsDTO struct {
	ServerHost  string `json:"serverHost"`
	ServerPort  int    `json:"serverPort"`
	ListenPort  int    `json:"listenPort"`
	DownloadDir string `json:"downloadDir"`
	PortMapping bool   `json:"portMapping"` // ask the router to forward our port
}

// NetworkDTO describes how reachable this device is, in plain terms.
type NetworkDTO struct {
	ServerAddr   string `json:"serverAddr"`
	ServerOnline bool   `json:"serverOnline"`
	ServerError  string `json:"serverError"`
	RTTMs        int    `json:"rttMs"`     // server round trip (0 = unknown)
	Relay        bool   `json:"relay"`     // the server relays when direct paths fail
	Detection    bool   `json:"detection"` // the server supports router detection
	PublicAddr   string `json:"publicAddr"`
	// Router: "unknown" | "open" (lenient, not symmetric) | "restricted"
	// (port-restricted) | "strict" (symmetric).
	Router      string `json:"router"`
	PortMapping bool   `json:"portMapping"` // forwarding is turned on in settings
	Forwarded   string `json:"forwarded"`   // "UPnP 203.0.113.7:7778" when the router forwards our port
	MappingErr  string `json:"mappingErr"`  // why forwarding isn't active
	Checking    bool   `json:"checking"`
}

type PeerDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Policy      string `json:"policy"` // "ask" | "auto" | "block"
	MaxAutoSize int64  `json:"maxAutoSize"`
	SaveDir     string `json:"saveDir"`
	AddedAt     int64  `json:"addedAt"` // unix milliseconds
	// RemovedUs: the peer removed this device; sending fails until the two
	// pair again.
	RemovedUs bool `json:"removedUs"`
}

// Invite describes a pairing code being shown to the user.
type Invite struct {
	Code      string `json:"code"`
	ExpiresAt int64  `json:"expiresAt"` // unix milliseconds
}

// inviteTTL mirrors the rendezvous server's nameplate lifetime.
const inviteTTL = 5 * time.Minute

// New prepares the app layer for the device whose data lives in dataDir.
// Nothing runs until Start.
func New(dataDir string, emit Emit) *Core {
	return &Core{dataDir: dataDir, transfers: map[string]*transferState{},
		incoming: map[string]*transferState{}, stopped: map[string]*transferState{}, offers: map[string]*pendingOffer{}, emitFn: emit}
}

func (c *Core) emit(name string, data ...any) {
	var payload any
	if len(data) > 0 {
		payload = data[0]
	}
	c.emitFn(name, payload)
}

// Node is the device's node, for shell-specific setup (nil before Start).
func (c *Core) Node() *node.Node { return c.n() }

func (c *Core) n() *node.Node {
	c.nodeMu.RLock()
	defer c.nodeMu.RUnlock()
	return c.node
}

func (c *Core) setNode(n *node.Node) {
	c.nodeMu.Lock()
	c.node = n
	c.nodeMu.Unlock()
}

// Start loads this device and connects in the background until ctx ends.
// An error means the app can't run at all (e.g. the data folder is unusable).
func (c *Core) Start(ctx context.Context) error {
	c.ctx = ctx
	openLog(c.dataDir)
	if err := c.startNode(); err != nil {
		return err
	}
	c.startUpdates()
	return nil
}

// logFile is a small diary of how transfers ended, in the profile folder,
// so a problem on someone's machine can be explained afterwards.
const (
	logFile    = "tropicalpeer.log"
	logMaxSize = 1 << 20 // then it starts over (the previous one is kept as .old)
)

func openLog(dir string) {
	path := filepath.Join(dir, logFile)
	if st, err := os.Stat(path); err == nil && st.Size() > logMaxSize {
		os.Rename(path, path+".old")
	}
	log.SetOutput(appendFile(path))
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.Printf("TropicalPeer %s starting", Version)
}

// appendFile writes each log line by opening the file, appending and
// closing it again: lines are rare, and the file is never held open (so the
// profile folder can always be moved or deleted).
type appendFile string

func (p appendFile) Write(b []byte) (int, error) {
	f, err := os.OpenFile(string(p), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return len(b), nil // logging must never break the app
	}
	defer f.Close()
	return f.Write(b)
}

// startNode loads the node from disk (settings included) and runs it until
// ctx ends or Restart stops it.
func (c *Core) startNode() error {
	ctx := c.ctx
	n, err := node.New(c.dataDir, node.Events{
		OnServerStatus:  c.onServerStatus,
		OnOffer:         c.onOffer,
		OnAccepted:      c.onAccepted,
		OnProgress:      c.onIncomingProgress,
		OnReceived:      c.onReceived,
		OnNetwork:       func(node.NetworkInfo) { c.emit("network:changed", c.GetNetwork()) },
		OnPathChanged:   c.onPathChanged,
		OnResumeRequest: c.onResumeRequest,
		OnCancelNotice:  c.onCancelNotice,
		OnRemovedUs:     c.onRemovedUs,
	})
	if err != nil {
		return err
	}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	c.nodeMu.Lock()
	c.node, c.stopNode, c.nodeDone = n, stop, done
	c.nodeMu.Unlock()
	c.mu.Lock()
	c.status = ServerStatus{Address: n.Settings().ServerAddr}
	c.mu.Unlock()

	go func() {
		defer close(done)
		if err := n.Run(runCtx); err != nil && runCtx.Err() == nil {
			msg := err.Error()
			if errors.Is(err, node.ErrProfileInUse) {
				msg = c.T("app.profileInUse")
			}
			c.emit("node:error", msg)
		}
	}()
	go func() {
		select {
		case <-n.Ready():
			c.emit("device:changed", c.GetDevice())
			c.runDevHooks()
		case <-runCtx.Done():
		}
	}()
	return nil
}

// Stop winds down before the app exits: running transfers stop where they
// are (both sides keep what arrived, to resume later), the router port
// forward is removed, and the node closes its connections, so peers hear
// right away that this device left rather than waiting for silence to time
// out.
func (c *Core) Stop() {
	n := c.n()
	if n == nil {
		return
	}
	c.installOnQuit()
	c.mu.Lock()
	var running []*transferState
	for _, st := range c.transfers {
		if st.dto.active() && st.stop != nil {
			running = append(running, st)
		}
	}
	c.mu.Unlock()
	for _, st := range running {
		st.stop()
	}
	// Let them close their connections (as stopped) before the node goes.
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline) && c.anyActiveSend(); {
		time.Sleep(20 * time.Millisecond)
	}

	cctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	n.StopPortMapping(cctx)

	c.nodeMu.Lock()
	stop, done := c.stopNode, c.nodeDone
	c.nodeMu.Unlock()
	if stop != nil {
		stop()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
}

func (c *Core) anyActiveSend() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, st := range c.transfers {
		if st.dto.active() && st.stop != nil {
			return true
		}
	}
	return false
}

func (c *Core) onServerStatus(online bool, err error) {
	c.mu.Lock()
	c.status = ServerStatus{Address: c.n().Settings().ServerAddr, Online: online}
	if err != nil {
		c.status.Error = c.friendlyServerError(err)
	}
	s := c.status
	c.mu.Unlock()
	c.emit("server:status", s)
}

// ---- Device & server ----

// GetDevice describes this device.
func (c *Core) GetDevice() DeviceInfo {
	if c.n() == nil {
		return DeviceInfo{}
	}
	return DeviceInfo{Name: c.n().Identity.Name, ID: c.n().Identity.ID(), Port: c.n().Port(), Version: Version}
}

// SetDeviceName changes the name other devices see when pairing.
func (c *Core) SetDeviceName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return c.errT("settings.errNameEmpty")
	}
	if len([]rune(name)) > 64 {
		return c.errT("settings.errNameLong")
	}
	if err := c.n().SetName(name); err != nil {
		return err
	}
	c.emit("device:changed", c.GetDevice())
	return nil
}

// GetServerStatus reports the connection to the rendezvous server.
func (c *Core) GetServerStatus() ServerStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status
}

// ---- Settings ----

func (c *Core) GetSettings() SettingsDTO {
	s := c.n().Settings()
	host, portStr, err := net.SplitHostPort(s.ServerAddr)
	if err != nil {
		host, portStr = s.ServerAddr, ""
	}
	port, _ := strconv.Atoi(portStr)
	return SettingsDTO{ServerHost: host, ServerPort: port, ListenPort: s.Port, DownloadDir: s.DownloadDir, PortMapping: !s.DisablePortMapping}
}

// RecheckNetwork runs the network checks again; results arrive as
// "network:changed" events.
func (c *Core) RecheckNetwork() {
	if c.n() != nil {
		c.n().Recheck()
	}
}

// GetNetwork reports server, router type and port forwarding.
func (c *Core) GetNetwork() NetworkDTO {
	if c.n() == nil {
		return NetworkDTO{Router: "unknown"}
	}
	info := c.n().Network()
	c.mu.RLock()
	status := c.status
	c.mu.RUnlock()
	out := NetworkDTO{
		ServerAddr: c.n().Settings().ServerAddr, ServerOnline: info.ServerOnline, ServerError: status.Error,
		RTTMs: int(info.ServerRTT.Milliseconds()), Relay: info.Relay, Detection: info.Detection,
		PublicAddr: info.PublicAddr, PortMapping: !c.n().Settings().DisablePortMapping,
		Forwarded: info.Mapping, MappingErr: info.MappingErr, Router: "unknown", Checking: info.Checking,
	}
	if out.ServerOnline && out.RTTMs == 0 && info.ServerRTT > 0 {
		out.RTTMs = 1
	}
	switch {
	case !info.NAT.Known:
	case info.NAT.Symmetric:
		out.Router = "strict"
	case info.NAT.Lenient:
		out.Router = "open"
	default:
		out.Router = "restricted"
	}
	c.devAdjustNetwork(&out)
	return out
}

// SaveSettings validates and applies the settings. A changed server address
// reconnects immediately; a changed listen port applies after a restart.
func (c *Core) SaveSettings(in SettingsDTO) error {
	host := strings.TrimSpace(in.ServerHost)
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]") // allow "[::1]"
	if host == "" || strings.ContainsAny(host, " /\\") {
		return c.errT("settings.errHost")
	}
	if in.ServerPort < 1 || in.ServerPort > 65535 {
		return c.errT("settings.errServerPort")
	}
	if in.ListenPort < 0 || in.ListenPort > 65535 {
		return c.errT("settings.errListenPort")
	}
	s := c.n().Settings()
	s.ServerAddr = net.JoinHostPort(host, strconv.Itoa(in.ServerPort))
	s.Port = in.ListenPort
	if d := strings.TrimSpace(in.DownloadDir); d != "" {
		s.DownloadDir = d
	}
	s.DisablePortMapping = !in.PortMapping
	return c.n().UpdateSettings(s)
}

// ---- Peers ----

func (c *Core) ListPeers() []PeerDTO {
	list := c.n().Peers.List()
	out := make([]PeerDTO, 0, len(list))
	for _, p := range list {
		out = append(out, toPeerDTO(p))
	}
	return out
}

// PeerUpdate is the editable part of a peer.
type PeerUpdate struct {
	Name        string `json:"name"`
	Policy      string `json:"policy"`      // "ask" | "auto" | "block"
	MaxAutoSize int64  `json:"maxAutoSize"` // bytes; 0 = no limit
	SaveDir     string `json:"saveDir"`     // "" = use the default download folder
}

// UpdatePeer changes how this device treats a peer. The name is a local
// nickname; the other device doesn't see it.
func (c *Core) UpdatePeer(id string, u PeerUpdate) (PeerDTO, error) {
	name := strings.TrimSpace(u.Name)
	if name == "" {
		return PeerDTO{}, c.errT("settings.errPeerNameEmpty")
	}
	if len([]rune(name)) > 64 {
		return PeerDTO{}, c.errT("settings.errNameLong")
	}
	policy, err := peers.ParsePolicy(u.Policy)
	if err != nil {
		return PeerDTO{}, err
	}
	if u.MaxAutoSize < 0 {
		return PeerDTO{}, c.errT("settings.errLimitNegative")
	}
	saveDir := strings.TrimSpace(u.SaveDir)
	if saveDir != "" && !filepath.IsAbs(saveDir) {
		return PeerDTO{}, c.errT("settings.errFolderPath")
	}

	err = c.n().Peers.Update(id, func(p *peers.Peer) {
		p.Name, p.Policy, p.MaxAutoSize, p.SaveDir = name, policy, u.MaxAutoSize, saveDir
	})
	if err != nil {
		return PeerDTO{}, err
	}
	c.emitPeersChanged()
	p, err := c.n().Peers.Get(id)
	if err != nil {
		return PeerDTO{}, err
	}
	return toPeerDTO(p), nil
}

func (c *Core) RemovePeer(id string) error {
	if err := c.n().RemovePeer(id); err != nil {
		return err
	}
	c.emitPeersChanged()
	return nil
}

// ReorderPeers saves the order the user arranged the peers in.
func (c *Core) ReorderPeers(ids []string) error {
	if err := c.n().Peers.Reorder(ids); err != nil {
		return err
	}
	c.emitPeersChanged()
	return nil
}

func (c *Core) emitPeersChanged() {
	c.emit("peers:changed", c.ListPeers())
}

func toPeerDTO(p peers.Peer) PeerDTO {
	return PeerDTO{
		ID:          p.ID,
		Name:        p.Name,
		Policy:      p.Policy.String(),
		MaxAutoSize: p.MaxAutoSize,
		SaveDir:     p.SaveDir,
		AddedAt:     p.AddedAt.UnixMilli(),
		RemovedUs:   p.RemovedUs,
	}
}

// ---- Pairing ----

// StartInvite creates a pairing code and returns it as soon as the server has
// assigned one. The outcome arrives later as a "pair:done" or "pair:failed"
// event. Starting a new invite cancels the previous one.
func (c *Core) StartInvite() (Invite, error) {
	c.CancelInvite()
	ctx, cancel := context.WithTimeout(c.ctx, inviteTTL+30*time.Second)
	c.mu.Lock()
	c.inviteCancel = cancel
	c.mu.Unlock()

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		defer cancel()
		shown := false // onCode runs synchronously inside Invite
		p, err := c.n().Invite(ctx, func(code string) { shown = true; codeCh <- code })
		switch {
		case err == nil:
		case !shown:
			errCh <- err // StartInvite returns this directly
			return
		case errors.Is(ctx.Err(), context.Canceled):
			return // the user closed the dialog
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			c.emit("pair:failed", c.T("pair.expired"))
			return
		default:
			c.emit("pair:failed", c.friendlyPairError(err))
			return
		}
		c.emit("pair:done", toPeerDTO(p))
		c.emitPeersChanged()
	}()

	select {
	case code := <-codeCh:
		return Invite{Code: code, ExpiresAt: time.Now().Add(inviteTTL).UnixMilli()}, nil
	case err := <-errCh:
		cancel()
		return Invite{}, errors.New(c.friendlyPairError(err))
	}
}

// CancelInvite withdraws the pairing code currently being shown, if any.
func (c *Core) CancelInvite() {
	c.mu.Lock()
	cancel := c.inviteCancel
	c.inviteCancel = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// JoinWithCode pairs with the device showing code.
func (c *Core) JoinWithCode(code string) (PeerDTO, error) {
	p, err := c.n().Join(c.ctx, code)
	if err != nil {
		return PeerDTO{}, errors.New(c.friendlyPairError(err))
	}
	c.emitPeersChanged()
	return toPeerDTO(p), nil
}

// ---- Error messages people can act on ----

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func (c *Core) friendlyServerError(err error) string {
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return c.T("network.serverError")
	}
	return err.Error()
}

func (c *Core) friendlyPairError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "the code did not match"):
		return c.T("pair.mismatch")
	case strings.Contains(msg, "unknown or expired pairing code"):
		return c.T("pair.unknownCode")
	case strings.Contains(msg, "pairing code expired"):
		return c.T("pair.expired")
	case strings.Contains(msg, "own code"):
		return c.T("pair.ownCode")
	case strings.Contains(msg, "connect to rendezvous server"):
		return c.T("pair.noServer")
	case strings.Contains(msg, "a pairing code"):
		return c.T("pair.badFormat")
	}
	return c.T("pair.failed", "error", msg)
}
