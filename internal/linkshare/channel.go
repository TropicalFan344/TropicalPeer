package linkshare

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

var (
	errClosed    = errors.New("the browser disconnected")
	errCancelled = errors.New("the receiver cancelled")
)

// channel turns a DataChannel's callbacks into blocking calls.
type channel struct {
	dc   *webrtc.DataChannel
	open chan struct{}

	msgs   chan pageMsg
	closed chan struct{}
	once   sync.Once

	low chan struct{} // signalled when the send buffer drains

	cancelled  chan struct{} // closed when the page says "cancel", even mid-file
	cancelOnce sync.Once
}

func newChannel(dc *webrtc.DataChannel) *channel {
	c := &channel{
		dc:     dc,
		open:   make(chan struct{}),
		msgs:   make(chan pageMsg, 16),
		closed: make(chan struct{}),
		low:    make(chan struct{}, 1),

		cancelled: make(chan struct{}),
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
		if pm.Type == "cancel" {
			c.cancelOnce.Do(func() { close(c.cancelled) })
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
		case <-c.cancelled:
			return errCancelled
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	select {
	case <-c.cancelled:
		return errCancelled
	default:
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

// flush waits (up to a second) for queued messages to leave, so a last
// message isn't cut off by closing the channel right after it.
func (c *channel) flush() {
	deadline := time.Now().Add(time.Second)
	for c.dc.BufferedAmount() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // and through the SCTP stack
}
