package main

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
// then the two swap names (the old one is removed at the next start).
type desktopUpdater struct{ app *App }

func (u desktopUpdater) AssetName(version string) string {
	if _, err := appBundle(); err != nil {
		return "" // not running from an .app (e.g. wails dev)
	}
	return fmt.Sprintf("TropicalPeer-%s.dmg", version)
}

func (u desktopUpdater) Dir() string { return filepath.Join(u.app.dataDir, "updates") }

func (u desktopUpdater) InstallOnQuit() bool { return true }

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
	old := bundle + ".old"
	os.RemoveAll(old)
	if err := os.Rename(bundle, old); err != nil {
		os.RemoveAll(fresh)
		return err
	}
	if err := os.Rename(fresh, bundle); err != nil {
		os.Rename(old, bundle) // put the working version back
		return err
	}
	return nil
}

func (u desktopUpdater) Relaunch() error {
	bundle, err := appBundle()
	if err != nil {
		return err
	}
	// The new copy waits for this one to exit (see waitForExit): until
	// then this process holds the single-instance lock.
	if err := exec.Command("open", "-n", bundle, "--args", afterUpdateFlag, strconv.Itoa(os.Getpid())).Start(); err != nil {
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
	os.RemoveAll(filepath.Join(dataDir, "updates"))
}

func waitForExit(pid int) {
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
	}
}
