// Package desktopapp is the Wails desktop shell around appcore.Core;
// main.go at the repository root runs it.
package desktopapp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"TropicalPeer/internal/appcore"
	"TropicalPeer/internal/desktop"
)

// App is the desktop shell around appcore.Core. Every exported method is
// callable from JavaScript (see frontend/wailsjs/go/desktopapp/App); most
// simply hand over to the core. Only what needs Wails or the desktop lives here:
// native dialogs, the file manager, the tray icon and taskbar attention.
//
// The methods are written out instead of embedding Core so Wails binds
// exactly this list and nothing meant for Go callers (Start, SendSources).
type App struct {
	ctx     context.Context
	core    *appcore.Core
	dataDir string
	icons   Icons
	notes   notes
}

// Icons are embedded next to main.go, since go:embed can't reach build/
// from here.
type Icons struct {
	Tray  []byte
	Toast []byte // Windows notifications only
}

func NewApp(dataDir string, icons Icons) *App {
	a := &App{dataDir: dataDir, icons: icons}
	a.core = appcore.New(dataDir, func(name string, data any) {
		if data == nil {
			runtime.EventsEmit(a.ctx, name)
		} else {
			runtime.EventsEmit(a.ctx, name, data)
		}
		if name == "offer:new" {
			desktop.FlashWindow()
		}
		go a.notifyEvent(name, data)
	})
	a.core.SetUpdater(desktopUpdater{app: a})
	return a
}

// Hooks are an App's lifecycle callbacks for options.App. They're handed
// out here rather than as exported methods, which Wails would bind.
type Hooks struct {
	OnStartup        func(context.Context)
	OnShutdown       func(context.Context)
	OnSecondInstance func(options.SecondInstanceData)
}

func HooksFor(a *App) Hooks {
	return Hooks{OnStartup: a.startup, OnShutdown: a.shutdown, OnSecondInstance: a.onSecondInstance}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	if err := a.core.Start(ctx); err != nil {
		runtime.MessageDialog(ctx, runtime.MessageDialogOptions{
			Type:    runtime.ErrorDialog,
			Title:   "TropicalPeer can't start",
			Message: err.Error(),
		})
		runtime.Quit(ctx)
		return
	}
	// Closing the window only hides it (HideWindowOnClose), so offers and
	// transfers keep arriving; the tray icon brings it back or quits.
	desktop.StartTray(a.icons.Tray, a.trayLabels(), a.showWindow, func() { runtime.Quit(ctx) })
	a.initNotes()
}

func (a *App) trayLabels() desktop.TrayLabels {
	return desktop.TrayLabels{Open: a.core.T("tray.open"), Quit: a.core.T("tray.quit")}
}

// showWindow brings the window back from the tray, the Dock or behind
// other windows.
func (a *App) showWindow() {
	runtime.Show(a.ctx) // a Mac app hidden by closing its window
	runtime.WindowUnminimise(a.ctx)
	runtime.WindowShow(a.ctx)
	// Windows only lets a background app flash its taskbar button, not
	// take focus; briefly staying on top brings it in front.
	runtime.WindowSetAlwaysOnTop(a.ctx, true)
	runtime.WindowSetAlwaysOnTop(a.ctx, false)
}

// drawAttention is for when a notification couldn't be shown: a window
// closed to the tray comes back minimised, so it has a taskbar button to
// flash (on a Mac the Dock icon bounces).
func (a *App) drawAttention() {
	if a.ctx != nil && desktop.WindowHidden() {
		runtime.WindowMinimise(a.ctx)
		for i := 0; i < 20 && desktop.WindowHidden(); i++ {
			time.Sleep(25 * time.Millisecond)
		}
	}
	desktop.FlashWindow()
}

// onSecondInstance runs when TropicalPeer is opened again while this copy
// is running: show this window instead of starting another.
func (a *App) onSecondInstance(options.SecondInstanceData) {
	if a.ctx == nil {
		return
	}
	a.showWindow()
}

// shutdown removes the tray icon and the router port forward before the
// app exits.
func (a *App) shutdown(context.Context) {
	desktop.StopTray()
	a.core.Stop()
}

// ---- Device, server and network ----

func (a *App) GetDevice() appcore.DeviceInfo             { return a.core.GetDevice() }
func (a *App) SetDeviceName(name string) error           { return a.core.SetDeviceName(name) }
func (a *App) GetServerStatus() appcore.ServerStatus     { return a.core.GetServerStatus() }
func (a *App) GetNetwork() appcore.NetworkDTO            { return a.core.GetNetwork() }
func (a *App) RecheckNetwork()                           { a.core.RecheckNetwork() }
func (a *App) GetSettings() appcore.SettingsDTO          { return a.core.GetSettings() }
func (a *App) SaveSettings(in appcore.SettingsDTO) error { return a.core.SaveSettings(in) }

// ---- Peers and pairing ----

func (a *App) ListPeers() []appcore.PeerDTO { return a.core.ListPeers() }
func (a *App) UpdatePeer(id string, u appcore.PeerUpdate) (appcore.PeerDTO, error) {
	return a.core.UpdatePeer(id, u)
}
func (a *App) RemovePeer(id string) error                        { return a.core.RemovePeer(id) }
func (a *App) ReorderPeers(ids []string) error                   { return a.core.ReorderPeers(ids) }
func (a *App) StartInvite() (appcore.Invite, error)              { return a.core.StartInvite() }
func (a *App) CancelInvite()                                     { a.core.CancelInvite() }
func (a *App) JoinWithCode(code string) (appcore.PeerDTO, error) { return a.core.JoinWithCode(code) }

// ---- Transfers and offers ----

func (a *App) SendFiles(peerID string, paths []string) (appcore.TransferDTO, error) {
	return a.core.SendFiles(peerID, paths)
}
func (a *App) CancelTransfer(id string)                             { a.core.CancelTransfer(id) }
func (a *App) RetryTransfer(id string) (appcore.TransferDTO, error) { return a.core.RetryTransfer(id) }
func (a *App) GetUpdate() appcore.UpdateDTO                         { return a.core.GetUpdate() }
func (a *App) CheckForUpdate() appcore.UpdateDTO                    { return a.core.CheckForUpdate() }
func (a *App) SetAutoUpdate(on bool) error                          { return a.core.SetAutoUpdate(on) }
func (a *App) InstallUpdate() error                                 { return a.core.InstallUpdate() }
func (a *App) PostponeUpdate()                                      { a.core.PostponeUpdate() }
func (a *App) PauseTransfer(id string)                              { a.core.PauseTransfer(id) }
func (a *App) ResumeTransfer(id string) (appcore.TransferDTO, error) {
	return a.core.ResumeTransfer(id)
}
func (a *App) ListTransfers() []appcore.TransferDTO      { return a.core.ListTransfers() }
func (a *App) ClearFinishedTransfers()                   { a.core.ClearFinishedTransfers() }
func (a *App) ListOffers() []appcore.OfferDTO            { return a.core.ListOffers() }
func (a *App) RespondOffer(id string, accept bool) error { return a.core.RespondOffer(id, accept) }

// Restart restarts networking without quitting, applying settings that
// only take effect on start (the listening port).
func (a *App) Restart() error { return a.core.Restart() }

// SetLanguage sets the language of messages from Go (a locales code).
func (a *App) SetLanguage(lang string) {
	a.core.SetLanguage(lang)
	desktop.SetTrayLabels(a.trayLabels())
	a.notesLanguageChanged()
}

// ---- Desktop only ----

// PickFiles opens the system file picker for choosing files to send.
func (a *App) PickFiles() ([]string, error) {
	return runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{Title: "Choose files to send"})
}

// PickFolder opens the system folder picker for choosing a folder to send.
// It returns nothing if cancelled.
func (a *App) PickFolder() ([]string, error) {
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "Choose a folder to send"})
	if err != nil || dir == "" {
		return nil, err
	}
	return []string{dir}, nil
}

// ChooseFolder opens the system folder picker. It returns "" if cancelled.
func (a *App) ChooseFolder(title, start string) (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: title,
		// Wails refuses to open the dialog at all if this folder is missing,
		// e.g. the default download folder before anything was received.
		DefaultDirectory:     nearestExistingDir(start),
		CanCreateDirectories: true,
	})
}

// ShowFile opens the file manager with the file selected.
func (a *App) ShowFile(path string) error {
	if err := desktop.RevealInFileManager(path); err != nil {
		return errors.New(a.core.T("transfer.fileGone"))
	}
	return nil
}

// OpenFolder opens a folder in the file manager.
func (a *App) OpenFolder(dir string) error {
	if err := desktop.OpenFolder(dir); err != nil {
		return errors.New(a.core.T("transfer.folderMissing"))
	}
	return nil
}

// nearestExistingDir returns dir or its closest existing parent, or "" if
// none exists.
func nearestExistingDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	dir = filepath.Clean(dir)
	for {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
