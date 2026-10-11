package transfer

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/quic-go/quic-go"
)

func TestPeerCloseCode(t *testing.T) {
	paused := &quic.ApplicationError{Remote: true, ErrorCode: ErrCodePaused, ErrorMessage: "paused"}
	wrapped := fmt.Errorf("driver.exe: %w", paused)
	if !IsPausedByPeer(wrapped) || IsCancelledByPeer(wrapped) {
		t.Fatal("a wrapped remote pause isn't recognised")
	}
	// Only the wording survived (some layer kept the text): still a pause.
	textOnly := errors.New("whql-amd-software-adrenalin-edition-26.9.1-win11-b.exe: Application error 0x3 (remote): paused")
	if !IsPausedByPeer(textOnly) {
		t.Fatal("a pause known only by its wording isn't recognised")
	}
	if !IsCancelledByPeer(errors.New("x: Application error 0x1 (remote): cancelled")) {
		t.Fatal("a cancel known only by its wording isn't recognised")
	}
	// Our own pause, or another code, isn't the other side pausing.
	local := &quic.ApplicationError{Remote: false, ErrorCode: ErrCodePaused}
	for _, err := range []error{local, errors.New("Application error 0x3 (local): paused"), errors.New("Application error 0x2 (remote): stopped"), nil} {
		if IsPausedByPeer(err) {
			t.Fatalf("%v counted as paused by the other side", err)
		}
	}
	if d := Describe(wrapped); d == "" || !strings.Contains(d, "*fmt.wrapError > *qerr.ApplicationError") {
		t.Fatalf("Describe = %q", d)
	}
}
