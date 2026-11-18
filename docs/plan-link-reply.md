# Replies on share links plan

Someone who opened a share link can send files back to the person who shared
it, from the same browser page, with no app installed. It's off unless the
link's owner turns it on, and the owner approves every batch.

A link used to only let people read what was chosen for it. A reply writes to
the owner's device, so the design starts from what a stranger holding the link
could try.

## Status

| Step | What | Status |
|---|---|---|
| 0 | Prototype: Chrome to pion, reverse direction | Done: 2 GiB at 74.6 MB/s, hash matches; with a disk held to 5 MB/s the browser queues at most 4.1 MiB |
| 1 | Engine: `internal/linkshare/reply.go`, `transfer.PartFile`, `transfer.FreeSpace` | Done |
| 2 | Receiver page: pick files, wait for the answer, progress, cancel | Done |
| 3 | App: option on the link, offer prompt, Transfers row, notifications, all 10 languages | Done |
| 4 | Fixes from the design review (22/40): review screen, honest states, full file list, no Accept in notifications | Done |
| 5 | "Ask for files": a link that only collects files | Done |

## Decisions

| Question | Decided | Why |
|---|---|---|
| On by default? | No, per link, off | A link already pasted somewhere must not start accepting files |
| Approve each batch? | Always; no auto-accept | Whoever has the link can reply, and nothing proves who they are |
| Accept from a notification? | No: Decline only, accepting opens the app | Every file name should be seen before a stranger's files are taken |
| How long does the owner have? | 3 minutes on a reply; as long as the page stays open on an Ask link | The owner asked for these files, so a slow answer shouldn't fail the visitor |
| Where the prompt shows | The existing offer strip and notifications, worded for a link | One place to answer, on every platform |
| Where progress shows | A normal incoming row on Transfers | Saved files, notifications and Android's copy to Downloads come for free |
| Pause and resume | None | A browser loses access to a picked file after a reload |
| Folders | Files only, flattened to their names | The picker offers files; names can't reach outside the download folder |

## Who can do what

| Someone could | What stops it |
|---|---|
| Fill the disk | The owner approves the batch and its total; free space is checked first; a file can't grow past its declared size |
| Escape the download folder | Names go through `transfer.SafeName`; `.part` files are renamed only once complete |
| Pass off malware | Saved files carry the system's downloaded-from-the-internet flag (Windows, macOS); the prompt says anyone with the link could be this person |
| Flood the owner with prompts | At most 2 prompts per link at once; a page declined 3 times isn't asked about again; the owner can turn replies off |
| Send data without asking | File data outside an accepted batch ends that visitor |
| Stall forever | 60 seconds of silence during a batch ends that visitor |

What this doesn't cover: whoever serves the receiver page can read the link's
secret (see `plan-link-share.md`), so a dishonest server operator could offer
files too. The approval prompt is the defense.

## Protocol

The same DataChannel, after the checks in `plan-link-share.md`. JSON messages
are strings; file data is binary, in chunks of at most 64 KiB. `hello` gains
`reply: true` when replies are on. Older pages ignore it, and a page that
doesn't see it offers only the download.

```
page → {"type":"offer","files":[{name,size}],"note":"who they say they are"}
app  → {"type":"replyAsking","seconds":180}   (0 = no limit)
app  → {"type":"replyAnswer","ok":true}
       or {"type":"replyAnswer","error":"off|busy|declined|noanswer|limit|space|invalid"}

for each file, in order:
page → {"type":"put","index":0}, chunks, {"type":"fin","index":0,"sha256":"…"}
app  → {"type":"saved","index":0}
```

- The page cancels with `{"type":"replyCancel"}`, also while waiting for the
  answer. The connection stays up.
- The app stops a batch with `{"type":"replyStop","error":"stopped|disk|damaged"}`.
  The page answers `replyCancel`; chunks already on their way are dropped.
- With replies on, a finished download sends `{"type":"downloaded"}` instead
  of `done`, so the page can still reply. It counts as finished for "one
  person" links; the visitor ends when the page closes.
- An offer has at most 100 files: its message must fit pion's 64 KiB limit.
- A prompt that runs out answers `noanswer` and doesn't count as a refusal.
  A page turned down 3 times, or unanswered 5 times, gets `limit`.
- `{"type":"replies","reply":true}` tells open pages replies were switched.

## Ask for files

A link has two abilities: it gives files, it takes files. A share link gives,
and takes if its owner allows; an Ask link only takes. `hello` says `ask: true`
and carries the owner's `purpose`. It uses the same review, waiting and result
screens as a reply. A one-person Ask link is held by the first visitor and is
used up once they have sent files and closed the page. Up to 8 prompts can be
open at once: one shows on the strip, and all can be answered on the link's card.

pion hands messages over one at a time and waits for each, and the channel's
queue is short, so a slow disk slows the browser down instead of filling
memory.

## Testing

- Go tests (`internal/linkshare/reply_test.go`): off by default, saved
  content, hostile names, declines, prompt limits, oversize and damaged files,
  data without an offer, the page leaving, stopping on this device, no room.
- Desktop Chrome against the real app: accept, decline, cancel from the page,
  stop from the app, reply after a download, turning replies off.
- Chrome on Android, through the TURN relay: 20 MB, hash matches. The
  connection survives the system file picker (about a minute open).
- Desktop Chrome against the real app for Ask links: made in the dialog, held
  by one person, answered on the card, two people at once.
- Safari 26 on a Mac sending to an Ask link owned by the Android app, on another
  network through the live server and relay: 200 MB at 13.8 MB/s, hashes match.
  The phone's notification offers Decline only.
- Not tested yet: Firefox, iPhone Safari, and a phone browser on the review screen.
