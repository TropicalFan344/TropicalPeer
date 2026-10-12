package desktopapp

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// afterUpdateFlag starts the new version after an update: it names the old
// process, which the new one waits for before taking over.
const afterUpdateFlag = "--after-update"

// HandleAfterUpdate waits for the previous version to quit when this
// process was started by an update, then cleans up what it left.
func HandleAfterUpdate(dataDir string, args []string) {
	for i, a := range args {
		if a == afterUpdateFlag && i+1 < len(args) {
			if pid, err := strconv.Atoi(args[i+1]); err == nil {
				waitForExit(pid)
			}
		}
	}
	cleanUpAfterUpdate(dataDir)
}

func runningExe() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// moveFile renames src to dst, or copies it when they're on different drives.
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return err
	}
	os.Remove(src)
	return nil
}
