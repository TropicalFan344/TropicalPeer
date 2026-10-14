# Share by link plan

The sender's app makes a link, shown as a QR code. Anyone who opens it in a
browser, with no app installed, downloads the files straight from the
sender's device over WebRTC. The sender chooses "first person only" or
"anyone with the link", sees each receiver under a generated name, and can
cancel any one of them. The rendezvous server only introduces the two sides,
as it does today; it never sees the files or the link's secret.

Paired devices keep using QUIC. Links are a separate, smaller path for people
who don't have the app.

## Status

| Step | What | Status |
|---|---|---|
| 0 | Prototype: server WebSocket + TURN, `tpcli share` sender, bare receiver page | Mostly done: 1 GB PC → Android Chrome on another network in 126 s (7.9 MB/s, through TURN), SHA-256 matches; desktop Chrome locally 40 MB/s; wrong key rejected. Left: Safari |
| 1 | Server: HTTPS, `/ws` signaling, embedded TURN, receiver page, `-domain` autocert | Done, plus DuckDNS DNS-01 certificates and a port check at startup |
| 2 | Engine: `internal/linkshare` (pion/webrtc sender), appcore methods and events | Done; links reconnect to the server for 15 minutes after losing it |
| 3 | Desktop UI: Links page, QR code, receivers list; receiver page styled per DESIGN.md; all 10 locales | Links page done (10 locales; drop files on it). Left: receiver page in 10 languages, Safari |
| 4 | Android sender (same engine; Kotlin only for the share sheet entry) | |
| 5 | README: running a server with links, what the page host can and can't see | |

## Decisions

The open questions were put to Jev (TypeSafe's decision model, `jev-1.13.0`)
with the project's principles and the browser constraints as state. Its
picks, with how sure it was, and what was decided:

| Question | Jev's pick (confidence) | Decided |
|---|---|---|
| Receiver page implementation | TypeScript page with its own small protocol (1.00) | Yes. Go to WASM would bundle megabytes and quic-go doesn't run in a browser anyway |
| Link authentication | Secret in the URL fragment, MACing both DTLS fingerprints (0.91) | Yes |
| Relay when direct fails | TURN embedded in the server (0.70) | Yes |
| Approve receivers by default | Start right away, "Ask me first" per link (0.68) | Yes |
| Default expiry | Until the sender stops it or quits the app (0.69) | Yes |
| Default receiver limit | First person only (0.64) | Yes |
| Receiver names | Sender's app generates them (0.59) | Yes. A browser could claim any name; the sender's app is the side that has to trust it |
| UI placement | New rail page "Links" (0.52) | Yes, see Desktop UI |
| HTTPS | Built-in autocert with `-domain`, plus a plain-HTTP mode for behind a reverse proxy (0.37) | Low confidence; proposed, confirm |
| Where the page is served from | Server serves it; a setting can point links at a separate static host (0.34) | Low confidence; proposed, confirm |

The last two came back close to even between the options. They are judgment
calls about who runs servers and how much they trust the operator, so they
stay open until the owner confirms them.

## How it works

```
  Sender's app ──TCP presence (existing)──┐
       │                                  ▼
       │                       Rendezvous server
       │                     HTTPS: /r/<id> page, /ws signaling
       │                     UDP: TURN (fallback only)
       │                                  ▲
       └──── WebRTC DataChannel ──── Browser (scanned the QR code)
                (direct, or through TURN as DTLS ciphertext)
```

### The link

`https://<page host>/r/<linkID>#<secret>`

- `linkID`: 10 random bytes, base32. The server routes by it.
- `secret`: 16 random bytes, base64url, in the fragment. Browsers never send
  the fragment to any server, so the server can't learn it.
- The QR code encodes the whole URL. The link lives only in the sender's
  app's memory; it ends when the sender stops it or quits.

### Signaling

Each open link has its own TCP connection from the sender's app to the
server (newline JSON, like pairing), so it doesn't touch presence. The
server announces the `links` capability and its page URL in `registered`.

- `link_host {link}` → `link_hosted {webURL}`: start routing a link; closing
  the connection stops it.
- `link_visitor {session, ice}`: the server tells the sender someone opened
  it, with TURN credentials for that session.
- `link_signal {session, data}`: SDP and ICE candidates, both directions.
- `link_drop {session}`: either side ends a visitor.

The browser connects to `wss://<server>/ws` and sends `{"type":"open","link":id}`.
The server gives it a session ID and relays `signal` messages between it and
the sender. If the link isn't open, the page says "This link has ended or the
sender is offline." The server also hands both sides short-lived TURN
credentials (HMAC of a per-start server secret, TURN REST style, valid for the
session's lifetime).

The server enforces nothing about limits; the sender's app decides who gets
files. The server only rate-limits opens per IP and caps visitors per link
(e.g. 50 waiting) so a leaked link can't flood the sender.

### Authentication

A server that relays signaling could swap in its own DTLS certificates and
sit in the middle. To stop that, once the DataChannel opens, each side sends:

```
HMAC-SHA256(secret, "tropicalpeer-link-v1" | role | linkID | session
                    | sender fingerprint | browser fingerprint)
```

with the fingerprints read from its own local and remote descriptions. In the
middle the two legs have different fingerprints, so the MACs can't match.
Neither side sends anything else until the other's MAC checks out. After
that, DTLS carries everything, so there's no second encryption layer.

What this doesn't cover: whoever serves the receiver page can serve code that
reads the fragment. That's why the page host is configurable (see
"Page hosting").

### Data protocol

One ordered, reliable DataChannel. JSON control messages are strings; file
data is binary.

1. Sender → `{"type":"hello","name":"Orchid Tiger","files":[{name,size}],"total"}`.
   The page shows "Orchid Tiger: you're receiving 3 files (1.2 GB) from Moon
   Laptop".
2. Browser → `{"type":"get","index":0}` once the receiver presses Download
   (the browser needs a click to save files anyway).
3. Sender streams 64 KiB binary chunks (Safari's message limit is the
   tightest), with backpressure through `bufferedAmountLowThreshold`, then
   `{"type":"end","index":0,"sha256":"…"}`. The hash comes at the end so the
   sender doesn't read big files twice.
4. The browser hashes as it goes (incremental SHA-256 from a small library;
   WebCrypto can't hash a stream). If the hash doesn't match, it deletes the
   file and says so.
5. Several files: "Download all" saves one store-only zip streamed in the
   page (no compression, so it stays fast); single files can also be saved
   one by one.

Cancel from the sender: `{"type":"cancel"}`, then close. Cancel from the
browser: close the channel. No resume in v1; a dropped download starts again
from the link.

### Saving in the browser

| Browser | How |
|---|---|
| Chrome / Edge on desktop | `showSaveFilePicker`, writing straight to disk |
| Chrome on Android, Firefox, Safari 15.4+ | Service worker stream: the page feeds a `ReadableStream` that a service-worker URL serves as a download |
| Anything else | In-memory Blob, with a size warning above ~500 MB |

iPhone Safari with very large files is the riskiest case. Step 0 tests it
first.

## Server (`internal/server`, `cmd/server`)

- New HTTP listener (`net/http`), on 443 by default when links are enabled:
  - `GET /r/<id>`: the receiver page (built from `web/`, embedded with
    `go:embed`), with a strict CSP and no third-party scripts.
  - `GET /ws`: WebSocket signaling (gorilla/websocket is already an indirect
    dependency through Wails).
- TURN through pion/turn on UDP and TCP 3478, relay ports UDP 40100-40119
  (outside the Windows dynamic range, where Hyper-V/WSL reserve blocks such
  as 49162-49261). Credentials are per session (TURN REST style). TODO:
  `-relay-rate` for TURN.
- New flags: `-domain` (Let's Encrypt via `autocert`, TLS-ALPN on 443),
  `-cert`/`-key` (own certificate), `-http` (plain HTTP behind a reverse
  proxy), `-turn-port`, `-links=false` to turn the whole feature off.
- `-upnp` also forwards 443 and the TURN port.
- `registered` gains `caps: ["links"]` and `webURL`. Older servers don't send
  them, and the Links page explains that the server needs an update.

## Engine (`internal/linkshare`)

- Uses pion/webrtc; it reads `transfer.Source`, so desktop paths and Android
  document descriptors both work.
- `Link {ID, URL, Files, Limit (one | unlimited), AskFirst, Created}`;
  `Visitor {Session, Name, Status (waiting | asking | sending | done |
  cancelled | failed), Done, Total, Speed}`.
- Names: two words from the `internal/pairing` word list, unique within the
  link.
- With "first person only", the first visitor who passes the MAC check takes
  the link. Later visitors get "This link was already used". If that first
  person's download fails, the sender can reopen the link from the row.
- Appcore methods (same names on desktop and mobile `Call`): `CreateLink`,
  `StopLink`, `CancelVisitor`, `AllowVisitor`, `Links`.
- Events: `link:update`, `link:visitor`.

## Desktop UI

Follows DESIGN.md.

- **Rail:** "Links" between Transfers and Settings (18px Lucide `Link` icon),
  with a count badge for active visitors, like Transfers.
- **Links page:** the header action is "New link" (pick files, or drop them on
  the page). Each link is a bordered block with the QR code on the left, and
  on the right the file summary, the URL with a copy button, the limit ("First
  person only" / "Anyone with the link"), "Ask me first", and a secondary
  "Stop link". Below it, one row per receiver in the Transfer Row style: their
  bed and generated name, progress and speed, and Cancel.
- **No Peers shortcut in v1:** Jev preferred the Links page alone over the
  page plus a shortcut (0.68 to 0.20), and the Peers header keeps its one
  action.
- **Ask me first:** reuses the Offer Strip the other way round ("Orchid Tiger
  wants to download 3 files"), with Allow / Decline.
- **Not available:** when the server lacks `links`, the page shows one
  sentence and a link to the Network page, like the other network states.
- **Phone layout:** the same page stacked; the QR code fills the width so
  someone next to you can scan it.

All new text is keys in `locales/*.json`. The receiver page picks its
language from `navigator.language` from the same files, bundled at build
time.

## Receiver page (`web/`)

Its own small Vite + Svelte build, sharing `app.css` tokens and fonts with
the app so it looks like TropicalPeer. It's one screen: sender's name, the
receiver's generated name, file list, Download, progress, then "Done. Check
your downloads". Target size is under 150 KB, gzipped, without fonts.

## Page hosting

By default links point at the server's own `/r/` page. Settings → Network gets
a "Link page address" field. People who don't want to trust the server
operator with the page code can host the `web/` build themselves (a release
asset, e.g. on GitHub Pages) and point links at it; the page then connects to
the server's `/ws` (CORS allowed for that origin only). The README says
plainly that whoever serves the page could read the link's secret.

## Testing

- **Step 0 targets:** a 5 GB file to Chrome on the Android phone (over `adb`;
  `adb shell am start -a android.intent.action.VIEW -d <url>`, and
  `adb forward tcp:9222 localabstract:chrome_devtools_remote` for the
  console) on Wi-Fi and on mobile data; to Safari on the Mac
  (`ssh mac`, 192.168.1.111); and a forced-TURN run (`iceTransportPolicy:
  "relay"`). Record the throughput of each.
- **Go tests:** signaling routing, the MAC check (a man-in-the-middle relay in
  the test fails it), limits and cancel, visitor flood caps. Two pion peers in
  one process stand in for the browser.
- **Page tests:** a hash mismatch deletes the file; the zip stream opens
  correctly on Windows, macOS and Android.

## Findings so far

- pion on Android needs `-ldflags=-checklinkname=0` (its `wlynxg/anet`
  dependency); the gomobile build will need the same.
- Windows reserves UDP port blocks for Hyper-V/WSL; the relay range must
  avoid them (hence 40100-40119).
- On the test router (CHT GEMTEK I-040G-W), many UPnP forwards let the first
  packet in but replies never get out (7777 and 7790 worked; 7791, 8443,
  9443 and others didn't, same binary). Forwards added by hand work. The
  server should check its ports from outside and say which aren't reachable.
- Incoming TCP 443 doesn't reach that home connection at all (likely the
  ISP), so Let's Encrypt's TLS-ALPN check can't work there. With a
  `.duckdns.org` domain the server gets the certificate by DNS-01 through the
  DuckDNS API (`-duckdns-token`, default `$DUCKDNS_TOKEN`) and serves HTTPS on
  another port (`-web :8443`). It also keeps the DuckDNS address current.
- Chrome on Android has `showSaveFilePicker`; on phones the page skips it and
  downloads straight to Downloads.

## Risks

- Large files on iPhone Safari may hit memory limits, even through the
  service worker. If they do, the page says so above a size, and the plan
  needs revisiting.
- Browser DataChannels top out well below QUIC (roughly 100 to 500 Mbit/s,
  depending on the browser).
- A home-hosted server needs a domain name and port 443 reachable for
  Let's Encrypt. A dynamic DNS name works; a bare IP doesn't.
