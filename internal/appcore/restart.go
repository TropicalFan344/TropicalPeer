package appcore

import (
	"context"
	"time"
)

// Restart stops the node and starts it again from the saved settings,
// without quitting the app. That applies what only takes effect on start
// (the listening port) and gives a stuck connection a clean slate.
// Running transfers end as cancelled and waiting offers are declined;
// finished transfers stay in the list.
func (c *Core) Restart() error {
	c.restarts.Lock()
	defer c.restarts.Unlock()

	c.nodeMu.RLock()
	n, stop, done := c.node, c.stopNode, c.nodeDone
	c.nodeMu.RUnlock()
	if n == nil {
		return c.startNode()
	}

	// Decline offers still waiting for an answer, and stop running transfers.
	c.mu.Lock()
	for _, po := range c.offers {
		select {
		case po.decision <- false:
		default:
		}
	}
	var running []*transferState
	for _, st := range c.transfers {
		if st.dto.active() {
			running = append(running, st)
		}
	}
	c.mu.Unlock()
	for _, st := range running {
		// Outgoing ones stop resumably; incoming ones end with the node.
		if st.stop != nil {
			st.stop()
		}
	}
	c.CancelInvite()

	// Remove the router forward, then close the node's sockets so the new
	// one can take the same port.
	cctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	n.StopPortMapping(cctx)
	cancel()
	stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}

	c.emit("server:status", ServerStatus{Address: n.Settings().ServerAddr})
	return c.startNode()
}
