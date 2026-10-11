// Package transfer moves files between two connected peers using package protocol.
package transfer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
)

type Direction string

const (
	Sending   Direction = "send"
	Receiving Direction = "receive"
)

// Progress is reported while a transfer runs. The GUI turns it into progress bars.
type Progress struct {
	TransferID string
	Direction  Direction
	FileIndex  int
	FileName   string
	FileDone   int64
	FileSize   int64
	Done       int64 // across all files
	Total      int64
}

// RejectedError is returned to the sender when the receiver declines.
type RejectedError struct{ Reason string }

func (e *RejectedError) Error() string {
	if e.Reason == "" {
		return "the other device declined the transfer"
	}
	return "the other device declined the transfer: " + e.Reason
}

// limits on what a receiver will even consider.
const (
	MaxFilesPerOffer = 10000
	// maxControlLine bounds a message on a transfer's control stream: an
	// offer of MaxFilesPerOffer files with their folder paths fits.
	maxControlLine   = 4 << 20
	progressInterval = 100 * time.Millisecond
	copyBufferSize   = 256 * 1024
)

func NewTransferID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// meter counts bytes as they flow and reports progress at a bounded rate.
type meter struct {
	p      Progress
	base   int64 // Done before this file started
	n      atomic.Int64
	last   time.Time
	report func(Progress)
}

func (m *meter) Write(b []byte) (int, error) {
	n := m.n.Add(int64(len(b)))
	if m.report != nil && time.Since(m.last) >= progressInterval {
		m.last = time.Now()
		m.emit(n)
	}
	return len(b), nil
}

// start counts n bytes as already there (a resumed file's first part).
func (m *meter) start(n int64) {
	m.n.Store(n)
	if m.report != nil {
		m.emit(n)
	}
}

func (m *meter) emit(n int64) {
	p := m.p
	p.FileDone = n
	p.Done = m.base + n
	m.report(p)
}

func (m *meter) finish() {
	if m.report != nil {
		m.emit(m.n.Load())
	}
}

// QUIC application error codes a transfer's connections close with.
const (
	// ErrCodeCancelled: someone cancelled the transfer. Partial files are
	// discarded and the sender doesn't try again.
	ErrCodeCancelled quic.ApplicationErrorCode = 1
	// ErrCodeStopped: the app on that side is quitting. The transfer can
	// pick up where it stopped later.
	ErrCodeStopped quic.ApplicationErrorCode = 2
	// ErrCodePaused: a person paused the transfer. Both sides keep what
	// arrived; it continues when either side resumes it.
	ErrCodePaused quic.ApplicationErrorCode = 3
)

// ErrCancelled is the cause to cancel a transfer's context with when a person
// cancels it (context.WithCancelCause). Any other cancellation, such as the
// app quitting, counts as stopping: the transfer may resume later.
var ErrCancelled = errors.New("transfer cancelled")

// ErrPaused is the cause to cancel a transfer's context with when a person
// pauses it.
var ErrPaused = errors.New("transfer paused")

// ErrCorrupted means a file's checksum didn't match; it has been discarded.
var ErrCorrupted = errors.New("checksum mismatch, file corrupted in transit")

// ErrOfferTooBig: the list of files is too long to offer in one transfer.
var ErrOfferTooBig = errors.New("too many files for one transfer")

// ErrOfferTooBigForPeer: the other device runs a version that can't take a
// list of files this long.
var ErrOfferTooBigForPeer = errors.New("the other device can't take this many files at once")

// IsRefusedByPeer reports whether the other device refused our certificate:
// it doesn't know this device (any more), because it removed us or lost
// its peers. TLS sends bad_certificate (42) or certificate_unknown (46).
func IsRefusedByPeer(err error) bool {
	var te *quic.TransportError
	if errors.As(err, &te) && te.Remote {
		const cryptoError = 0x100 // QUIC carries TLS alert n as 0x100+n
		code := te.ErrorCode
		return code == cryptoError+42 || code == cryptoError+46
	}
	return false
}

// encodedSize is how long m is on the wire.
func encodedSize(m any) int {
	b, _ := json.Marshal(m)
	return len(b) + 1
}

// abortOnCancel closes every connection of a transfer as soon as ctx is
// cancelled, because stream reads and writes don't watch ctx. The returned
// func must be deferred; it stops the watcher and, if ctx was the reason for
// failure, replaces *err with ctx.Err().
func abortOnCancel(ctx context.Context, conns func() []*quic.Conn, err *error) func() {
	stop := context.AfterFunc(ctx, func() {
		code, msg := ErrCodeStopped, "stopped"
		switch cause := context.Cause(ctx); {
		case errors.Is(cause, ErrCancelled):
			code, msg = ErrCodeCancelled, "cancelled"
		case errors.Is(cause, ErrPaused):
			code, msg = ErrCodePaused, "paused"
		}
		for _, c := range conns() {
			c.CloseWithError(code, msg)
		}
	})
	return func() {
		stop()
		if *err != nil && ctx.Err() != nil {
			*err = ctx.Err()
		}
	}
}

// IsCancelledByPeer reports whether err means the other device cancelled.
func IsCancelledByPeer(err error) bool {
	code, ok := PeerCloseCode(err)
	return ok && code == ErrCodeCancelled
}

// IsPausedByPeer reports whether err means the other device paused.
func IsPausedByPeer(err error) bool {
	code, ok := PeerCloseCode(err)
	return ok && code == ErrCodePaused
}

// remoteClose matches how quic-go words a connection the other side closed.
var remoteClose = regexp.MustCompile(`Application error (0x[0-9a-fA-F]+) \(remote\)`)

// PeerCloseCode returns the code the other device closed the connection
// with, if that is what err is about. It reads the structured QUIC error,
// and falls back to its wording should a layer in between have kept only
// the text: a pause or cancel from the other side must never surface as a
// raw connection error.
func PeerCloseCode(err error) (quic.ApplicationErrorCode, bool) {
	if err == nil {
		return 0, false
	}
	var appErr *quic.ApplicationError
	if errors.As(err, &appErr) {
		return appErr.ErrorCode, appErr.Remote
	}
	if m := remoteClose.FindStringSubmatch(err.Error()); m != nil {
		if n, perr := strconv.ParseUint(m[1], 0, 64); perr == nil {
			return quic.ApplicationErrorCode(n), true
		}
	}
	return 0, false
}

// Describe spells out err with the type of every error in its chain, for
// logs that must show why a transfer ended the way it did.
func Describe(err error) string {
	if err == nil {
		return "<nil>"
	}
	var types []string
	for e := err; e != nil; e = errors.Unwrap(e) {
		types = append(types, fmt.Sprintf("%T", e))
	}
	return fmt.Sprintf("%v [%s]", err, strings.Join(types, " > "))
}

// copyExactly copies exactly n bytes, failing on a short stream.
func copyExactly(dst io.Writer, src io.Reader, n int64) error {
	buf := make([]byte, copyBufferSize)
	written, err := io.CopyBuffer(dst, io.LimitReader(src, n), buf)
	if err != nil {
		return err
	}
	if written != n {
		return fmt.Errorf("stream ended after %d of %d bytes", written, n)
	}
	return nil
}
