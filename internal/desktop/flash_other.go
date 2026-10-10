//go:build !windows && !darwin

package desktop

// FlashWindow is a no-op where no taskbar-attention API is wired up yet.
func FlashWindow() {}
