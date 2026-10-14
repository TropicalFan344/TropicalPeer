package node

import (
	"context"

	"TropicalPeer/internal/linkshare"
	"TropicalPeer/internal/rendezvous"
	"TropicalPeer/internal/transfer"
)

// ShareLink opens a link that browsers can download files from (see
// package linkshare). The link lasts until Stop or until ctx ends.
func (n *Node) ShareLink(ctx context.Context, files []transfer.Source, opts linkshare.Options) (*linkshare.Share, error) {
	if opts.SenderName == "" {
		opts.SenderName = n.Identity.Name
	}
	s, err := linkshare.Start(ctx, n.rendezvous(), files, opts)
	if err != nil {
		return nil, err
	}
	go func() {
		select {
		case <-ctx.Done():
			s.Stop()
		case <-s.Done():
		}
	}()
	return s, nil
}

// LinksURL is where the rendezvous server serves link pages, or "" when it
// doesn't support links (or we aren't connected to it).
func (n *Node) LinksURL() string {
	if p, _ := n.presence.Load().(*rendezvous.Presence); p != nil {
		return p.LinksURL
	}
	return ""
}
