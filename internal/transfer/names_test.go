package transfer

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSafeNameRemovesDirectionTricks(t *testing.T) {
	// U+202E (right-to-left override) would show this as "invoiceexe.pdf".
	got := SafeName("invoice‮fdp.exe")
	if strings.ContainsRune(got, '‮') || !strings.HasSuffix(got, ".exe") {
		t.Fatalf("SafeName = %q", got)
	}
	for _, name := range []string{"../../etc/passwd", `..\..\boot.ini`, "a:b", "CON.txt"} {
		if s := SafeName(name); strings.ContainsAny(s, `/\:`) || s == "CON.txt" {
			t.Fatalf("SafeName(%q) = %q", name, s)
		}
	}
}

func TestReceivedFilesAreMarkedAsDownloaded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "setup.exe")
	os.WriteFile(path, []byte("x"), 0o644)
	markDownloaded(path)
	if runtime.GOOS == "windows" {
		zone, err := os.ReadFile(path + ":Zone.Identifier")
		if err != nil || !strings.Contains(string(zone), "ZoneId=3") {
			t.Fatalf("no Mark of the Web: %v %q", err, zone)
		}
	}
}
