//go:build !windows && !darwin

package desktop

// The tray is only wired up on Windows and macOS.

func StartTray(icon []byte, labels TrayLabels, open, quit func()) {}
func SetTrayLabels(labels TrayLabels)                             {}
func StopTray()                                                   {}
