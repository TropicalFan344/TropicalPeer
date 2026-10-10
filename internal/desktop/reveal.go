// Package desktop holds the desktop app's small OS integrations: getting the
// user's attention and showing files in the system file manager.
package desktop

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// RevealInFileManager shows path selected in the system file manager.
func RevealInFileManager(path string) error {
	if _, err := os.Stat(path); err != nil {
		return errors.New("that file isn't there anymore; it may have been moved or deleted")
	}
	switch runtime.GOOS {
	case "windows":
		// explorer.exe returns exit code 1 even on success, so ignore Run's error.
		exec.Command("explorer.exe", "/select,", filepath.Clean(path)).Run()
		return nil
	case "darwin":
		return exec.Command("open", "-R", path).Run()
	default:
		return exec.Command("xdg-open", filepath.Dir(path)).Start()
	}
}

// OpenFolder opens dir in the system file manager.
func OpenFolder(dir string) error {
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return errors.New("that folder doesn't exist yet; it's created when the first file arrives")
	}
	switch runtime.GOOS {
	case "windows":
		exec.Command("explorer.exe", filepath.Clean(dir)).Run()
		return nil
	case "darwin":
		return exec.Command("open", dir).Run()
	default:
		return exec.Command("xdg-open", dir).Start()
	}
}
