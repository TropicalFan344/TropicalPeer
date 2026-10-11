// Package protocol defines the peer-to-peer file transfer protocol, spoken over
// QUIC connections whose TLS layer has already pinned both devices' keys.
//
//	Sender                                     Receiver
//	  │── control stream: Hello{Offer} ───────────▶│  policy: auto / ask / block
//	  │◀───────────────────────── Response ────────│
//	  │══ uni stream per segment: FileHeader,      │
//	  │   <raw bytes from Offset> ════════════════▶│  write .part at Offset
//	  │   …the segment that reaches the end of the │
//	  │   file is followed by its 32-byte SHA-256  │  verify, rename
//	  │◀──────────────────────── FileResult ───────│  (one per file, on control)
//
// A segment that ends (stream FIN) before the end of the file was cut: the
// file continues in a new segment at the next offset. That is how a transfer
// moves to a better path mid-file (relay → direct): the sender opens a second
// connection, sends Hello{Join: transferID} on its control stream, waits for
// JoinAck, cuts the current segment and carries on over the new connection.
// Results then arrive on whichever control stream the receiver joined last.
//
// Resuming: a sender that keeps its transfer ID when it tries again marks
// the offer Resumable. The receiver then lists, in its Response, what an
// earlier attempt left behind: files already saved (skipped entirely, with no
// FileResult) and partial files, with the SHA-256 of the bytes it holds. The
// sender hashes the same prefix of its own file; if they match it continues
// at that offset, otherwise it starts the file over with a segment at 0.
// Partial files of an older offer with the same name and size are offered
// too, so sending a file again after both apps restarted also continues it.
package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

const SHA256Size = 32

type FileInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type Offer struct {
	TransferID string     `json:"transferId"`
	Files      []FileInfo `json:"files"`
	// Resumable says the sender understands Response.Resume.
	Resumable bool `json:"resumable,omitempty"`
}

// Fingerprint identifies exactly which files an offer lists (names and
// sizes, in order), so a later offer can be checked against one already
// accepted.
func (o Offer) Fingerprint() string {
	h := sha256.New()
	for _, f := range o.Files {
		fmt.Fprintf(h, "%d:%s %d;", len(f.Name), f.Name, f.Size)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (o Offer) TotalSize() int64 {
	var n int64
	for _, f := range o.Files {
		n += f.Size
	}
	return n
}

// Hello opens every control stream: a new offer, a join of an existing
// transfer from a new connection, a receiver's request to resume one, or
// news that one was cancelled while nothing connected the two sides.
type Hello struct {
	Offer *Offer `json:"offer,omitempty"`
	Join  string `json:"join,omitempty"` // transfer ID
	// Resume asks the sender to continue a paused or interrupted transfer
	// (by its transfer ID); the receiver sends it. Answered with a JoinAck.
	Resume string `json:"resume,omitempty"`
	// Cancel tells the other side that a transfer with no connection left
	// (paused or interrupted) was cancelled here, by the sender's transfer
	// ID. Either side sends it. Answered with a JoinAck.
	Cancel string `json:"cancel,omitempty"`
}

type Response struct {
	Accept bool   `json:"accept"`
	Reason string `json:"reason,omitempty"`
	// Resume lists what the receiver kept from an earlier attempt. Only
	// sent in answer to a Resumable offer.
	Resume []ResumePoint `json:"resume,omitempty"`
}

// ResumePoint says the receiver holds the first Offset bytes of file Index,
// whose SHA-256 is Prefix. Saved means the whole file arrived in an earlier
// attempt of this same transfer: the sender skips it and no FileResult
// follows.
type ResumePoint struct {
	Index  int    `json:"index"`
	Offset int64  `json:"offset,omitempty"`
	Prefix []byte `json:"prefix,omitempty"`
	Saved  bool   `json:"saved,omitempty"`
}

// JoinAck answers a join. Once the sender has it, it may send segments on
// the new connection.
type JoinAck struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// FileHeader opens each segment stream.
type FileHeader struct {
	Index  int   `json:"index"`
	Offset int64 `json:"offset"`
}

type FileResult struct {
	Index int    `json:"index"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// SavedAs is the file name the receiver actually used (it may add " (1)").
	SavedAs string `json:"savedAs,omitempty"`
}
