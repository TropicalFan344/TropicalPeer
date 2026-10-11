package pairing

import (
	"strings"
	"testing"
)

func TestCleanName(t *testing.T) {
	if got := cleanName("Moon‮enohp"); strings.ContainsRune(got, '‮') {
		t.Fatalf("direction override kept: %q", got)
	}
	if got := cleanName(strings.Repeat("x", 500)); len([]rune(got)) != maxNameRunes {
		t.Fatalf("long name kept %d characters", len([]rune(got)))
	}
	if got := cleanName("\n\t "); got != "TropicalPeer device" {
		t.Fatalf("empty name became %q", got)
	}
	if got := cleanName("Galaxy A55 5G"); got != "Galaxy A55 5G" {
		t.Fatalf("plain name changed to %q", got)
	}
}
