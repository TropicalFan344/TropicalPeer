package transfer

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// markDownloaded sets the quarantine flag browsers set on downloads, so
// Gatekeeper checks a received app or script before it first runs.
func markDownloaded(path string) {
	value := fmt.Sprintf("0081;%08x;TropicalPeer;", time.Now().Unix())
	unix.Setxattr(path, "com.apple.quarantine", []byte(value), 0)
}
