package desktopapp

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// desktopUpdater installs a new TropicalPeer.app in place of this one, from
// the release's disk image: the new bundle is copied next to the old one,
// and the two swap names once this process has exited.
type desktopUpdater struct{ app *App }

func (u desktopUpdater) AssetName(version string) string {
	if _, err := appBundle(); err != nil {
		return "" // not running from an .app (e.g. wails dev)
	}
	return fmt.Sprintf("TropicalPeer-%s.dmg", version)
}

func (u desktopUpdater) Dir() string { return filepath.Join(u.app.dataDir, "updates") }

func (u desktopUpdater) InstallOnQuit() bool { return true }

// CantInstallHere: an app opened from its disk image, or from Downloads
// without moving it (macOS then runs it from a read-only copy, "App
// Translocation"), can't replace itself.
func (u desktopUpdater) CantInstallHere() string {
	bundle, err := appBundle()
	if err != nil || writableDir(filepath.Dir(bundle)) {
		return ""
	}
	if strings.Contains(bundle, "/AppTranslocation/") || strings.HasPrefix(bundle, "/Volumes/") {
		return "update.moveToApplications"
	}
	return "update.folderReadOnly"
}

func (u desktopUpdater) Install(dmg string) error {
	bundle, err := appBundle()
	if err != nil {
		return err
	}
	mnt, err := os.MkdirTemp("", "tropicalpeer-update")
	if err != nil {
		return err
	}
	defer os.Remove(mnt)
	if out, err := exec.Command("hdiutil", "attach", "-nobrowse", "-readonly", "-noautoopen", "-mountpoint", mnt, dmg).CombinedOutput(); err != nil {
		return fmt.Errorf("mounting the update: %v: %s", err, out)
	}
	defer exec.Command("hdiutil", "detach", "-force", mnt).Run()

	fresh := bundle + ".new"
	os.RemoveAll(fresh)
	if out, err := exec.Command("ditto", filepath.Join(mnt, "TropicalPeer.app"), fresh).CombinedOutput(); err != nil {
		os.RemoveAll(fresh)
		return fmt.Errorf("copying the update: %v: %s", err, out)
	}
	// The swap waits until this process has exited. Renaming the bundle of
	// a running app leaves the Dock with a second icon for it: the old
	// process runs on from TropicalPeer.app.old, a different app to the Dock.
	cmd := exec.Command("/bin/sh", "-c", swapScript, "swap", strconv.Itoa(os.Getpid()), bundle, u.relaunchMarker())
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // outlives this process
	if err := cmd.Start(); err != nil {
		os.RemoveAll(fresh)
		return err
	}
	return cmd.Process.Release()
}

// swapScript puts bundle.new in place of bundle once process $1 has exited,
// keeping the old version if that fails, then opens the app if the marker
// file asks for it.
const swapScript = `pid=$1 bundle=$2 marker=$3
while kill -0 "$pid" 2>/dev/null; do sleep 0.2; done
rm -rf "$bundle.old"
if mv "$bundle" "$bundle.old"; then
	if mv "$bundle.new" "$bundle"; then rm -rf "$bundle.old"; else mv "$bundle.old" "$bundle"; fi
fi
if [ -e "$marker" ]; then rm -f "$marker"; open "$bundle"; fi`

// relaunchMarker is the file that asks the swap to open the app afterwards.
func (u desktopUpdater) relaunchMarker() string {
	return filepath.Join(u.Dir(), fmt.Sprintf("relaunch-%d", os.Getpid()))
}

// Relaunch quits; the swap Install started then opens the new version
// (only once this process is gone, so just one copy runs at any time).
func (u desktopUpdater) Relaunch() error {
	if err := os.WriteFile(u.relaunchMarker(), nil, 0o600); err != nil {
		return err
	}
	wruntime.Quit(u.app.ctx)
	return nil
}

// appBundle is the TropicalPeer.app this process runs from.
func appBundle() (string, error) {
	exe, err := runningExe()
	if err != nil {
		return "", err
	}
	bundle := filepath.Dir(filepath.Dir(filepath.Dir(exe))) // .app/Contents/MacOS/TropicalPeer
	if !strings.HasSuffix(bundle, ".app") {
		return "", errors.New("not running from an app bundle")
	}
	return bundle, nil
}

func cleanUpAfterUpdate(dataDir string) {
	if bundle, err := appBundle(); err == nil {
		os.RemoveAll(bundle + ".old")
	}
	cleanUpDownloads(filepath.Join(dataDir, "updates"))
}

func waitForExit(pid int) {
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
	}
}
