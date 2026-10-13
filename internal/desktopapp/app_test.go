package desktopapp

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"TropicalPeer/internal/appcore"
)

func TestNearestExistingDir(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "Downloads")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(sub, "a.txt")
	os.WriteFile(file, nil, 0o644)

	cases := map[string]string{
		sub:                                    sub, // exists
		filepath.Join(sub, "TropicalPeer"):     sub, // missing: use parent
		filepath.Join(sub, "x", "y", "z"):      sub, // several levels missing
		file:                                   sub, // a file, not a folder
		"":                                     "",  // nothing given
		filepath.Join("Q:\\", "nope", "there"): "",  // no existing ancestor (drive missing)
	}
	for in, want := range cases {
		if in != "" && in[0] == 'Q' {
			if runtime.GOOS != "windows" {
				continue // a drive letter is just a relative name elsewhere
			}
			if _, err := os.Stat("Q:\\"); err == nil {
				continue // this machine actually has a Q: drive
			}
		}
		if got := nearestExistingDir(in); got != want {
			t.Errorf("nearestExistingDir(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanUpDownloadsKeepsNewerVersions(t *testing.T) {
	old := appcore.Version
	appcore.Version = "0.1.11"
	defer func() { appcore.Version = old }()
	dir := t.TempDir()
	for _, name := range []string{"TropicalPeer-0.1.12.dmg", "TropicalPeer-0.1.13.dmg.part", "TropicalPeer-0.1.11.dmg", "TropicalPeer-0.1.9.exe", "junk"} {
		os.WriteFile(filepath.Join(dir, name), nil, 0o600)
	}
	cleanUpDownloads(dir)
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "TropicalPeer-0.1.12.dmg" {
		t.Fatalf("left %v; want only the finished newer download", entries)
	}
}
