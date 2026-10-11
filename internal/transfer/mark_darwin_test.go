package transfer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestReceivedFilesAreQuarantined(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Setup.app.zip")
	os.WriteFile(path, []byte("x"), 0o644)
	markDownloaded(path)
	buf := make([]byte, 256)
	n, err := unix.Getxattr(path, "com.apple.quarantine", buf)
	if err != nil || !strings.Contains(string(buf[:n]), "TropicalPeer") {
		t.Fatalf("no quarantine flag: %v %q", err, buf[:max(n, 0)])
	}
}
