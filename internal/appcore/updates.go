package appcore

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"TropicalPeer/internal/update"
)

// Updates.
//
// The app looks for a new release shortly after it starts and then every
// few hours. With automatic updates on (the default) it downloads the new
// version for this platform, checks it against the signed checksums (see
// package update) and installs it: on a computer by replacing the app and
// restarting it once no transfer is running, on Android by handing it to
// the system installer, which asks the person to confirm.

// Updater is what installing looks like on this platform. The desktop app
// and the Android app each provide one.
type Updater interface {
	// AssetName is the release file for this platform ("" = can't update).
	AssetName(version string) string
	// Dir is where downloads go.
	Dir() string
	// Install puts the downloaded file in place of the running app. On a
	// computer the new version starts with the next launch (see Relaunch);
	// on Android the system takes over from here.
	Install(path string) error
	// Relaunch quits and starts the installed version. It may be a no-op.
	Relaunch() error
	// InstallOnQuit says whether a postponed update may be put in place
	// as the app quits (a computer: yes; Android, where installing means a
	// system dialog: no).
	InstallOnQuit() bool
}

// InstallBlocker is an Updater that can tell before downloading that it
// couldn't put an update in place, e.g. because the app runs from a folder
// it can't write to (a disk image, a protected folder). CantInstallHere
// returns the locale key of what the person should do, or "" if all is well.
type InstallBlocker interface {
	CantInstallHere() string
}

// Update states as the frontend sees them.
const (
	UpdateIdle        = "idle" // not checked yet, or automatic updates are off
	UpdateChecking    = "checking"
	UpdateCurrent     = "current" // this is the newest version
	UpdateDownloading = "downloading"
	UpdateReady       = "ready"      // downloaded and verified; installs at the next chance
	UpdateInstalling  = "installing" // handed over (Android) or restarting (computer)
	UpdateFailed      = "failed"
	UpdateUnsupported = "unsupported" // a development build, or a platform without updates
)

// UpdateDTO is the update status, pushed as "update:changed".
type UpdateDTO struct {
	State     string `json:"state"`
	Current   string `json:"current"` // this app's version
	Version   string `json:"version"` // the newer version, when there is one
	Done      int64  `json:"done"`    // download progress
	Total     int64  `json:"total"`
	Error     string `json:"error"`
	CheckedAt int64  `json:"checkedAt"` // unix ms, 0 = never
	Auto      bool   `json:"auto"`      // automatic updates are on
	// RestartAt (unix ms) is when a downloaded update restarts the app by
	// itself; 0 = not scheduled. PostponeUpdate cancels it.
	RestartAt int64 `json:"restartAt"`
}

var (
	updateFirstCheck = 20 * time.Second
	updateEvery      = 6 * time.Hour
	updateRestartIn  = 15 * time.Second // the notice before restarting by itself
)

type updates struct {
	mu        sync.Mutex
	updater   Updater
	dto       UpdateDTO
	path      string // the verified download
	postponed bool   // the person said "not now": install on quit instead
	installed bool   // already put in place
	running   bool   // a check/download is in progress
	wake      chan struct{}
}

// SetUpdater enables updates; call it before Start.
func (c *Core) SetUpdater(u Updater) { c.upd.updater = u }

func (c *Core) startUpdates() {
	c.upd.wake = make(chan struct{}, 1)
	c.upd.mu.Lock()
	c.upd.dto = UpdateDTO{State: UpdateIdle, Current: Version, Auto: c.autoUpdate()}
	if c.upd.updater == nil || c.upd.updater.AssetName(Version) == "" || !update.Newer("999999.0.0", Version) {
		c.upd.dto.State = UpdateUnsupported
		c.upd.mu.Unlock()
		return
	}
	c.upd.mu.Unlock()
	go func() {
		next := time.After(updateFirstCheck)
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-next:
			case <-c.upd.wake:
			}
			if c.autoUpdate() {
				c.checkForUpdate()
			}
			next = time.After(updateEvery)
		}
	}()
	go c.restartWhenIdle()
}

func (c *Core) autoUpdate() bool {
	if c.n() == nil {
		return true
	}
	return !c.n().Settings().DisableAutoUpdate
}

// GetUpdate reports the update status.
func (c *Core) GetUpdate() UpdateDTO {
	c.upd.mu.Lock()
	defer c.upd.mu.Unlock()
	d := c.upd.dto
	d.Auto = c.autoUpdate()
	return d
}

// CheckForUpdate looks for a new version now (and downloads it), whether
// or not automatic updates are on. Progress arrives as "update:changed".
func (c *Core) CheckForUpdate() UpdateDTO {
	go c.checkForUpdate()
	return c.GetUpdate()
}

// SetAutoUpdate turns automatic updates on or off.
func (c *Core) SetAutoUpdate(on bool) error {
	s := c.n().Settings()
	s.DisableAutoUpdate = !on
	if err := c.n().UpdateSettings(s); err != nil {
		return err
	}
	if on && c.upd.wake != nil {
		select {
		case c.upd.wake <- struct{}{}:
		default:
		}
	}
	c.emitUpdate()
	return nil
}

// InstallUpdate installs a downloaded update now (restarting the app on a
// computer, opening the system installer on Android).
func (c *Core) InstallUpdate() error {
	c.upd.mu.Lock()
	path, state := c.upd.path, c.upd.dto.State
	c.upd.mu.Unlock()
	if path == "" || (state != UpdateReady && state != UpdateInstalling) {
		return c.errT("update.notReady")
	}
	return c.install(true)
}

// PostponeUpdate cancels the automatic restart; the update installs when
// the app quits and runs from the next start.
func (c *Core) PostponeUpdate() {
	c.upd.mu.Lock()
	c.upd.postponed = true
	c.upd.dto.RestartAt = 0
	c.upd.mu.Unlock()
	c.emitUpdate()
}

func (c *Core) setUpdate(fn func(d *UpdateDTO)) {
	c.upd.mu.Lock()
	fn(&c.upd.dto)
	c.upd.mu.Unlock()
	c.emitUpdate()
}

func (c *Core) emitUpdate() { c.emit("update:changed", c.GetUpdate()) }

func (c *Core) checkForUpdate() {
	c.upd.mu.Lock()
	if c.upd.running || c.upd.updater == nil || c.upd.dto.State == UpdateUnsupported || c.upd.installed {
		c.upd.mu.Unlock()
		return
	}
	c.upd.running = true
	c.upd.mu.Unlock()
	defer func() {
		c.upd.mu.Lock()
		c.upd.running = false
		c.upd.mu.Unlock()
	}()

	c.setUpdate(func(d *UpdateDTO) { d.State, d.Error = UpdateChecking, "" })
	ctx, cancel := context.WithTimeout(c.ctx, 30*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 25 * time.Minute}
	rel, err := update.Latest(ctx, client)
	now := time.Now().UnixMilli()
	// An older (or the same) release is no news, signed or not.
	if rel != nil && !update.Newer(rel.Version, Version) {
		c.setUpdate(func(d *UpdateDTO) { d.State, d.Version, d.Error, d.CheckedAt = UpdateCurrent, "", "", now })
		return
	}
	if err != nil {
		log.Printf("update: check failed: %v", err)
		c.setUpdate(func(d *UpdateDTO) { d.State, d.Error, d.CheckedAt = UpdateFailed, c.updateError(err), now })
		return
	}
	// Downloading would be for nothing: say what to do instead.
	if key := c.cantInstallHere(); key != "" {
		log.Printf("update: %s is available (this is %s), but this copy can't replace itself where it is", rel.Version, Version)
		c.setUpdate(func(d *UpdateDTO) {
			d.State, d.Version, d.Error, d.CheckedAt = UpdateFailed, rel.Version, c.T(key), now
		})
		return
	}
	name := c.upd.updater.AssetName(rel.Version)
	log.Printf("update: %s is available (this is %s); downloading %s", rel.Version, Version, name)
	c.setUpdate(func(d *UpdateDTO) {
		d.State, d.Version, d.CheckedAt, d.Done, d.Total = UpdateDownloading, rel.Version, now, 0, 0
	})
	var last time.Time
	path, err := update.Download(ctx, client, rel, name, c.upd.updater.Dir(), func(done, total int64) {
		if time.Since(last) > 250*time.Millisecond {
			last = time.Now()
			c.setUpdate(func(d *UpdateDTO) { d.Done, d.Total = done, total })
		}
	})
	if err != nil {
		log.Printf("update: download failed: %v", err)
		c.setUpdate(func(d *UpdateDTO) { d.State, d.Error = UpdateFailed, c.updateError(err) })
		return
	}
	log.Printf("update: %s downloaded and verified", rel.Version)
	c.upd.mu.Lock()
	c.upd.path = path
	c.upd.mu.Unlock()
	c.setUpdate(func(d *UpdateDTO) { d.State, d.Done = UpdateReady, d.Total })
}

// cantInstallHere is the locale key of why an update couldn't be put in
// place, or "" (see InstallBlocker).
func (c *Core) cantInstallHere() string {
	if b, ok := c.upd.updater.(InstallBlocker); ok {
		return b.CantInstallHere()
	}
	return ""
}

func (c *Core) updateError(err error) string {
	if errors.Is(err, update.ErrUnsigned) {
		return c.T("update.unsigned")
	}
	return c.T("update.failed")
}

// busy reports whether restarting now would interrupt something.
func (c *Core) busy() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.offers) > 0 {
		return true
	}
	for _, st := range c.transfers {
		if st.dto.active() {
			return true
		}
	}
	return false
}

// restartWhenIdle installs a downloaded update by itself (if updates are
// automatic and nobody said "not now"): on a computer once nothing is being
// transferred, after a short notice; on Android by opening the installer.
func (c *Core) restartWhenIdle() {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-tick.C:
		}
		c.upd.mu.Lock()
		ready := c.upd.dto.State == UpdateReady && !c.upd.postponed && c.upd.path != ""
		at := c.upd.dto.RestartAt
		c.upd.mu.Unlock()
		if !ready || !c.autoUpdate() {
			continue
		}
		if c.busy() {
			if at != 0 { // something started during the notice: wait again
				c.setUpdate(func(d *UpdateDTO) { d.RestartAt = 0 })
			}
			continue
		}
		switch {
		case at == 0:
			c.setUpdate(func(d *UpdateDTO) { d.RestartAt = time.Now().Add(updateRestartIn).UnixMilli() })
		case time.Now().UnixMilli() >= at:
			if err := c.install(true); err != nil {
				log.Printf("update: install failed: %v", err)
			}
		}
	}
}

// install puts the update in place and, if relaunch, restarts into it.
func (c *Core) install(relaunch bool) error {
	c.upd.mu.Lock()
	path, done := c.upd.path, c.upd.installed
	c.upd.mu.Unlock()
	// On Android "installing" means opening the system installer, which the
	// person may have dismissed: hand it over again each time.
	if !done || !c.upd.updater.InstallOnQuit() {
		if err := c.upd.updater.Install(path); err != nil {
			msg := c.T("update.installFailed")
			if key := c.cantInstallHere(); key != "" {
				msg = c.T(key)
			}
			c.setUpdate(func(d *UpdateDTO) { d.State, d.Error, d.RestartAt = UpdateFailed, msg, 0 })
			return err
		}
		c.upd.mu.Lock()
		c.upd.installed = true
		c.upd.mu.Unlock()
		log.Printf("update: installed %s", c.GetUpdate().Version)
	}
	c.setUpdate(func(d *UpdateDTO) { d.State, d.RestartAt = UpdateInstalling, 0 })
	if relaunch {
		return c.upd.updater.Relaunch()
	}
	return nil
}

// installOnQuit puts a downloaded, postponed update in place as the app
// quits, so the next start runs it. Stop calls it.
func (c *Core) installOnQuit() {
	c.upd.mu.Lock()
	pending := c.upd.path != "" && !c.upd.installed && c.upd.dto.State == UpdateReady
	c.upd.mu.Unlock()
	if pending && c.upd.updater != nil && c.upd.updater.InstallOnQuit() {
		c.install(false)
	}
}
