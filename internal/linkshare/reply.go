package linkshare

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"TropicalPeer/internal/protocol"
	"TropicalPeer/internal/transfer"
)

// Replies: someone who opened the link sends files back. Unlike a download,
// this writes to this device on the word of whoever has the link, so it is
// off unless the link's owner turns it on, every batch is put to the owner,
// and nothing about a batch is taken on trust (see docs/plan-link-reply.md).

const (
	// maxReplyFiles keeps an offer inside one DataChannel message (64 KiB).
	maxReplyFiles = 100
	// maxAsking is how many prompts one link can have open at once; a link
	// that asks for files expects several people to answer.
	maxAsking        = 2
	maxAskingCollect = 8
	// maxDeclined is how many times one page can be turned down before it
	// isn't put to the owner any more, and maxUnanswered how many prompts it
	// can let run out.
	maxDeclined    = 3
	maxUnanswered  = 5
	maxVisitorNote = 200 // characters
	drainFor       = 30 * time.Second
)

// replyStall is how long a batch may go silent (a variable for tests).
var replyStall = 60 * time.Second

// freeSpace is transfer.FreeSpace (a variable for tests).
var freeSpace = transfer.FreeSpace

// Why a batch ended early, as ReplyRun.Finished reports it.
var (
	ErrReplyStopped   = errors.New("stopped on this device")
	ErrReplyCancelled = errors.New("the person cancelled")
	ErrReplyLost      = errors.New("the browser disconnected")
	ErrReplyDamaged   = errors.New("a file arrived damaged")
)

// errReplyStalled ends the visitor: its page stopped answering.
var errReplyStalled = errors.New("the browser stopped sending")

// ReplyFile is one file someone wants to send back.
type ReplyFile struct {
	Name string // safe to use as a file name
	Size int64
}

// ReplyOffer is one batch of files someone wants to send back.
type ReplyOffer struct {
	ID      string
	Session string
	Name    string // the visitor's generated name
	Relayed bool
	Files   []ReplyFile
	Total   int64
	Note    string // the visitor's own words about who they are; proves nothing
}

// ReplyAnswer is how the owner answered a batch.
type ReplyAnswer int

const (
	ReplyDeclined   ReplyAnswer = iota
	ReplyAccepted               //
	ReplyUnanswered             // the prompt ran out: nobody said no
)

// ReplyRun follows an accepted batch.
type ReplyRun struct {
	Progress func(fileIndex int, done int64)
	// Finished reports what was saved; err is nil or one of the ErrReply
	// errors, or what went wrong writing the files.
	Finished func(saved []string, err error)
}

// ReplyHooks is how the app takes part in replies.
type ReplyHooks struct {
	// Dir is the folder replies are saved in.
	Dir func() string
	// Wait is how long the owner has to answer, which the page is told
	// (0 = as long as the page stays).
	Wait time.Duration
	// Ask puts a batch to the link's owner and waits for the answer. ctx ends
	// when the page gives up.
	Ask func(ctx context.Context, o ReplyOffer) ReplyAnswer
	// Started is called once a batch is accepted; cancel stops it.
	Started func(o ReplyOffer, cancel func()) ReplyRun
}

// SetReplies turns replies on or off for this link, and tells the pages that
// are open. Batches already accepted carry on. A link that asks for files
// always takes them.
func (s *Share) SetReplies(on bool) {
	on = (on || s.opts.Collect) && s.opts.Replies != nil
	if s.replies.Swap(on) == on {
		return
	}
	s.mu.Lock()
	visitors := make([]*visitor, 0, len(s.visitors))
	for _, v := range s.visitors {
		visitors = append(visitors, v)
	}
	s.mu.Unlock()
	for _, v := range visitors {
		if ch := v.ch.Load(); ch != nil {
			ch.sendJSON(pageMsg{Type: "replies", Reply: on})
		}
	}
}

// maxPrompts is how many prompts this link can have open at once.
func (s *Share) maxPrompts() int32 {
	if s.opts.Collect {
		return maxAskingCollect
	}
	return maxAsking
}

// visitorNote cleans what a visitor wrote about themselves to one short line.
func visitorNote(s string) string {
	s, err := protocol.CleanNote(s)
	if err != nil {
		return ""
	}
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxVisitorNote {
		s = string(r[:maxVisitorNote])
	}
	return s
}

// newReplyOffer checks what the page says it wants to send.
func (v *visitor) newReplyOffer(files []fileInfo, note string) (ReplyOffer, bool) {
	if len(files) == 0 || len(files) > maxReplyFiles {
		return ReplyOffer{}, false
	}
	v.mu.Lock()
	o := ReplyOffer{ID: transfer.NewTransferID(), Session: v.info.Session, Name: v.info.Name, Relayed: v.info.Relayed, Note: visitorNote(note)}
	v.mu.Unlock()
	for _, f := range files {
		name := transfer.SafeName(f.Name)
		if name == "" || f.Size < 0 || f.Size > math.MaxInt64-o.Total {
			return ReplyOffer{}, false
		}
		o.Files = append(o.Files, ReplyFile{Name: name, Size: f.Size})
		o.Total += f.Size
	}
	return o, true
}

// reply handles one offer from the page, from the question to the last file.
// An error ends the visitor; a batch that merely didn't happen doesn't.
func (v *visitor) reply(ch *channel, m pageMsg) error {
	s := v.s
	refuse := func(code string) error { return ch.sendJSON(pageMsg{Type: "replyAnswer", Error: code}) }
	if !s.replies.Load() {
		return refuse("off")
	}
	o, ok := v.newReplyOffer(m.Files, m.Note)
	if !ok {
		return refuse("invalid")
	}
	v.mu.Lock()
	spent := v.declined >= maxDeclined || v.unanswered >= maxUnanswered
	v.mu.Unlock()
	if spent {
		return refuse("limit")
	}
	dir := s.opts.Replies.Dir()
	if free, err := freeSpace(existingDir(dir)); err == nil && free < o.Total {
		return refuse("space")
	}
	if s.asking.Add(1) > s.maxPrompts() {
		s.asking.Add(-1)
		return refuse("busy")
	}

	was := v.status()
	v.setStatus(Asking)
	defer v.leaveReplying(was)

	if err := ch.sendJSON(pageMsg{Type: "replyAsking", Seconds: int(s.opts.Replies.Wait / time.Second)}); err != nil {
		s.asking.Add(-1)
		return err
	}
	answer, cancelled, err := v.ask(ch, o)
	s.asking.Add(-1)
	if err != nil || cancelled {
		return err
	}
	switch answer {
	case ReplyAccepted:
		return v.receiveBatch(ch, o, dir)
	case ReplyUnanswered:
		v.mu.Lock()
		v.unanswered++
		v.mu.Unlock()
		return refuse("noanswer")
	}
	v.mu.Lock()
	v.declined++
	v.mu.Unlock()
	return refuse("declined")
}

// ask waits for the owner, while watching for the page giving up; cancelled
// means the page took the offer back.
func (v *visitor) ask(ch *channel, o ReplyOffer) (answer ReplyAnswer, cancelled bool, err error) {
	ctx, cancel := context.WithCancel(v.ctx)
	defer cancel()
	answers := make(chan ReplyAnswer, 1)
	go func() { answers <- v.s.opts.Replies.Ask(ctx, o) }()
	for {
		select {
		case a := <-answers:
			return a, false, nil
		case m := <-ch.msgs:
			if m.binary {
				return ReplyDeclined, false, errUnasked
			}
			if m.Type == "replyCancel" {
				return ReplyDeclined, true, nil
			}
		case <-ch.closed:
			return ReplyDeclined, false, errClosed
		case <-v.ctx.Done():
			return ReplyDeclined, false, v.ctx.Err()
		}
	}
}

// errUnasked: file data outside an accepted batch.
var errUnasked = errors.New("the browser sent file data nobody accepted")

// receiveBatch saves an accepted batch and tells the page how it ended.
func (v *visitor) receiveBatch(ch *channel, o ReplyOffer, dir string) error {
	stop := make(chan struct{})
	var once sync.Once
	run := v.s.opts.Replies.Started(o, func() { once.Do(func() { close(stop) }) })
	if err := ch.sendJSON(pageMsg{Type: "replyAnswer", OK: true}); err != nil {
		run.Finished(nil, ErrReplyLost)
		return err
	}
	v.startReceiving(o.Total)
	saved, err := v.receiveFiles(ch, o, dir, stop, func(i int, done int64) {
		run.Progress(i, done)
		v.replyProgress(done)
	})
	run.Finished(saved, reportedReplyError(err, v.ctx.Err() != nil))

	switch {
	case err == nil, errors.Is(err, ErrReplyCancelled):
		return nil
	case errors.Is(err, errClosed), errors.Is(err, errReplyStalled):
		return err
	case v.ctx.Err() != nil:
		return v.ctx.Err()
	}
	code := "disk"
	switch {
	case errors.Is(err, ErrReplyStopped):
		code = "stopped"
	case errors.Is(err, ErrReplyDamaged):
		code = "damaged"
	}
	if err := ch.sendJSON(pageMsg{Type: "replyStop", Error: code}); err != nil {
		return err
	}
	return v.drain(ch)
}

// reportedReplyError is what the app hears about a batch that ended with err.
func reportedReplyError(err error, linkClosing bool) error {
	switch {
	case err == nil:
		return nil
	case linkClosing:
		return ErrReplyStopped
	case errors.Is(err, errClosed), errors.Is(err, errReplyStalled):
		return ErrReplyLost
	}
	return err
}

// drain drops what the page sent before it heard the batch was stopped.
func (v *visitor) drain(ch *channel) error {
	deadline := time.NewTimer(drainFor)
	defer deadline.Stop()
	for {
		select {
		case m := <-ch.msgs:
			if m.Type == "replyCancel" {
				return nil
			}
		case <-deadline.C:
			return errReplyStalled
		case <-ch.closed:
			return errClosed
		case <-v.ctx.Done():
			return v.ctx.Err()
		}
	}
}

// receiveFiles takes the batch's files in order. What's saved stays saved
// when a later file fails.
func (v *visitor) receiveFiles(ch *channel, o ReplyOffer, dir string, stop <-chan struct{}, progress func(int, int64)) ([]string, error) {
	stall := time.NewTimer(replyStall)
	defer stall.Stop()
	next := func() (pageMsg, error) {
		stall.Reset(replyStall)
		select {
		case m := <-ch.msgs:
			return m, nil
		case <-stop:
			return pageMsg{}, ErrReplyStopped
		case <-stall.C:
			return pageMsg{}, errReplyStalled
		case <-ch.closed:
			return pageMsg{}, errClosed
		case <-v.ctx.Done():
			return pageMsg{}, v.ctx.Err()
		}
	}
	var saved []string
	var base int64
	for i, f := range o.Files {
		m, err := next()
		if err != nil {
			return saved, err
		}
		if m.Type == "replyCancel" {
			return saved, ErrReplyCancelled
		}
		if m.binary || m.Type != "put" || m.Index != i {
			return saved, ErrReplyDamaged
		}
		path, err := v.receiveFile(i, f, dir, base, next, ch, progress)
		if err != nil {
			return saved, err
		}
		saved = append(saved, path)
		base += f.Size
		v.fileDelivered(f.Size)
		if err := ch.sendJSON(pageMsg{Type: "saved", Index: i}); err != nil {
			return saved, errClosed
		}
	}
	return saved, nil
}

// receiveFile writes one file, up to its declared size and no further.
func (v *visitor) receiveFile(i int, f ReplyFile, dir string, base int64, next func() (pageMsg, error), ch *channel, progress func(int, int64)) (string, error) {
	out, err := transfer.NewPartFile(dir, f.Name)
	if err != nil {
		return "", err
	}
	kept := false
	defer func() {
		if !kept {
			out.Discard()
		}
	}()
	h := sha256.New()
	var n int64
	var last time.Time
	for {
		m, err := next()
		if err != nil {
			return "", err
		}
		switch {
		case m.binary:
			if int64(len(m.data)) > f.Size-n {
				return "", ErrReplyDamaged
			}
			if _, err := out.Write(m.data); err != nil {
				return "", err
			}
			h.Write(m.data)
			n += int64(len(m.data))
			if now := time.Now(); now.Sub(last) >= 100*time.Millisecond {
				last = now
				progress(i, base+n)
			}
		case m.Type == "replyCancel":
			return "", ErrReplyCancelled
		case m.Type == "fin" && m.Index == i:
			if n != f.Size || m.SHA256 != hex.EncodeToString(h.Sum(nil)) {
				return "", ErrReplyDamaged
			}
			path, err := out.Keep()
			kept = true // Keep cleans up after itself
			if err != nil {
				return "", err
			}
			progress(i, base+n)
			return path, nil
		default:
			return "", ErrReplyDamaged
		}
	}
}

// existingDir is dir, or its nearest parent that exists: the download folder
// is only created when the first file arrives.
func existingDir(dir string) string {
	for {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}
