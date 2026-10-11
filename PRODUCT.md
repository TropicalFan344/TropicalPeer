# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

The interface is web technology (Svelte) rendered inside a desktop app shell (Wails + WebView2), shipping first on Windows. The Android app reuses the same web UI in a Capacitor shell, with the same Go engine built as an Android library (docs/plan-android.md); below 640px wide the shell switches to a phone layout.

## Users

The general public: people who want to move files to their own other devices or to people they know, including non-technical users. They are typically mid-task ("get this video from my PC to my friend", "move these photos to my laptop") and want the file to arrive without accounts, uploads to a cloud, or size limits.

## Product Purpose

TropicalPeer is a peer-to-peer file drop. Two devices pair once with a short code; after that either can send files to the other directly, across the internet, with no account and no cloud storage in between. Success means a first-time user can pair a device and get a file to arrive on the other side without help, and a returning user can send with one drag or click.

## Positioning

- **Works across the internet, not just the same Wi-Fi.** Unlike AirDrop or LocalSend, paired devices reach each other anywhere. Files travel device-to-device; they are never stored on a server.
- **Self-hosted and private.** A small rendezvous server only introduces devices and relays the pairing handshake. Anyone can run their own; there are no accounts, and transfers are end-to-end encrypted with keys that never leave the devices.
- **Grows into a private network.** The long-term direction is a ZeroTier-like virtual LAN between paired devices, with file drop as the first thing built on it.

## Operating Context

- Pairing happens once per device pair: one device shows a code (e.g. `7-orchid-tiger`, valid 5 minutes, single use), the other types it in. People read the code aloud, over a call, or in a chat message.
- After pairing, peers are remembered by cryptographic key. Sending is: click a peer and choose files, or drag files from the file manager onto the peer.
- Each peer has an accept policy on the receiving side: **ask first** (default), **accept automatically** (optionally only up to a size limit), or **block** (declined silently).
- The receiving app must be open and online to receive. On Android, "Stay ready to receive" (Settings, off by default) keeps it online in the background behind a quiet notification, and offers arrive as notifications.
- The rendezvous server address is configurable in Settings (host + port, default `localhost:7777`).

## Capabilities and Constraints

Built and working:
- Device identity (Ed25519 keypair; device ID like `K3GH-ULVS-22KS-TWCC`), editable device name.
- Pairing by code using a PAKE (CPace); the server cannot learn or forge the code.
- Per-peer settings: local nickname, accept policy, auto-accept size limit, custom download folder; remove peer.
- Sending one or many files over QUIC with SHA-256 verification, live progress and speed, cancel, retry; receiver discards partial files on failure.
- Receiving: accept/decline prompt (expires after 3 minutes), incoming progress, show file / open folder.
- Automatic updates from GitHub releases, verified against a signing key: computers replace the app and restart when idle (after a notice); Android downloads and opens the system installer.
- Pause and resume: either side can pause a running transfer (both keep what arrived) and either side can resume it; resuming an incoming one asks the sender's app to continue, so it must be open there.
- Resuming: an interrupted transfer (connection lost, either app quit) keeps its partial files on the receiver; the sender reconnects by itself for a few minutes and sends only the rest (verified by SHA-256 of the kept part), without asking the receiver again. Resume (Retry) continues the same way later; sending the same file again after restarts continues it too (the receiver is asked again in that case). Cancelling discards the partial files; untouched ones are cleaned up after 7 days.
- Rendezvous server binary (`cmd/server`) and a command-line client (`cmd/tpcli`).

Not built yet / constraints:
- Sending folders is not supported (files only).
- Connections across home routers use UDP hole punching, with an end-to-end encrypted relay through the rendezvous server as fallback (verified in a NAT simulator; not yet tested across real networks). The server must be reachable on TCP and UDP on its port, plus UDP on a second port (default +100) for router detection. Devices also ask their router to forward a port via UPnP/NAT-PMP when it supports it (on by default, can be turned off). A home-hosted server can do the same for its own ports with `server -upnp`.
- Virtual LAN feature: planned, not started.

Open decisions:
- Who operates a default public rendezvous server for the general public, versus each user self-hosting.

## Brand Commitments

- Name: **TropicalPeer**.
- Voice in shipped UI copy: plain, friendly, specific; errors say what happened and what to do next (e.g. "Moon Laptop is offline. Make sure TropicalPeer is open on it.").
- Languages: English, 繁體中文, 简体中文, 日本語, 한국어, Español, Français, Deutsch, Português (Brasil) and Русский, following the system language unless chosen in Settings. All text lives in `locales/*.json` (see locales/README.md); new UI text gets a key there, never a literal in code.

## Evidence on Hand

None yet: no logo or app icon (the window still shows the default Wails icon), no screenshots or marketing material, no users, testimonials, benchmarks or download numbers. Future work must not fabricate any of these.

## Product Principles

1. **Pair once, then it just works.** The one-time pairing carries the effort; every send after it should be one gesture.
2. **The receiver stays in control.** Nothing lands on a device its owner didn't allow, either per transfer or by an explicit per-peer rule.
3. **Private by construction, not by promise.** Servers only introduce devices; file contents and pairing secrets never pass through anything the user doesn't control.
4. **Say what's happening.** Connecting, waiting for the other side, sending, failed: every state is visible and every failure explains what to do.
5. **Approachable for non-technical people.** Network concepts (ports, servers, NAT) stay out of the main path; they live in Settings for those who need them.
