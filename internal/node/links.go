package node

import (
	"context"

	"TropicalPeer/internal/linkshare"
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
// doesn't support links (or hasn't been reached yet). It stays known while
// the presence connection is down: a link has a connection of its own, and
// a phone drops presence whenever the app is briefly in the background
// (such as while picking the files to share).
func (n *Node) LinksURL() string {
	s, _ := n.linksURL.Load().(string)
	return s
}
