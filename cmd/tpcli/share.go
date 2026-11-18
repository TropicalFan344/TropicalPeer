package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"TropicalPeer/internal/linkshare"
	"TropicalPeer/internal/node"
	"TropicalPeer/internal/transfer"
)

// acceptReplies saves every file people send back in dir, without asking.
func acceptReplies(dir string) *linkshare.ReplyHooks {
	return &linkshare.ReplyHooks{
		Dir: func() string { return dir },
		Ask: func(_ context.Context, o linkshare.ReplyOffer) linkshare.ReplyAnswer {
			fmt.Printf("\n%s sends back %d file(s), %s\n", o.Name, len(o.Files), formatSize(o.Total))
			return linkshare.ReplyAccepted
		},
		Started: func(o linkshare.ReplyOffer, _ func()) linkshare.ReplyRun {
			return linkshare.ReplyRun{
				Progress: func(int, int64) {},
				Finished: func(saved []string, err error) {
					fmt.Printf("%s: saved %v (%v)\n", o.Name, saved, err)
				},
			}
		},
	}
}

func shareLink(ctx context.Context, n *node.Node, args []string) error {
	limit := linkshare.FirstOnly
	if len(args) > 0 && args[0] == "-unlimited" {
		limit, args = linkshare.Unlimited, args[1:]
	}
	var replies *linkshare.ReplyHooks
	if len(args) > 1 && args[0] == "-accept-replies" {
		replies, args = acceptReplies(args[1]), args[2:]
	}
	if len(args) == 0 {
		return errors.New("usage: tpcli share [-unlimited] [-accept-replies <folder>] <file>...")
	}
	files, err := transfer.FileSources(args)
	if err != nil {
		return err
	}
	start := map[string]time.Time{}
	s, err := n.ShareLink(ctx, files, linkshare.Options{
		Limit:        limit,
		Replies:      replies,
		AllowReplies: replies != nil,
		Log:          func(f string, a ...any) { fmt.Printf("  "+f+"\n", a...) },
		OnUpdate: func(v linkshare.Visitor) {
			switch v.Status {
			case linkshare.Sending:
				if start[v.Session].IsZero() {
					start[v.Session] = time.Now()
				}
				secs := time.Since(start[v.Session]).Seconds()
				speed := ""
				if secs > 0.5 {
					speed = formatSize(int64(float64(v.Done)/secs)) + "/s"
				}
				fmt.Printf("\r%s: %s of %s %s   ", v.Name, formatSize(v.Done), formatSize(v.Total), speed)
			case linkshare.Done:
				secs := time.Since(start[v.Session]).Seconds()
				fmt.Printf("\n%s: done, %s in %.1fs (%s/s)\n", v.Name, formatSize(v.Done), secs, formatSize(int64(float64(v.Done)/max(secs, 0.001))))
			default:
				msg := ""
				if v.Error != "" {
					msg = ": " + v.Error
				}
				if v.Relayed {
					msg += " (relayed)"
				}
				fmt.Printf("\n%s: %s%s\n", v.Name, v.Status, msg)
			}
		},
	})
	if err != nil {
		return err
	}
	fmt.Printf("Link (%s): %s\nPress Ctrl+C to stop sharing.\n", limit, s.URL)
	select {
	case <-ctx.Done():
	case <-s.Done():
		return errors.New("lost the connection to the rendezvous server")
	}
	return nil
}
