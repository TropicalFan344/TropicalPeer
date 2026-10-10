package node

import (
	"os"
	"path/filepath"
	"testing"
)

// Settings edited by hand on Windows often start with a UTF-8 BOM.
func TestSettingsWithBOM(t *testing.T) {
	dir := t.TempDir()
	data := append([]byte{0xEF, 0xBB, 0xBF}, `{"serverAddr":"example.org:9000","port":1234,"downloadDir":"D:\\in"}`...)
	if err := os.WriteFile(filepath.Join(dir, settingsFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := loadSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.ServerAddr != "example.org:9000" || s.Port != 1234 {
		t.Fatalf("got %+v", s)
	}
}
