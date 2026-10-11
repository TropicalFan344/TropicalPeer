package mobile

import (
	"encoding/json"
	"fmt"
)

// androidUpdater downloads new versions in Go and hands the APK to the app,
// which opens Android's installer ("update:install"); Android asks the
// person to confirm and only accepts an APK signed with the same key.
type androidUpdater struct {
	dir      string
	listener Listener
}

func (u androidUpdater) AssetName(version string) string {
	return fmt.Sprintf("TropicalPeer-%s.apk", version)
}

func (u androidUpdater) Dir() string { return u.dir }

func (u androidUpdater) Install(path string) error {
	b, _ := json.Marshal(map[string]string{"path": path})
	u.listener.OnEvent("update:install", string(b))
	return nil
}

func (u androidUpdater) Relaunch() error     { return nil } // Android restarts the app itself
func (u androidUpdater) InstallOnQuit() bool { return false }
