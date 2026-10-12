package desktop

import (
	"runtime"
	"sync"

	"fyne.io/systray"
)

var (
	trayMu             sync.Mutex
	trayOpen, trayQuit *systray.MenuItem
	trayPending        *TrayLabels // labels set before the icon was ready
)

// StartTray puts the app's icon in the notification area. Clicking it calls
// open; its menu offers open and quit. icon is an .ico file.
func StartTray(icon []byte, labels TrayLabels, open, quit func()) {
	h := trayHandlers{open: open, quit: quit}
	go func() {
		// The tray's hidden window gets its messages on the thread that made it.
		runtime.LockOSThread()
		systray.Run(func() {
			systray.SetIcon(icon)
			systray.SetTooltip("TropicalPeer")
			systray.SetOnTapped(h.onOpen)
			trayMu.Lock()
			if trayPending != nil {
				labels = *trayPending
			}
			trayOpen = systray.AddMenuItem(labels.Open, "")
			systray.AddSeparator()
			trayQuit = systray.AddMenuItem(labels.Quit, "")
			openCh, quitCh := trayOpen.ClickedCh, trayQuit.ClickedCh
			trayMu.Unlock()
			go func() {
				for {
					select {
					case <-openCh:
						h.onOpen()
					case <-quitCh:
						h.onQuit()
					}
				}
			}()
		}, nil)
	}()
}

// SetTrayLabels changes the menu's language.
func SetTrayLabels(labels TrayLabels) {
	trayMu.Lock()
	defer trayMu.Unlock()
	if trayOpen == nil {
		trayPending = &labels
		return
	}
	trayOpen.SetTitle(labels.Open)
	trayQuit.SetTitle(labels.Quit)
}

// StopTray removes the icon; Windows would otherwise leave it behind until
// the mouse passes over it.
func StopTray() { systray.Quit() }
