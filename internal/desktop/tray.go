package desktop

// TrayLabels are the tray menu's items, in the app's language.
type TrayLabels struct {
	Open string
	Quit string
}

// trayHandlers run off the UI thread, so they may call back into Wails.
type trayHandlers struct {
	open, quit func()
}

func (h trayHandlers) onOpen() { go h.open() }
func (h trayHandlers) onQuit() { go h.quit() }
