package transfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/quic-go/quic-go"

	"TropicalPeer/internal/protocol"
	"TropicalPeer/internal/wire"
)

// SendOptions observes and steers an outgoing transfer. All fields are optional.
type SendOptions struct {
	Report func(Progress)
	// Upgrades delivers better connections to the same peer (e.g. direct
	// after starting on the relay). The transfer joins each one and moves
	// the remaining bytes onto it, mid-file if needed.
	Upgrades <-chan *quic.Conn
	// OnUpgraded runs once the transfer has moved to an upgraded connection.
	OnUpgraded func()
}

// sendSession is one outgoing transfer, possibly spanning several connections.
type sendSession struct {
	id      string
	results chan protocol.FileResult
	failed  chan error // a control stream died while it was the current one

	mu      sync.Mutex
	conns   []*quic.Conn
	current *quic.Conn
	ctrlGen int // bumped on every switch; only the current control stream's death is fatal

	pending atomic.Pointer[quic.Conn] // joined, waiting to take over at the next chunk
}

func (s *sendSession) all() []*quic.Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*quic.Conn(nil), s.conns...)
}

// readResults forwards a control stream's results. Its death only matters
// while it is still the current control stream.
func (s *sendSession) readResults(ctrl *wire.Conn, gen int) {
	for {
		var res protocol.FileResult
		if err := ctrl.Recv(&res); err != nil {
			s.mu.Lock()
			current := gen == s.ctrlGen
			s.mu.Unlock()
			if current {
				select {
				case s.failed <- err:
				default:
				}
			}
			return
		}
		s.results <- res
	}
}

// Source is one file to send: a name, an exact size, and a way to read its
// bytes from the start. It need not be a path on disk; on Android it is a
// content:// document opened through the system.
type Source struct {
	Name string
	Size int64
	// Open is called once per send (again on retry). The reader must yield
	// exactly Size bytes.
	Open func() (io.ReadCloser, error)
}

// FileSource describes a regular file on disk.
func FileSource(path string) (Source, error) {
	st, err := os.Stat(path)
	if err != nil {
		return Source{}, err
	}
	if !st.Mode().IsRegular() {
		return Source{}, fmt.Errorf("%s: only regular files can be sent for now", path)
	}
	return Source{
		Name: filepath.Base(path),
		Size: st.Size(),
		Open: func() (io.ReadCloser, error) { return os.Open(path) },
	}, nil
}

// FileSources describes several files on disk.
func FileSources(paths []string) ([]Source, error) {
	out := make([]Source, 0, len(paths))
	for _, p := range paths {
		src, err := FileSource(p)
		if err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, nil
}

// Send offers the files to the peer on conn and streams them if accepted.
// Cancelling ctx aborts the transfer by closing every connection (cancel it
// with cause ErrCancelled when a person cancelled). Calling Send again with
// the same transferID after a failure continues where the receiver stopped.
func Send(ctx context.Context, conn *quic.Conn, transferID string, files []Source, opts SendOptions) (err error) {
	s := &sendSession{
		id:      transferID,
		results: make(chan protocol.FileResult, 16),
		failed:  make(chan error, 1),
		conns:   []*quic.Conn{conn},
		current: conn,
	}
	defer abortOnCancel(ctx, s.all, &err)()

	offer := protocol.Offer{TransferID: transferID, Resumable: true}
	for _, f := range files {
		if f.Size < 0 || f.Open == nil {
			return fmt.Errorf("%s: can't tell how big this file is", f.Name)
		}
		offer.Files = append(offer.Files, protocol.FileInfo{Name: f.Name, Size: f.Size})
	}
	if len(offer.Files) == 0 {
		return errors.New("no files to send")
	}

	ctrlStream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return err
	}
	defer ctrlStream.Close()
	ctrl := wire.NewConn(ctrlStream)

	if err := ctrl.Send(protocol.Hello{Offer: &offer}); err != nil {
		return err
	}
	var resp protocol.Response
	if err := ctrl.Recv(&resp); err != nil {
		return fmt.Errorf("waiting for the other device to accept: %w", err)
	}
	if !resp.Accept {
		return &RejectedError{Reason: resp.Reason}
	}
	resume := map[int]protocol.ResumePoint{}
	for _, p := range resp.Resume {
		if p.Index >= 0 && p.Index < len(offer.Files) && p.Offset >= 0 && p.Offset <= offer.Files[p.Index].Size {
			resume[p.Index] = p
		}
	}
	go s.readResults(ctrl, 0)

	if opts.Upgrades != nil {
		go s.acceptUpgrades(ctx, opts.Upgrades)
	}

	total := offer.TotalSize()
	var done int64
	for i, f := range offer.Files {
		m := &meter{
			p:      Progress{TransferID: transferID, Direction: Sending, FileIndex: i, FileName: f.Name, FileSize: f.Size, Total: total},
			base:   done,
			report: opts.Report,
		}
		if p := resume[i]; p.Saved {
			// It arrived in full last time; the receiver expects nothing.
			m.start(f.Size)
			done += f.Size
			continue
		}
		if err := s.sendFile(ctx, i, files[i], m, opts.OnUpgraded, resume[i]); err != nil {
			return fmt.Errorf("%s: %w", f.Name, err)
		}
		res, err := s.nextResult(ctx)
		if err != nil {
			return fmt.Errorf("%s: waiting for confirmation: %w", f.Name, err)
		}
		if !res.OK || res.Index != i {
			return fmt.Errorf("%s: the other device reported: %s", f.Name, res.Error)
		}
		done += f.Size
	}
	return nil
}

// nextResult waits for the receiver's next file result. A control stream
// that ends right after delivering a result (the receiver closes it after
// the last file) must not hide that result: the reader queues the result
// before it reports the end, so a queued result always wins.
func (s *sendSession) nextResult(ctx context.Context) (protocol.FileResult, error) {
	select {
	case res := <-s.results:
		return res, nil
	case err := <-s.failed:
		select {
		case res := <-s.results:
			return res, nil
		default:
			return protocol.FileResult{}, err
		}
	case <-ctx.Done():
		return protocol.FileResult{}, ctx.Err()
	}
}

// acceptUpgrades joins each better connection to this transfer. Once the
// receiver acknowledges, the sender switches to it at the next chunk.
func (s *sendSession) acceptUpgrades(ctx context.Context, upgrades <-chan *quic.Conn) {
	for {
		var c *quic.Conn
		select {
		case <-ctx.Done():
			return
		case c = <-upgrades:
		}
		if c == nil {
			return
		}
		s.mu.Lock()
		s.conns = append(s.conns, c)
		s.mu.Unlock()

		stream, err := c.OpenStreamSync(ctx)
		if err != nil {
			continue
		}
		ctrl := wire.NewConn(stream)
		if err := ctrl.Send(protocol.Hello{Join: s.id}); err != nil {
			continue
		}
		var ack protocol.JoinAck
		if err := ctrl.Recv(&ack); err != nil || !ack.OK {
			continue
		}
		s.mu.Lock()
		s.ctrlGen++
		gen := s.ctrlGen
		s.mu.Unlock()
		go s.readResults(ctrl, gen)
		s.pending.Store(c)
	}
}

// sendFile streams one file as one or more segments, moving to a joined
// connection between chunks when one is pending. If the receiver holds the
// start of the file (from), the file continues after it.
func (s *sendSession) sendFile(ctx context.Context, index int, src Source, m *meter, onUpgraded func(), from protocol.ResumePoint) error {
	f, h, offset, err := openAt(src, from)
	if err != nil {
		return err
	}
	defer f.Close()
	size := src.Size
	m.start(offset)

	for {
		if next := s.pending.Swap(nil); next != nil {
			s.mu.Lock()
			s.current = next
			s.mu.Unlock()
			if onUpgraded != nil {
				onUpgraded()
			}
		}
		s.mu.Lock()
		conn := s.current
		s.mu.Unlock()

		finished, n, err := s.sendSegment(ctx, conn, index, f, offset, size, h, m)
		offset += n
		if err != nil {
			return err
		}
		if finished {
			m.finish()
			return nil
		}
	}
}

// openAt opens src where the receiver can continue: after the prefix it
// holds if that matches this file, else at the start. The returned hash
// already covers the bytes before the returned offset.
func openAt(src Source, from protocol.ResumePoint) (io.ReadCloser, hash.Hash, int64, error) {
	f, err := src.Open()
	if err != nil {
		return nil, nil, 0, err
	}
	h := sha256.New()
	if from.Offset <= 0 {
		return f, h, 0, nil
	}
	if _, err := io.CopyN(h, f, from.Offset); err == nil && bytes.Equal(h.Sum(nil), from.Prefix) {
		return f, h, from.Offset, nil
	}
	// What the receiver holds is not the start of this file: start over.
	// The receiver sees a segment at 0 and discards its part.
	f.Close()
	if f, err = src.Open(); err != nil {
		return nil, nil, 0, err
	}
	return f, sha256.New(), 0, nil
}

// sendSegment writes one segment starting at offset. It stops early (cut,
// finished=false) when a better connection is waiting.
func (s *sendSession) sendSegment(ctx context.Context, conn *quic.Conn, index int, f io.Reader, offset, size int64, h hash.Hash, m *meter) (finished bool, sent int64, err error) {
	st, err := conn.OpenUniStreamSync(ctx)
	if err != nil {
		return false, 0, err
	}
	if err := wire.Write(st, protocol.FileHeader{Index: index, Offset: offset}); err != nil {
		st.CancelWrite(0)
		return false, 0, err
	}
	out := io.MultiWriter(st, h, m)
	buf := make([]byte, copyBufferSize)
	for offset+sent < size {
		if s.pending.Load() != nil {
			// Cut here: the rest goes over the better connection.
			return false, sent, st.Close()
		}
		want := min(int64(len(buf)), size-offset-sent)
		n, rerr := io.ReadFull(f, buf[:want])
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				st.CancelWrite(0)
				return false, sent, werr
			}
			sent += int64(n)
		}
		if rerr != nil {
			st.CancelWrite(0)
			return false, sent, fmt.Errorf("file changed while sending or read failed: %w", rerr)
		}
	}
	if _, err := st.Write(h.Sum(nil)); err != nil {
		return false, sent, err
	}
	return true, sent, st.Close()
}
