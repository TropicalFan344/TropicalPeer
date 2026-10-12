package desktopapp

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
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
