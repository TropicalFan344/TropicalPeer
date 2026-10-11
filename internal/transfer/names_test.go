package transfer

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"TropicalPeer/internal/protocol"
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

func TestSafePathStaysInside(t *testing.T) {
	for in, want := range map[string]string{
		"Album/2024/a.jpg":             "Album/2024/a.jpg",
		`Album\2024\a.jpg`:             "Album/2024/a.jpg",
		"../../etc/passwd":             "etc/passwd",
		"/abs/./x//y.txt":              "abs/x/y.txt",
		"C:/Windows/evil.dll":          "C_/Windows/evil.dll",
		"Album/../../../a.txt":         "Album/a.txt",
		"Album/CON/a.txt":              "Album/_CON/a.txt",
		"a/ ../b.txt":                  "a/b.txt",
		"../..":                        "",
		strings.Repeat("d/", 50) + "f": strings.Repeat("d/", maxFolderDepth-1) + "f",
	} {
		if got := SafePath(in); got != want {
			t.Errorf("SafePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFolderMapKeepsCopiesApart(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "Album"), 0o755)
	fm := &folderMap{dir: dir, names: map[string]string{}}
	d, f, err := fm.target("Album/sub/a.jpg")
	if err != nil || d != filepath.Join(dir, "Album (1)", "sub") || f != "a.jpg" {
		t.Fatalf("target = %q %q %v", d, f, err)
	}
	if d, _, _ := fm.target("Album/b.jpg"); d != filepath.Join(dir, "Album (1)") {
		t.Fatalf("second file of the folder went to %q", d)
	}
	if d, f, _ := fm.target("loose.txt"); d != dir || f != "loose.txt" {
		t.Fatalf("loose file: %q %q", d, f)
	}
}

func TestResumedFolderContinuesInTheSameFolder(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "Album"), 0o755)
	os.Mkdir(filepath.Join(dir, "Album (1)"), 0o755) // made by the first attempt
	in := &Incoming{Offer: protocol.Offer{Files: []protocol.FileInfo{{Name: "Album/a.jpg"}, {Name: "Album/b.jpg"}}}}
	fm := in.folders(dir, map[int]*resumeFrom{0: {saved: filepath.Join(dir, "Album (1)", "a.jpg")}})
	if d, _, _ := fm.target("Album/b.jpg"); d != filepath.Join(dir, "Album (1)") {
		t.Fatalf("resumed into %q", d)
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
