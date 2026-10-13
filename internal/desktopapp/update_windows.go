package desktopapp

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// desktopUpdater installs a new TropicalPeer.exe in place of this one.
// A running exe can't be overwritten but can be renamed: it moves aside to
// .old (removed at the next start) and the new one takes its name.
type desktopUpdater struct{ app *App }

func (u desktopUpdater) AssetName(version string) string {
	if runtime.GOARCH != "amd64" { // releases ship an x64 build only
		return ""
	}
	return fmt.Sprintf("TropicalPeer-%s.exe", version)
}

func (u desktopUpdater) Dir() string { return filepath.Join(u.app.dataDir, "updates") }

func (u desktopUpdater) InstallOnQuit() bool { return true }

// CantInstallHere: the exe can't replace itself in a folder it may not
// change, such as Program Files.
func (u desktopUpdater) CantInstallHere() string {
	exe, err := runningExe()
	if err != nil || writableDir(filepath.Dir(exe)) {
		return ""
	}
	return "update.folderReadOnly"
}

func (u desktopUpdater) Install(path string) error {
	exe, err := runningExe()
	if err != nil {
		return err
	}
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return err
	}
	if err := moveFile(path, exe); err != nil {
		os.Rename(old, exe) // put the working version back
		return err
	}
	return nil
}

func (u desktopUpdater) Relaunch() error {
	exe, err := runningExe()
	if err != nil {
		return err
	}
	// The new copy waits for this one to exit (see waitForExit): until
	// then this process holds the single-instance lock.
	cmd := exec.Command(exe, afterUpdateFlag, strconv.Itoa(os.Getpid()))
	if err := cmd.Start(); err != nil {
		return err
	}
	wruntime.Quit(u.app.ctx)
	return nil
}

// cleanUpAfterUpdate removes what the previous update left behind.
func cleanUpAfterUpdate(dataDir string) {
	if exe, err := runningExe(); err == nil {
		os.Remove(exe + ".old")
	}
	cleanUpDownloads(filepath.Join(dataDir, "updates"))
}

// waitForExit waits (briefly) for the process that started this one to quit.
func waitForExit(pid int) {
	const synchronize = 0x00100000
	h, err := syscall.OpenProcess(synchronize, false, uint32(pid))
	if err != nil {
		return // already gone
	}
	defer syscall.CloseHandle(h)
	syscall.WaitForSingleObject(h, 20000)
}
