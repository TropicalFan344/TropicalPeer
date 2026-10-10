package transfer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/quic-go/quic-go"

	"TropicalPeer/internal/protocol"
	"TropicalPeer/internal/wire"
)

// Decider is asked whether to accept an offer. It may block (e.g. while the
// user decides) and should return promptly when ctx is cancelled.
type Decider func(ctx context.Context, offer protocol.Offer) (accept bool, reason string)

// Partials remembers what interrupted transfers left behind, so a later
// attempt can continue instead of starting over. Without one (Incoming.Parts
// nil) an interrupted file is deleted.
type Partials interface {
	// Find returns the .part file an earlier attempt left for this file and
	// the path it was meant to be saved as.
	Find(transferID string, index int, f protocol.FileInfo) (part, final string, ok bool)
	// Saved returns where an earlier attempt of this same transfer saved
	// file index in full.
	Saved(transferID string, index int) (path string, ok bool)
	// Started records that part is being written for file index.
	Started(transferID string, index int, f protocol.FileInfo, part, final string)
	// Finished records file index saved in full at path; its part is gone.
	Finished(transferID string, index int, path string)
	// Discard forgets part, which has been deleted.
	Discard(part string)
}

// resumeFrom is what an earlier attempt left of one file.
type resumeFrom struct {
	saved       string // the whole file, saved at this path
	part, final string // or a part file, holding offset bytes
	offset      int64
	h           hash.Hash // over those bytes
}

// Result describes one received file.
type Result struct {
	Name string // as offered
	Path string // where it was saved
}

// Control is an accepted control stream and the Hello it opened with.
type Control struct {
	Hello  protocol.Hello
	stream *quic.Stream
	conn   *quic.Conn
	wire   *wire.Conn
}

// AcceptControl waits for the control stream a sender opens on a new
// connection and reads its Hello: a new offer or a join of a running transfer.
func AcceptControl(ctx context.Context, conn *quic.Conn) (*Control, error) {
	st, err := conn.AcceptStream(ctx)
	if err != nil {
		return nil, err
	}
	c := &Control{stream: st, conn: conn, wire: wire.NewConn(st)}
	if err := c.wire.Recv(&c.Hello); err != nil {
		st.CancelRead(0)
		return nil, err
	}
	if c.Hello.Offer == nil && c.Hello.Join == "" && c.Hello.Resume == "" && c.Hello.Cancel == "" {
		st.CancelRead(0)
		return nil, errors.New("control stream opened without an offer, join, resume or cancel")
	}
	return c, nil
}

// Answer replies to a resume request: whether the transfer is coming.
func (c *Control) Answer(ok bool, reason string) {
	c.wire.Send(protocol.JoinAck{OK: ok, Error: reason})
	c.stream.Close()
}

// RequestResume asks the sender on conn to continue transferID.
func RequestResume(ctx context.Context, conn *quic.Conn, transferID string) error {
	return ask(ctx, conn, protocol.Hello{Resume: transferID})
}

// NotifyCancel tells the peer on conn that transferID (the sender's ID) was
// cancelled here while the two weren't connected.
func NotifyCancel(ctx context.Context, conn *quic.Conn, transferID string) error {
	return ask(ctx, conn, protocol.Hello{Cancel: transferID})
}

// ask opens a control stream with hello and waits for the JoinAck.
func ask(ctx context.Context, conn *quic.Conn, hello protocol.Hello) error {
	st, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return err
	}
	defer st.Close()
	w := wire.NewConn(st)
	if err := w.Send(hello); err != nil {
		return err
	}
	var ack protocol.JoinAck
	if err := w.Recv(&ack); err != nil {
		return fmt.Errorf("waiting for an answer: %w", err)
	}
	if !ack.OK {
		return &ResumeRefusedError{Reason: ack.Error}
	}
	return nil
}

// ResumeRefusedError: the sender can't continue that transfer (it no longer
// has it, or it was cancelled there).
type ResumeRefusedError struct{ Reason string }

func (e *ResumeRefusedError) Error() string { return "the other device can't resume it: " + e.Reason }

// RejectJoin answers a join that matches no running transfer.
func (c *Control) RejectJoin(reason string) {
	c.wire.Send(protocol.JoinAck{OK: false, Error: reason})
	c.stream.Close()
}

type segment struct {
	hdr    protocol.FileHeader
	r      *bufio.Reader
	stream *quic.ReceiveStream
}

// Incoming is one transfer being received, possibly over several
// connections (it starts on one and may be joined by better ones).
type Incoming struct {
	Offer protocol.Offer
	// Parts, if set, keeps interrupted files for a later attempt. Set it
	// before Receive.
	Parts Partials

	segments chan segment
	connDone chan struct{} // signalled when a connection's segment reader stops
	live     atomic.Int32  // segment readers still running

	mu      sync.Mutex
	conns   []*quic.Conn
	ctrl    *wire.Conn // the control stream results go to (latest joined)
	ctrls   []*quic.Stream
	started bool // segment readers running (set once the offer is accepted)
}

// NewIncoming starts receiving the transfer offered on c.
func NewIncoming(c *Control) *Incoming {
	in := &Incoming{
		Offer:    *c.Hello.Offer,
		segments: make(chan segment, 64),
		connDone: make(chan struct{}, 1),
		conns:    []*quic.Conn{c.conn},
		ctrl:     c.wire,
		ctrls:    []*quic.Stream{c.stream},
	}
	return in
}

func (in *Incoming) all() []*quic.Conn {
	in.mu.Lock()
	defer in.mu.Unlock()
	return append([]*quic.Conn(nil), in.conns...)
}

func (in *Incoming) sendCtrl(m any) error {
	in.mu.Lock()
	ctrl := in.ctrl
	in.mu.Unlock()
	return ctrl.Send(m)
}

// startReader runs readSegments for conn, counting it as live.
func (in *Incoming) startReader(ctx context.Context, conn *quic.Conn) {
	in.live.Add(1)
	go func() {
		defer func() {
			in.live.Add(-1)
			select {
			case in.connDone <- struct{}{}:
			default:
			}
		}()
		in.readSegments(ctx, conn)
	}()
}

// readSegments feeds every segment stream arriving on conn into the session.
func (in *Incoming) readSegments(ctx context.Context, conn *quic.Conn) {
	for {
		st, err := conn.AcceptUniStream(ctx)
		if err != nil {
			return
		}
		br := bufio.NewReaderSize(st, copyBufferSize)
		line, err := wire.ReadLine(br)
		if err != nil {
			st.CancelRead(0)
			continue
		}
		var hdr protocol.FileHeader
		if err := json.Unmarshal(line, &hdr); err != nil {
			st.CancelRead(0)
			continue
		}
		select {
		case in.segments <- segment{hdr: hdr, r: br, stream: st}:
		case <-ctx.Done():
			st.CancelRead(0)
			return
		}
	}
}

// Stop closes the transfer's connections, telling the sender this device is
// stopping (as opposed to cancelling): the transfer can resume later.
func (in *Incoming) Stop() {
	for _, c := range in.all() {
		c.CloseWithError(ErrCodeStopped, "stopped")
	}
}

// Join adds a better connection to the running transfer: segments may now
// arrive on it, and results go to its control stream.
func (in *Incoming) Join(ctx context.Context, c *Control) error {
	in.mu.Lock()
	in.conns = append(in.conns, c.conn)
	in.ctrl = c.wire
	in.ctrls = append(in.ctrls, c.stream)
	started := in.started
	in.mu.Unlock()
	if started {
		in.startReader(ctx, c.conn)
	} // otherwise Receive starts it with the rest
	return c.wire.Send(protocol.JoinAck{OK: true})
}

// Receive asks decide about the offer and, if accepted, saves the files in
// saveDir. decideCtx bounds only the decision (it should end when the first
// connection closes, so a prompt goes away if the sender gives up); ctx
// bounds the whole transfer.
func (in *Incoming) Receive(ctx, decideCtx context.Context, saveDir string, decide Decider, report func(Progress)) (_ []Result, err error) {
	defer abortOnCancel(ctx, in.all, &err)()
	defer func() {
		in.mu.Lock()
		for _, st := range in.ctrls {
			st.Close()
		}
		in.mu.Unlock()
	}()

	offer := in.Offer
	if err := validate(offer); err != nil {
		in.sendCtrl(protocol.Response{Accept: false, Reason: err.Error()})
		return nil, err
	}
	accept, reason := decide(decideCtx, offer)
	if !accept {
		in.sendCtrl(protocol.Response{Accept: false, Reason: reason})
		return nil, &RejectedError{Reason: reason}
	}
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		in.sendCtrl(protocol.Response{Accept: false, Reason: err.Error()})
		return nil, err
	}
	resume, points := in.leftovers()
	if err := in.sendCtrl(protocol.Response{Accept: true, Resume: points}); err != nil {
		return nil, err
	}
	in.mu.Lock()
	in.started = true
	conns := append([]*quic.Conn(nil), in.conns...)
	in.mu.Unlock()
	for _, c := range conns {
		in.startReader(ctx, c)
	}

	total := offer.TotalSize()
	var done int64
	var results []Result
	held := map[[2]int64]segment{} // segments that arrived ahead of their turn
	for i, f := range offer.Files {
		m := &meter{
			p:      Progress{TransferID: offer.TransferID, Direction: Receiving, FileIndex: i, FileName: f.Name, FileSize: f.Size, Total: total},
			base:   done,
			report: report,
		}
		if r := resume[i]; r != nil && r.saved != "" {
			// Saved in full by an earlier attempt; the sender skips it.
			m.start(f.Size)
			results = append(results, Result{Name: f.Name, Path: r.saved})
			done += f.Size
			continue
		}
		path, err := in.receiveFile(ctx, i, f, saveDir, m, held, resume[i])
		res := protocol.FileResult{Index: i, OK: err == nil}
		if err != nil {
			res.Error = err.Error()
		} else {
			res.SavedAs = filepath.Base(path)
		}
		if sendErr := in.sendCtrl(res); sendErr != nil && err == nil {
			err = sendErr
		}
		if err != nil {
			return results, fmt.Errorf("%s: %w", f.Name, err)
		}
		results = append(results, Result{Name: f.Name, Path: path})
		done += f.Size
	}
	return results, nil
}

// leftovers looks up what earlier attempts left of each file and the resume
// points to tell the sender. Only a sender that can resume hears of them.
func (in *Incoming) leftovers() (map[int]*resumeFrom, []protocol.ResumePoint) {
	if in.Parts == nil || !in.Offer.Resumable {
		return nil, nil
	}
	resume := map[int]*resumeFrom{}
	var points []protocol.ResumePoint
	for i, f := range in.Offer.Files {
		id := in.Offer.TransferID
		if path, ok := in.Parts.Saved(id, i); ok {
			if st, err := os.Stat(path); err == nil && st.Size() == f.Size {
				resume[i] = &resumeFrom{saved: path}
				points = append(points, protocol.ResumePoint{Index: i, Saved: true})
				continue
			}
		}
		part, final, ok := in.Parts.Find(id, i, f)
		if !ok {
			continue
		}
		r, err := hashPart(part, f.Size)
		if err != nil {
			os.Remove(part)
			in.Parts.Discard(part)
			continue
		}
		r.final = final
		resume[i] = r
		if r.offset > 0 {
			points = append(points, protocol.ResumePoint{Index: i, Offset: r.offset, Prefix: r.h.Sum(nil)})
		}
	}
	return resume, points
}

// hashPart reads a part file back so the transfer can continue it.
func hashPart(part string, size int64) (*resumeFrom, error) {
	f, err := os.Open(part)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.CopyBuffer(h, f, make([]byte, copyBufferSize))
	if err != nil {
		return nil, err
	}
	if n > size {
		return nil, errors.New("part file is bigger than the file")
	}
	return &resumeFrom{part: part, offset: n, h: h}, nil
}

// nextSegment returns the segment for file index at offset, holding any
// that arrive early (a cut segment's successor may overtake it). When
// restart is set, a segment at offset 0 is accepted too: the sender chose to
// start a resumed file over.
func (in *Incoming) nextSegment(ctx context.Context, index int, offset int64, restart bool, held map[[2]int64]segment) (segment, error) {
	key := [2]int64{int64(index), offset}
	alt := [2]int64{int64(index), 0}
	for _, k := range [][2]int64{key, alt} {
		if s, ok := held[k]; ok && (k == key || restart) {
			delete(held, k)
			return s, nil
		}
	}
	for {
		select {
		case s := <-in.segments:
			k := [2]int64{int64(s.hdr.Index), s.hdr.Offset}
			if k == key || (restart && k == alt) {
				return s, nil
			}
			if s.hdr.Index < index || len(held) > 64 {
				s.stream.CancelRead(0) // stale or flooding
				continue
			}
			held[k] = s
		case <-in.connDone:
			if in.live.Load() == 0 {
				return segment{}, errors.New("connection lost")
			}
		case <-ctx.Done():
			return segment{}, ctx.Err()
		}
	}
}

func (in *Incoming) receiveFile(ctx context.Context, index int, f protocol.FileInfo, dir string, m *meter, held map[[2]int64]segment, from *resumeFrom) (string, error) {
	var (
		final, part string
		out         *os.File
		h           = sha256.New()
		offset      int64
		err         error
	)
	if from != nil {
		if out, err = os.OpenFile(from.part, os.O_WRONLY, 0); err == nil {
			if _, err = out.Seek(from.offset, io.SeekStart); err != nil {
				out.Close()
				out = nil
			}
		}
		if out != nil {
			final, part, h, offset = from.final, from.part, from.h, from.offset
		} else {
			os.Remove(from.part)
			in.Parts.Discard(from.part)
		}
	}
	if out == nil {
		if final, part, out, err = createUnique(dir, SafeName(f.Name)); err != nil {
			return "", err
		}
	}
	if in.Parts != nil {
		in.Parts.Started(in.Offer.TransferID, index, f, part, final)
	}
	m.start(offset)
	// fail keeps the part for a later attempt, unless it is corrupt or no
	// later attempt can find it.
	fail := func(err error) (string, error) {
		out.Close()
		if in.Parts == nil || errors.Is(err, ErrCorrupted) {
			os.Remove(part)
			if in.Parts != nil {
				in.Parts.Discard(part)
			}
		}
		return "", err
	}

	restart := offset > 0
	for {
		seg, err := in.nextSegment(ctx, index, offset, restart, held)
		if err != nil {
			return fail(err)
		}
		if restart && seg.hdr.Offset == 0 && offset > 0 {
			// The sender's file doesn't start with what we kept.
			if err := out.Truncate(0); err != nil {
				return fail(err)
			}
			if _, err := out.Seek(0, io.SeekStart); err != nil {
				return fail(err)
			}
			h, offset = sha256.New(), 0
			m.start(0)
		}
		restart = false
		n, err := io.CopyBuffer(io.MultiWriter(out, h, m), io.LimitReader(seg.r, f.Size-offset), make([]byte, copyBufferSize))
		offset += n
		if err != nil {
			seg.stream.CancelRead(0)
			return fail(err)
		}
		if offset < f.Size {
			// The stream ended early: a cut segment. The rest follows in
			// another segment, possibly on another connection.
			continue
		}
		sum := make([]byte, protocol.SHA256Size)
		if _, err := io.ReadFull(seg.r, sum); err != nil {
			seg.stream.CancelRead(0)
			return fail(err)
		}
		if !bytes.Equal(sum, h.Sum(nil)) {
			return fail(ErrCorrupted)
		}
		break
	}
	if err := out.Close(); err != nil {
		return fail(err)
	}
	m.finish()
	// A file of that name may have appeared since the part was started.
	if _, err := os.Stat(final); err == nil {
		final = freeName(final)
	}
	if err := os.Rename(part, final); err != nil {
		return fail(err)
	}
	if in.Parts != nil {
		in.Parts.Finished(in.Offer.TransferID, index, final)
	}
	return final, nil
}

// freeName returns path, or "name (1).ext", ... if that is taken.
func freeName(path string) string {
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	for i := 1; i < 10000; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
	}
	return path
}

func validate(o protocol.Offer) error {
	if len(o.Files) == 0 || len(o.Files) > maxFilesPerOffer {
		return fmt.Errorf("offer must contain 1 to %d files", maxFilesPerOffer)
	}
	for _, f := range o.Files {
		if f.Size < 0 {
			return errors.New("negative file size")
		}
		if SafeName(f.Name) == "" {
			return fmt.Errorf("invalid file name %q", f.Name)
		}
	}
	return nil
}

// createUnique picks "name.ext", "name (1).ext", ... that doesn't exist yet and
// creates its ".part" file exclusively.
func createUnique(dir, name string) (final, part string, f *os.File, err error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; i < 10000; i++ {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		final = filepath.Join(dir, candidate)
		part = final + ".part"
		if _, statErr := os.Stat(final); statErr == nil {
			continue
		}
		f, err = os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return final, part, f, err
	}
	return "", "", nil, errors.New("could not find a free file name")
}

var reservedWindowsNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// SafeName reduces a sender-supplied name to a plain file name that cannot
// escape the download folder and is valid on Windows. It returns "" if
// nothing usable is left.
func SafeName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 32 || r == 127, strings.ContainsRune(`<>:"|?*`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	name = strings.TrimRight(strings.TrimSpace(b.String()), ". ")
	if name == "" || name == "." || name == ".." {
		return ""
	}
	stem := strings.ToUpper(strings.TrimSuffix(name, filepath.Ext(name)))
	if reservedWindowsNames[stem] {
		name = "_" + name
	}
	if len(name) > 200 {
		ext := filepath.Ext(name)
		if len(ext) > 20 {
			ext = ""
		}
		name = strings.ToValidUTF8(name[:200-len(ext)], "") + ext
	}
	return name
}
