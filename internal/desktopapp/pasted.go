package desktopapp

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Pasted files wait here to be sent. They stay a while, so a send that
// stops can pick up again, then go on the next paste.
const (
	pastedDir     = "pasted"
	pastedKeep    = 7 * 24 * time.Hour
	maxPastedSize = 200 << 20 // the page holds it all in memory as base64
)

// SavePasted writes a file pasted into the window (a screenshot or a copied
// image, which has no path of its own) and returns its path, ready to send.
// name is what the clipboard called it; data is base64.
func (a *App) SavePasted(name, data string) (string, error) {
	if base64.StdEncoding.DecodedLen(len(data)) > maxPastedSize {
		return "", errors.New(a.core.T("share.pasteTooBig"))
	}
	b, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(a.dataDir, pastedDir)
	prunePasted(dir, time.Now())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// A folder per paste keeps the name as given and two pastes apart.
	sub, err := os.MkdirTemp(dir, time.Now().Format("20060102-150405-"))
	if err != nil {
		return "", err
	}
	path := filepath.Join(sub, pastedName(name, time.Now()))
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// pastedName keeps a real file name; a bare "image.png", which is what
// browsers call any image on the clipboard, gets the time it was pasted.
func pastedName(name string, now time.Time) string {
	name = strings.TrimSpace(filepath.Base(strings.ReplaceAll(name, "\\", "/")))
	ext := strings.ToLower(filepath.Ext(name))
	if name == "" || name == "." || name == "/" || strings.ContainsAny(name, `<>:"|?*`) {
		name, ext = "", ".png"
	}
	if name == "" || strings.EqualFold(strings.TrimSuffix(name, filepath.Ext(name)), "image") {
		return "Pasted image " + now.Format("2006-01-02 15.04.05") + ext
	}
	return name
}

// prunePasted removes pastes older than pastedKeep.
func prunePasted(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err == nil && now.Sub(info.ModTime()) > pastedKeep {
			os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}
