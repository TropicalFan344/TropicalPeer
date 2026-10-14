package linkshare

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

var errClosed = errors.New("the browser disconnected")

// channel turns a DataChannel's callbacks into blocking calls.
type channel struct {
	dc   *webrtc.DataChannel
	open chan struct{}

	msgs   chan pageMsg
	closed chan struct{}
	once   sync.Once

	low chan struct{} // signalled when the send buffer drains
}

func newChannel(dc *webrtc.DataChannel) *channel {
	c := &channel{
		dc:     dc,
		open:   make(chan struct{}),
		msgs:   make(chan pageMsg, 16),
		closed: make(chan struct{}),
		low:    make(chan struct{}, 1),
	}
	var openOnce sync.Once
	markOpen := func() { openOnce.Do(func() { close(c.open) }) }
	dc.OnOpen(markOpen)
	if dc.ReadyState() == webrtc.DataChannelStateOpen {
		markOpen()
	}
	dc.OnClose(func() { c.once.Do(func() { close(c.closed) }) })
	dc.OnMessage(func(m webrtc.DataChannelMessage) {
		if !m.IsString {
			return // the page never sends file data
		}
		var pm pageMsg
		if json.Unmarshal(m.Data, &pm) != nil {
			return
		}
		select {
		case c.msgs <- pm:
		case <-c.closed:
		}
	})
	dc.SetBufferedAmountLowThreshold(lowWater)
	dc.OnBufferedAmountLow(func() {
		select {
		case c.low <- struct{}{}:
		default:
		}
	})
	return c
}

// recv waits for the next control message; timeout 0 waits as long as ctx.
func (c *channel) recv(ctx context.Context, timeout time.Duration) (pageMsg, error) {
	var t <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		t = timer.C
	}
	select {
	case m := <-c.msgs:
		return m, nil
	case <-c.closed:
		return pageMsg{}, errClosed
	case <-t:
		return pageMsg{}, errors.New("the browser stopped answering")
	case <-ctx.Done():
		return pageMsg{}, ctx.Err()
	}
}

func (c *channel) sendJSON(m pageMsg) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return c.dc.SendText(string(b))
}

// sendBinary sends a chunk, first waiting while too much is still queued.
func (c *channel) sendBinary(ctx context.Context, b []byte) error {
	for c.dc.BufferedAmount() > highWater {
		select {
		case <-c.low:
		case <-time.After(100 * time.Millisecond): // in case the signal raced
		case <-c.closed:
			return errClosed
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := c.dc.Send(b); err != nil {
		select {
		case <-c.closed:
			return errClosed
		default:
		}
		return err
	}
	return nil
}
