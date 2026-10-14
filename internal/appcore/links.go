package appcore

import (
	"errors"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"rsc.io/qr"

	"TropicalPeer/internal/linkshare"
	"TropicalPeer/internal/transfer"
)

// Share by link: files anyone with the link can download in a browser (see
// package linkshare). Links live while the app runs; they aren't saved.

// LinkDTO is one link, pushed as "link:update".
type LinkDTO struct {
	ID        string       `json:"id"`
	URL       string       `json:"url"`
	QR        []string     `json:"qr"` // rows of the QR code, "1" = dark module
	Files     []FileDTO    `json:"files"`
	Total     int64        `json:"total"`
	Unlimited bool         `json:"unlimited"` // anyone with the link, not just the first person
	Ended     string       `json:"ended"`     // why the link stopped working ("" while it works)
	Offline   bool         `json:"offline"`   // lost the server for now; trying to get back
	CreatedAt int64        `json:"createdAt"` // unix ms
	Visitors  []VisitorDTO `json:"visitors"`  // oldest first
}

// VisitorDTO is one browser that opened a link.
type VisitorDTO struct {
	Session string  `json:"session"`
	Name    string  `json:"name"`   // generated, e.g. "Orchid Tiger"
	Status  string  `json:"status"` // connecting, waiting, sending, done, cancelled, failed, refused
	Done    int64   `json:"done"`
	Total   int64   `json:"total"`
	Speed   float64 `json:"speed"`
	Error   string  `json:"error"`
	SeenAt  int64   `json:"seenAt"` // unix ms
}

type linkState struct {
	mu       sync.Mutex
	dto      LinkDTO
	share    *linkshare.Share
	speeds   map[string]*speedometer
	order    map[string]int
	emitted  time.Time
	pendingE bool
}

type speedometer struct {
	at   time.Time
	done int64
	rate float64
}

type linkStore struct {
	mu    sync.Mutex
	links map[string]*linkState
}

// LinksAvailable reports whether the rendezvous server supports links.
func (c *Core) LinksAvailable() bool {
	n := c.n()
	return n != nil && n.LinksURL() != ""
}

// CreateLink opens a link for the files at paths.
func (c *Core) CreateLink(paths []string, unlimited bool) (LinkDTO, error) {
	sources, err := c.describeFiles(paths)
	if err != nil {
		return LinkDTO{}, err
	}
	return c.CreateLinkSources(sources, unlimited)
}

// CreateLinkSources is CreateLink for files that aren't paths on disk.
func (c *Core) CreateLinkSources(sources []transfer.Source, unlimited bool) (LinkDTO, error) {
	n := c.n()
	if n == nil {
		return LinkDTO{}, c.errT("links.notReady")
	}
	if len(sources) == 0 {
		return LinkDTO{}, c.errT("transfer.chooseFile")
	}
	if n.LinksURL() == "" {
		return LinkDTO{}, c.errT("links.unsupported")
	}
	ls := &linkState{speeds: map[string]*speedometer{}, order: map[string]int{}}
	limit := linkshare.FirstOnly
	if unlimited {
		limit = linkshare.Unlimited
	}
	share, err := n.ShareLink(c.ctx, sources, linkshare.Options{
		Limit:    limit,
		OnUpdate: func(v linkshare.Visitor) { c.onVisitor(ls, v) },
		Log:      log.Printf,
		OnServer: func(connected bool) {
			ls.mu.Lock()
			ls.dto.Offline = !connected
			ls.mu.Unlock()
			c.emitLink(ls, true)
		},
	})
	if err != nil {
		return LinkDTO{}, errors.New(c.T("links.createFailed", "error", c.friendlyServerError(err)))
	}
	dto := LinkDTO{ID: share.ID, URL: share.URL, Unlimited: unlimited, CreatedAt: time.Now().UnixMilli()}
	for _, s := range sources {
		dto.Files = append(dto.Files, FileDTO{Name: transfer.SafePath(s.Name), Size: s.Size})
		dto.Total += s.Size
	}
	if code, err := qr.Encode(share.URL, qr.M); err == nil {
		for y := 0; y < code.Size; y++ {
			var row strings.Builder
			for x := 0; x < code.Size; x++ {
				if code.Black(x, y) {
					row.WriteByte('1')
				} else {
					row.WriteByte('0')
				}
			}
			dto.QR = append(dto.QR, row.String())
		}
	}
	ls.dto, ls.share = dto, share

	c.lk.mu.Lock()
	if c.lk.links == nil {
		c.lk.links = map[string]*linkState{}
	}
	c.lk.links[dto.ID] = ls
	c.lk.mu.Unlock()
	c.emitLink(ls, true)

	go func() {
		<-share.Done()
		ls.mu.Lock()
		if ls.dto.Ended == "" {
			ls.dto.Ended = c.T("links.endedServer")
		}
		ls.mu.Unlock()
		c.emitLink(ls, true)
	}()
	return c.linkSnapshot(ls), nil
}

func (c *Core) onVisitor(ls *linkState, v linkshare.Visitor) {
	ls.mu.Lock()
	now := time.Now()
	i, ok := ls.order[v.Session]
	if !ok {
		i = len(ls.dto.Visitors)
		ls.order[v.Session] = i
		ls.dto.Visitors = append(ls.dto.Visitors, VisitorDTO{Session: v.Session, SeenAt: now.UnixMilli()})
	}
	sp := ls.speeds[v.Session]
	if sp == nil {
		sp = &speedometer{at: now}
		ls.speeds[v.Session] = sp
	}
	if dt := now.Sub(sp.at).Seconds(); dt >= 0.25 {
		r := float64(v.Done-sp.done) / dt
		if sp.rate == 0 {
			sp.rate = r
		} else {
			sp.rate = 0.7*sp.rate + 0.3*r
		}
		sp.at, sp.done = now, v.Done
	}
	vd := &ls.dto.Visitors[i]
	vd.Name, vd.Status, vd.Done, vd.Total = v.Name, string(v.Status), v.Done, v.Total
	vd.Error = visitorError(c, v)
	vd.Speed = 0
	if v.Status == linkshare.Sending {
		vd.Speed = sp.rate
	}
	final := v.Status != linkshare.Sending
	ls.mu.Unlock()
	c.emitLink(ls, final)
}

func visitorError(c *Core, v linkshare.Visitor) string {
	switch v.Status {
	case linkshare.Failed:
		return c.T("links.visitorFailed")
	case linkshare.Cancelled:
		switch {
		case strings.Contains(v.Error, "receiver cancelled"):
			return c.T("links.visitorCancelled")
		case strings.Contains(v.Error, "receiver"):
			return c.T("links.visitorLeft")
		}
	}
	return ""
}

// emitLink pushes a link to the UI, at most every progressEvery unless now.
func (c *Core) emitLink(ls *linkState, now bool) {
	ls.mu.Lock()
	if !now && time.Since(ls.emitted) < progressEvery {
		if !ls.pendingE {
			ls.pendingE = true
			time.AfterFunc(progressEvery, func() {
				ls.mu.Lock()
				ls.pendingE = false
				ls.mu.Unlock()
				c.emitLink(ls, true)
			})
		}
		ls.mu.Unlock()
		return
	}
	ls.emitted = time.Now()
	ls.mu.Unlock()
	c.emit("link:update", c.linkSnapshot(ls))
}

func (c *Core) linkSnapshot(ls *linkState) LinkDTO {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	d := ls.dto
	d.Visitors = append([]VisitorDTO{}, ls.dto.Visitors...) // [] not null for the UI
	return d
}

// ListLinks returns the open links, newest first.
func (c *Core) ListLinks() []LinkDTO {
	c.lk.mu.Lock()
	all := make([]*linkState, 0, len(c.lk.links))
	for _, ls := range c.lk.links {
		all = append(all, ls)
	}
	c.lk.mu.Unlock()
	out := make([]LinkDTO, len(all))
	for i, ls := range all {
		out[i] = c.linkSnapshot(ls)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

// StopLink closes a link (cancelling downloads in progress) and forgets it.
func (c *Core) StopLink(id string) {
	c.lk.mu.Lock()
	ls := c.lk.links[id]
	delete(c.lk.links, id)
	c.lk.mu.Unlock()
	if ls == nil {
		return
	}
	ls.mu.Lock()
	ls.dto.Ended = "stopped"
	ls.mu.Unlock()
	ls.share.Stop()
	c.emit("link:removed", id)
}

// SetLinkUnlimited switches a link between the first person only and
// anyone with the link.
func (c *Core) SetLinkUnlimited(id string, unlimited bool) {
	c.lk.mu.Lock()
	ls := c.lk.links[id]
	c.lk.mu.Unlock()
	if ls == nil {
		return
	}
	l := linkshare.FirstOnly
	if unlimited {
		l = linkshare.Unlimited
	}
	ls.share.SetLimit(l)
	ls.mu.Lock()
	ls.dto.Unlimited = unlimited
	ls.mu.Unlock()
	c.emitLink(ls, true)
}

// CancelLinkVisitor stops sending to one person.
func (c *Core) CancelLinkVisitor(id, session string) {
	c.lk.mu.Lock()
	ls := c.lk.links[id]
	c.lk.mu.Unlock()
	if ls != nil {
		ls.share.CancelVisitor(session)
	}
}

// stopLinks closes every link (the app is quitting).
func (c *Core) stopLinks() {
	c.lk.mu.Lock()
	all := c.lk.links
	c.lk.links = nil
	c.lk.mu.Unlock()
	for _, ls := range all {
		ls.share.Stop()
	}
}
