package linkshare

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"TropicalPeer/internal/transfer"
)

// owner plays the app on a link with replies on.
type owner struct {
	dir      string
	answer   func(ctx context.Context, o ReplyOffer) ReplyAnswer
	asked    atomic.Int32
	offers   chan ReplyOffer
	cancel   chan func()
	finished chan replyEnd
}

type replyEnd struct {
	saved []string
	err   error
}

func newOwner(t *testing.T) *owner {
	return &owner{
		dir:      t.TempDir(),
		answer:   func(context.Context, ReplyOffer) ReplyAnswer { return ReplyAccepted },
		offers:   make(chan ReplyOffer, 8),
		cancel:   make(chan func(), 8),
		finished: make(chan replyEnd, 8),
	}
}

func (w *owner) hooks() *ReplyHooks {
	return &ReplyHooks{
		Dir: func() string { return w.dir },
		Ask: func(ctx context.Context, o ReplyOffer) ReplyAnswer {
			w.asked.Add(1)
			w.offers <- o
			return w.answer(ctx, o)
		},
		Started: func(o ReplyOffer, cancel func()) ReplyRun {
			w.cancel <- cancel
			return ReplyRun{
				Progress: func(int, int64) {},
				Finished: func(saved []string, err error) { w.finished <- replyEnd{saved, err} },
			}
		},
	}
}

func (w *owner) end(t *testing.T) replyEnd {
	t.Helper()
	select {
	case e := <-w.finished:
		return e
	case <-time.After(15 * time.Second):
		t.Fatal("the batch never finished")
	}
	return replyEnd{}
}

// The files left in the owner's folder.
func (w *owner) files(t *testing.T) []string {
	t.Helper()
	list, err := os.ReadDir(w.dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range list {
		names = append(names, e.Name())
	}
	return names
}

func replyShare(t *testing.T, w *owner, limit Limit) (*Share, *statuses, string, string) {
	t.Helper()
	c, wsURL := startServer(t)
	st := newStatuses()
	s, err := Start(context.Background(), c, []transfer.Source{memFile("a.txt", []byte("the files"))}, Options{
		Limit: limit, SenderName: "Test PC", OnUpdate: st.update, Replies: w.hooks(), AllowReplies: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	_, secret, _ := strings.Cut(s.URL, "#")
	return s, st, wsURL, secret
}

func open(t *testing.T, wsURL, id, secret string) *browser {
	t.Helper()
	b := openLink(t, wsURL, id, secret)
	b.next("auth")
	if !b.next("hello").Reply {
		t.Fatal("hello doesn't say replies are on")
	}
	return b
}

func (b *browser) offer(files ...fileInfo) {
	raw, _ := json.Marshal(pageMsg{Type: "offer", Files: files})
	b.send(string(raw))
}

// answer is the owner's answer, past the notice that they are being asked.
func (b *browser) answer() pageMsg {
	b.t.Helper()
	for {
		select {
		case m := <-b.msgs:
			switch m.Type {
			case "replyAsking", "replies":
				continue
			case "replyAnswer":
				return m
			}
			b.t.Fatalf("got %q (%s), want an answer", m.Type, m.Error)
		case <-time.After(15 * time.Second):
			b.t.Fatal("no answer from the owner")
		}
	}
}

// putFile sends one file of an accepted batch, in the page's order.
func (b *browser) putFile(i int, data []byte, sum string) {
	b.send(fmt.Sprintf(`{"type":"put","index":%d}`, i))
	for rest := data; len(rest) > 0; {
		n := min(32*1024, len(rest))
		if err := b.dc.Send(rest[:n]); err != nil {
			b.t.Fatal(err)
		}
		rest = rest[n:]
	}
	b.send(fmt.Sprintf(`{"type":"fin","index":%d,"sha256":%q}`, i, sum))
}

func sumOf(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

func TestRepliesOffByDefault(t *testing.T) {
	s, _, wsURL, secret := share(t, []transfer.Source{memFile("a.txt", []byte("x"))}, Unlimited)
	b := openLink(t, wsURL, s.ID, secret)
	b.next("auth")
	if b.next("hello").Reply {
		t.Fatal("a link without replies says it takes them")
	}
	b.offer(fileInfo{Name: "b.txt", Size: 1})
	if m := b.answer(); m.OK || m.Error != "off" {
		t.Fatalf("answer: %+v", m)
	}
}

// Turning replies off refuses pages that already saw them on.
func TestRepliesTurnedOff(t *testing.T) {
	w := newOwner(t)
	s, _, wsURL, secret := replyShare(t, w, Unlimited)
	b := open(t, wsURL, s.ID, secret)
	s.SetReplies(false)
	b.offer(fileInfo{Name: "b.txt", Size: 1})
	if m := b.answer(); m.Error != "off" {
		t.Fatalf("answer: %+v", m)
	}
	if w.asked.Load() != 0 {
		t.Fatal("the owner was asked about a link with replies off")
	}
}

func TestReply(t *testing.T) {
	w := newOwner(t)
	s, _, wsURL, secret := replyShare(t, w, Unlimited)
	b := open(t, wsURL, s.ID, secret)

	one := make([]byte, 3<<20+17)
	rand.Read(one)
	two := []byte{} // empty files are files too
	b.offer(fileInfo{Name: "photo.jpg", Size: int64(len(one))}, fileInfo{Name: "empty.txt", Size: 0})
	o := <-w.offers
	if o.Total != int64(len(one)) || len(o.Files) != 2 || o.Name == "" {
		t.Fatalf("offer: %+v", o)
	}
	if m := b.answer(); !m.OK {
		t.Fatalf("answer: %+v", m)
	}
	b.putFile(0, one, sumOf(one))
	if m := b.next("saved"); m.Index != 0 {
		t.Fatalf("saved: %+v", m)
	}
	b.putFile(1, two, sumOf(two))
	if m := b.next("saved"); m.Index != 1 {
		t.Fatalf("saved: %+v", m)
	}
	e := w.end(t)
	if e.err != nil || len(e.saved) != 2 {
		t.Fatalf("finished: %+v", e)
	}
	got, err := os.ReadFile(filepath.Join(w.dir, "photo.jpg"))
	if err != nil || !bytes.Equal(got, one) {
		t.Fatalf("saved file differs: %v", err)
	}
	if names := w.files(t); len(names) != 2 {
		t.Fatalf("left in the folder: %v", names)
	}

	// The connection carries on: the page can still download.
	b.send(`{"type":"get","index":0}`)
	b.next("end")
}

// A name from a stranger never reaches outside the download folder.
func TestReplyHostileNames(t *testing.T) {
	w := newOwner(t)
	s, _, wsURL, secret := replyShare(t, w, Unlimited)
	b := open(t, wsURL, s.ID, secret)
	data := []byte("x")
	b.offer(fileInfo{Name: `..\..\..\evil.exe`, Size: 1}, fileInfo{Name: "../../evil2.txt", Size: 1})
	o := <-w.offers
	if o.Files[0].Name != "evil.exe" || o.Files[1].Name != "evil2.txt" {
		t.Fatalf("names: %+v", o.Files)
	}
	b.answer()
	b.putFile(0, data, sumOf(data))
	b.next("saved")
	b.putFile(1, data, sumOf(data))
	b.next("saved")
	for _, p := range w.end(t).saved {
		if filepath.Dir(p) != w.dir {
			t.Fatalf("saved outside the folder: %s", p)
		}
	}
}

func TestReplyInvalidOffers(t *testing.T) {
	w := newOwner(t)
	s, _, wsURL, secret := replyShare(t, w, Unlimited)
	b := open(t, wsURL, s.ID, secret)
	many := make([]fileInfo, maxReplyFiles+1)
	for i := range many {
		many[i] = fileInfo{Name: "f", Size: 1}
	}
	for name, files := range map[string][]fileInfo{
		"no files":   nil,
		"too many":   many,
		"no name":    {{Name: "..", Size: 1}},
		"negative":   {{Name: "a", Size: -1}},
		"overflowed": {{Name: "a", Size: 1<<63 - 1}, {Name: "b", Size: 1}},
	} {
		b.offer(files...)
		if m := b.answer(); m.Error != "invalid" {
			t.Fatalf("%s: %+v", name, m)
		}
	}
	if w.asked.Load() != 0 {
		t.Fatal("the owner was asked about an invalid offer")
	}
}

// A page turned down repeatedly stops reaching the owner.
func TestReplyDeclinedStopsAsking(t *testing.T) {
	w := newOwner(t)
	w.answer = func(context.Context, ReplyOffer) ReplyAnswer { return ReplyDeclined }
	s, _, wsURL, secret := replyShare(t, w, Unlimited)
	b := open(t, wsURL, s.ID, secret)
	for i := range maxDeclined + 2 {
		b.offer(fileInfo{Name: "a.txt", Size: 1})
		want := "declined"
		if i >= maxDeclined {
			want = "limit"
		}
		if m := b.answer(); m.Error != want {
			t.Fatalf("offer %d: %+v", i, m)
		}
	}
	if n := w.asked.Load(); n != maxDeclined {
		t.Fatalf("asked %d times, want %d", n, maxDeclined)
	}
}

// A link can't pile up prompts, however many people have it.
func TestReplyPromptLimit(t *testing.T) {
	w := newOwner(t)
	release := make(chan struct{})
	w.answer = func(ctx context.Context, _ ReplyOffer) ReplyAnswer {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return ReplyDeclined
	}
	defer close(release)
	s, _, wsURL, secret := replyShare(t, w, Unlimited)
	for range maxAsking {
		b := open(t, wsURL, s.ID, secret)
		b.offer(fileInfo{Name: "a.txt", Size: 1})
		<-w.offers
	}
	b := open(t, wsURL, s.ID, secret)
	b.offer(fileInfo{Name: "a.txt", Size: 1})
	if m := b.answer(); m.Error != "busy" {
		t.Fatalf("answer: %+v", m)
	}
}

// The page takes its offer back while the owner is still deciding.
func TestReplyCancelledWhileAsking(t *testing.T) {
	w := newOwner(t)
	gaveUp := make(chan struct{})
	w.answer = func(ctx context.Context, _ ReplyOffer) ReplyAnswer {
		<-ctx.Done()
		close(gaveUp)
		return ReplyDeclined
	}
	s, _, wsURL, secret := replyShare(t, w, Unlimited)
	b := open(t, wsURL, s.ID, secret)
	b.offer(fileInfo{Name: "a.txt", Size: 1})
	<-w.offers
	b.next("replyAsking")
	b.send(`{"type":"replyCancel"}`)
	select {
	case <-gaveUp:
	case <-time.After(15 * time.Second):
		t.Fatal("the prompt stayed open")
	}
	b.send(`{"type":"get","index":0}`)
	b.next("end")
}

func TestReplyNoRoom(t *testing.T) {
	old := freeSpace
	freeSpace = func(string) (int64, error) { return 100, nil }
	defer func() { freeSpace = old }()
	w := newOwner(t)
	s, _, wsURL, secret := replyShare(t, w, Unlimited)
	b := open(t, wsURL, s.ID, secret)
	b.offer(fileInfo{Name: "a.bin", Size: 101})
	if m := b.answer(); m.Error != "space" {
		t.Fatalf("answer: %+v", m)
	}
	if w.asked.Load() != 0 {
		t.Fatal("the owner was asked about files that can't fit")
	}
}

// More data than declared stops the batch and leaves nothing behind.
func TestReplyOversize(t *testing.T) {
	w := newOwner(t)
	s, _, wsURL, secret := replyShare(t, w, Unlimited)
	b := open(t, wsURL, s.ID, secret)
	b.offer(fileInfo{Name: "a.bin", Size: 10})
	b.answer()
	big := make([]byte, 11)
	b.putFile(0, big, sumOf(big))
	if m := b.next("replyStop"); m.Error != "damaged" {
		t.Fatalf("stop: %+v", m)
	}
	if e := w.end(t); !errors.Is(e.err, ErrReplyDamaged) {
		t.Fatalf("finished: %+v", e)
	}
	if names := w.files(t); len(names) != 0 {
		t.Fatalf("left in the folder: %v", names)
	}
}

func TestReplyDamaged(t *testing.T) {
	w := newOwner(t)
	s, _, wsURL, secret := replyShare(t, w, Unlimited)
	b := open(t, wsURL, s.ID, secret)
	data := []byte("hello")
	b.offer(fileInfo{Name: "a.bin", Size: 5})
	b.answer()
	b.putFile(0, data, sumOf([]byte("other")))
	if m := b.next("replyStop"); m.Error != "damaged" {
		t.Fatalf("stop: %+v", m)
	}
	if e := w.end(t); !errors.Is(e.err, ErrReplyDamaged) {
		t.Fatalf("finished: %+v", e)
	}
	if names := w.files(t); len(names) != 0 {
		t.Fatalf("left in the folder: %v", names)
	}
}

// File data nobody accepted ends the visitor.
func TestReplyDataWithoutOffer(t *testing.T) {
	w := newOwner(t)
	s, st, wsURL, secret := replyShare(t, w, Unlimited)
	b := open(t, wsURL, s.ID, secret)
	b.dc.Send([]byte("unasked"))
	st.waitFor(t, Failed)
	if names := w.files(t); len(names) != 0 {
		t.Fatalf("left in the folder: %v", names)
	}
}

// The page disappears halfway: nothing partial stays.
func TestReplyPageLeaves(t *testing.T) {
	w := newOwner(t)
	s, _, wsURL, secret := replyShare(t, w, Unlimited)
	b := open(t, wsURL, s.ID, secret)
	b.offer(fileInfo{Name: "a.bin", Size: 1 << 20})
	b.answer()
	b.send(`{"type":"put","index":0}`)
	b.dc.Send(make([]byte, 1024))
	time.Sleep(100 * time.Millisecond)
	b.pc.Close()
	if e := w.end(t); !errors.Is(e.err, ErrReplyLost) {
		t.Fatalf("finished: %+v", e)
	}
	if names := w.files(t); len(names) != 0 {
		t.Fatalf("left in the folder: %v", names)
	}
}

// A page that goes silent mid-batch doesn't hold the file open forever.
func TestReplyStalled(t *testing.T) {
	old := replyStall
	replyStall = 300 * time.Millisecond
	defer func() { replyStall = old }()
	w := newOwner(t)
	s, st, wsURL, secret := replyShare(t, w, Unlimited)
	b := open(t, wsURL, s.ID, secret)
	b.offer(fileInfo{Name: "a.bin", Size: 1 << 20})
	b.answer()
	b.send(`{"type":"put","index":0}`)
	if e := w.end(t); !errors.Is(e.err, ErrReplyLost) {
		t.Fatalf("finished: %+v", e)
	}
	st.waitFor(t, Failed)
	if names := w.files(t); len(names) != 0 {
		t.Fatalf("left in the folder: %v", names)
	}
}

// The owner stops a batch; the page can go on using the link.
func TestReplyStoppedHere(t *testing.T) {
	w := newOwner(t)
	s, _, wsURL, secret := replyShare(t, w, Unlimited)
	b := open(t, wsURL, s.ID, secret)
	b.offer(fileInfo{Name: "a.bin", Size: 1 << 20})
	b.answer()
	b.send(`{"type":"put","index":0}`)
	b.dc.Send(make([]byte, 1024))
	(<-w.cancel)()
	if m := b.next("replyStop"); m.Error != "stopped" {
		t.Fatalf("stop: %+v", m)
	}
	if e := w.end(t); !errors.Is(e.err, ErrReplyStopped) {
		t.Fatalf("finished: %+v", e)
	}
	b.dc.Send(make([]byte, 1024)) // on its way before the page heard
	b.send(`{"type":"replyCancel"}`)
	b.send(`{"type":"get","index":0}`)
	b.next("end")
	if names := w.files(t); len(names) != 0 {
		t.Fatalf("left in the folder: %v", names)
	}
}

// With replies on, a finished download leaves the page connected. The link
// counts as used at once, and the person can still send files back.
func TestReplyAfterDownload(t *testing.T) {
	w := newOwner(t)
	s, st, wsURL, secret := replyShare(t, w, FirstOnly)
	b := open(t, wsURL, s.ID, secret)
	b.send(`{"type":"get","index":0}`)
	b.next("end")
	b.send(`{"type":"downloaded"}`)
	st.waitFor(t, Downloaded)

	other := openLink(t, wsURL, s.ID, secret)
	if m := other.next("error"); m.Error != "used" {
		t.Fatalf("second person: %+v", m)
	}

	data := []byte("thanks")
	b.offer(fileInfo{Name: "thanks.txt", Size: int64(len(data))})
	<-w.offers
	if m := b.answer(); !m.OK {
		t.Fatalf("answer: %+v", m)
	}
	b.putFile(0, data, sumOf(data))
	b.next("saved")
	if e := w.end(t); e.err != nil {
		t.Fatalf("finished: %+v", e)
	}
	st.waitFor(t, Downloaded) // back to where they were

	b.pc.Close()
	st.waitFor(t, Done)
}

// A prompt nobody answered isn't a refusal, and doesn't count as one.
func TestReplyUnansweredIsNotDeclined(t *testing.T) {
	w := newOwner(t)
	w.answer = func(context.Context, ReplyOffer) ReplyAnswer { return ReplyUnanswered }
	s, _, wsURL, secret := replyShare(t, w, Unlimited)
	b := open(t, wsURL, s.ID, secret)
	for i := range maxUnanswered {
		b.offer(fileInfo{Name: "a.txt", Size: 1})
		if m := b.answer(); m.Error != "noanswer" {
			t.Fatalf("offer %d: %+v", i, m)
		}
	}
	b.offer(fileInfo{Name: "a.txt", Size: 1})
	if m := b.answer(); m.Error != "limit" {
		t.Fatalf("after %d unanswered: %+v", maxUnanswered, m)
	}
}

// The page hears how long the owner has, and what it wrote reaches the owner
// as one clean line.
func TestReplyAskingNotice(t *testing.T) {
	w := newOwner(t)
	c, wsURL := startServer(t)
	hooks := w.hooks()
	hooks.Wait = 3 * time.Minute
	s, err := Start(context.Background(), c, []transfer.Source{memFile("a.txt", []byte("x"))}, Options{Replies: hooks, AllowReplies: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	_, secret, _ := strings.Cut(s.URL, "#")
	b := open(t, wsURL, s.ID, secret)
	raw, _ := json.Marshal(pageMsg{Type: "offer", Files: []fileInfo{{Name: "a.txt", Size: 0}}, Note: "It's\n  Sam\u202e " + strings.Repeat("x", 500)})
	b.send(string(raw))
	if m := b.next("replyAsking"); m.Seconds != 180 {
		t.Fatalf("notice: %+v", m)
	}
	o := <-w.offers
	if strings.ContainsAny(o.Note, "\n\u202e") || len([]rune(o.Note)) > maxVisitorNote || !strings.HasPrefix(o.Note, "It's Sam") {
		t.Fatalf("note: %q", o.Note)
	}
}

// Pages already open hear when replies are turned on or off.
func TestRepliesChangeReachesOpenPages(t *testing.T) {
	w := newOwner(t)
	s, _, wsURL, secret := replyShare(t, w, Unlimited)
	b := open(t, wsURL, s.ID, secret)
	s.SetReplies(false)
	if m := b.next("replies"); m.Reply {
		t.Fatalf("turned off: %+v", m)
	}
	s.SetReplies(true)
	if m := b.next("replies"); !m.Reply {
		t.Fatalf("turned on: %+v", m)
	}
}

func collectShare(t *testing.T, w *owner, limit Limit) (*Share, *statuses, string, string) {
	t.Helper()
	c, wsURL := startServer(t)
	st := newStatuses()
	s, err := Start(context.Background(), c, nil, Options{
		Limit: limit, SenderName: "Test PC", OnUpdate: st.update, Replies: w.hooks(), Collect: true, Purpose: "Wedding photos",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	_, secret, _ := strings.Cut(s.URL, "#")
	return s, st, wsURL, secret
}

// A link that asks for files has nothing to download, and always takes them.
func TestCollectLink(t *testing.T) {
	w := newOwner(t)
	s, st, wsURL, secret := collectShare(t, w, FirstOnly)
	s.SetReplies(false) // no such switch on this kind of link
	b := openLink(t, wsURL, s.ID, secret)
	b.next("auth")
	h := b.next("hello")
	if !h.Ask || !h.Reply || h.Purpose != "Wedding photos" || len(h.Files) != 0 {
		t.Fatalf("hello: %+v", h)
	}

	// The person holds the link until their page closes.
	other := openLink(t, wsURL, s.ID, secret)
	other.next("auth")
	if m := other.next("error"); m.Error != "used" {
		t.Fatalf("second person: %+v", m)
	}

	data := []byte("photo")
	b.offer(fileInfo{Name: "photo.jpg", Size: int64(len(data))})
	<-w.offers
	if m := b.answer(); !m.OK {
		t.Fatalf("answer: %+v", m)
	}
	b.putFile(0, data, sumOf(data))
	b.next("saved")
	w.end(t)

	// They can send more while the page is open.
	b.offer(fileInfo{Name: "photo2.jpg", Size: int64(len(data))})
	<-w.offers
	b.answer()
	b.putFile(0, data, sumOf(data))
	b.next("saved")
	w.end(t)

	b.pc.Close()
	v := st.waitFor(t, Done)
	if v.SentFiles != 2 || v.SentBytes != int64(2*len(data)) {
		t.Fatalf("visitor: %+v", v)
	}
	late := openLink(t, wsURL, s.ID, secret)
	if m := late.next("error"); m.Error != "used" {
		t.Fatalf("after it was used: %+v", m)
	}
}

// Leaving without sending anything doesn't use the link up.
func TestCollectLinkLeftEmpty(t *testing.T) {
	w := newOwner(t)
	s, st, wsURL, secret := collectShare(t, w, FirstOnly)
	b := openLink(t, wsURL, s.ID, secret)
	b.next("auth")
	b.next("hello")
	b.send(`{"type":"downloaded"}`) // means nothing here
	b.pc.Close()
	st.waitFor(t, Cancelled)
	again := openLink(t, wsURL, s.ID, secret)
	again.next("auth")
	if !again.next("hello").Ask {
		t.Fatal("the link wasn't free again")
	}
}
