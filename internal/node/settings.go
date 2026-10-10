package node

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"TropicalPeer/internal/rendezvous"
)

// utf8BOM is tolerated at the start of JSON files, which Windows editors
// and PowerShell often add when someone edits them by hand.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

const settingsFile = "settings.json"

// DefaultPort is the UDP port TropicalPeer listens on. A fixed port makes
// router port forwarding possible; use 0 for a random port.
const DefaultPort = 7778

type Settings struct {
	ServerAddr  string `json:"serverAddr"`
	Port        int    `json:"port"`
	DownloadDir string `json:"downloadDir"`
	// DisablePortMapping turns off asking the router (UPnP / NAT-PMP) to
	// forward our port. Stored inverted so older settings files default to on.
	DisablePortMapping bool `json:"disablePortMapping,omitempty"`
}

func defaultSettings() Settings {
	dl := "TropicalPeer Downloads"
	if home, err := os.UserHomeDir(); err == nil {
		dl = filepath.Join(home, "Downloads", "TropicalPeer")
	}
	return Settings{ServerAddr: rendezvous.DefaultAddr, Port: DefaultPort, DownloadDir: dl}
}

func loadSettings(dir string) (Settings, error) {
	s := defaultSettings()
	data, err := os.ReadFile(filepath.Join(dir, settingsFile))
	if errors.Is(err, os.ErrNotExist) {
		return s, saveSettings(dir, s)
	}
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(bytes.TrimPrefix(data, utf8BOM), &s)
}

func saveSettings(dir string, s Settings) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, settingsFile), data, 0o600)
}

// DefaultDataDir is where the app keeps its identity, peers and settings.
func DefaultDataDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "TropicalPeer")
	}
	return ".tropicalpeer"
}
