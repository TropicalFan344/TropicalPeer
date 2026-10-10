# Plan: hole punching and relay

Status: **built** (items 1–8, 10 below); only the real-world test is still open.
Goal: two paired devices behind different home routers (NATs) connect without port forwarding, directly when possible and through an encrypted relay when not.

## Where we are

- Each device opens one UDP socket and runs QUIC on it (`quic.Transport` in `internal/node`).
- Devices register with the rendezvous server over TCP and report their LAN addresses plus the UDP port. The server adds the public IP it sees on the **TCP** connection.
- The sender looks the peer up and dials every candidate address at once.

What breaks across the internet: the public address the server records comes from TCP, so it says nothing about the UDP mapping the router created for the QUIC socket, and the receiver's router drops unsolicited incoming UDP. Today only port forwarding, IPv6 without a firewall, or a VPN works.

## Design

### 1. Learn our real UDP address (STUN-style, on the same socket)

- The server also listens on **UDP** (same port number, e.g. 7777/udp).
- The device sends a small *probe* from its QUIC socket using `quic.Transport.WriteTo`; the server replies with the address it saw. Replies arrive through `Transport.ReadNonQUICPacket`, so QUIC and probes share one socket and therefore one router mapping.
- Probes carry the device ID and an Ed25519 signature over a timestamp, so nobody can register a mapping for someone else.
- Repeat every ~25 s to keep the mapping alive (most routers forget idle UDP after 30–120 s), and report the result in the presence registration as a new candidate type: `reflexive`.
- **NAT type check:** probe a second server address (second port is enough for most cases). Same mapping from both → "easy" NAT (punchable). Different mapping → "symmetric" NAT (go straight to relay for pairs where both sides are symmetric).

### 2. Coordinated hole punch

The presence connection becomes two-way so the server can push messages to an online device.

1. Sender asks the server: `connect_request {to: peerID, candidates}`.
2. Server pushes `connect_offer {from, candidates, nonce}` to the receiver over its presence connection, and returns the receiver's candidates to the sender.
3. Both sides immediately send small **punch packets** to every candidate of the other for up to ~3 s (every 100–200 ms). Punch packets are authenticated (HMAC keyed from both devices' identity keys + nonce), so a stranger can't trigger traffic.
4. As soon as either side receives a valid punch, the path is open; the sender dials QUIC to that address. TLS key pinning is unchanged, so a wrong or hijacked path simply fails the handshake.
5. LAN, IPv6 and reflexive IPv4 candidates race together ("happy eyeballs"); the first QUIC handshake to finish wins, as today.

### 3. Relay fallback (TURN-style, end-to-end encrypted)

- If no direct path works within ~3 s (or both NATs are symmetric), both devices ask the server for a **relay session** for this pair.
- The server allocates a session ID and forwards UDP packets between the two devices: `[8-byte session ID][QUIC packet]`. It never sees plaintext: QUIC runs end-to-end through it with the same pinned keys.
- Client side: a small `relayPacketConn` implementing `net.PacketConn` wraps the socket and adds/removes the header; QUIC (`quic.Dial` / `Listen` on that PacketConn) works unmodified.
- Server protection: sessions only between two registered, online devices that both requested it; per-session and global bandwidth caps; idle timeout; a `-relay=false` flag for people who self-host and don't want to carry traffic; logs count bytes, never contents.

### 4. Upgrade and keepalive

- Start relayed, keep punching in the background, and move the QUIC connection to the direct path when it opens (quic-go supports connection migration with `Conn.AddPath`), so big transfers don't stay on the relay.
- QUIC keepalives (already 15 s) keep whichever path is in use alive.

### 5. What the user sees

- Nothing new on the happy path. While connecting, the transfer card says "Finding a path to Moon Laptop…".
- A small path label on active transfers: **Direct** or **Relayed** (with a tooltip explaining relayed transfers are still end-to-end encrypted, just slower).
- Settings → Network: "Allow relayed transfers" (on by default), and a read-only line showing the detected NAT type, for troubleshooting.
- Clear failure copy when nothing works: "Couldn't reach Moon Laptop directly, and the server doesn't allow relaying. Ask whoever runs your server to enable it, or forward UDP port 7778."

## Work breakdown

| # | Piece | Where | Status |
|---|---|---|---|
| 1 | UDP probe endpoint + signed probe/reply | `internal/server/udp.go`, `internal/rendezvous/udp.go` | ✅ Built |
| 2 | Shared-socket probing via `ReadNonQUICPacket` | `internal/node/connect.go` | ✅ Built (probe every 20 s also keeps the NAT mapping alive) |
| 3 | Two-way presence channel (server push) | `internal/rendezvous/presence.go`, `internal/server/server.go` | ✅ Built |
| 4 | Punch coordinator | `internal/node/connect.go` | ✅ Built. Punch packets carry only a nonce: receivers ignore them (their job is done by being sent), so they need no authentication. |
| 5 | Router (NAT type) detection | `internal/node/natdetect.go`, `internal/server/udp.go` (`ServeUDPAlt`) | ✅ Built. Probes to the server's second UDP port (default listen port + 100) reveal symmetric routers; a filter-test packet sent from that port before the device ever contacted it reveals lenient vs port-restricted filtering. Devices report the profile; the server passes it on in introductions; `rendezvous.DirectHopeless` then skips the 2 s head start (relay after 0.3 s, still letting same-LAN addresses win). |
| 6 | Relay sessions on the server + relay socket | `internal/server/udp.go`, `internal/node/relay.go` | ✅ Built. The relay uses its **own** UDP socket per device (not the QUIC socket), because quic-go only queues 32 non-QUIC packets. |
| 7 | Path upgrade relay → direct | `internal/node/upgrade.go`, `internal/transfer`, `internal/protocol` | ✅ Built. QUIC connection migration can't change the *peer's* address, so the upgrade is done in the transfer protocol: files travel as offset-tagged segments; while relayed, the sender re-introduces itself every 5 s (60 s when routers say direct is hopeless) without a new relay session, and when a direct connection comes up it joins the transfer (`Hello{Join}`), cuts the current segment after the chunk in flight and continues the same file at the same offset. `TestUpgradeRelayToDirect` switches mid-file (≈9 of 48 MB) every run, byte-identical, in ~1–3 s instead of ~6 s relay-only. |
| 8 | UI: "Finding a path…", Relayed label | `frontend/src/lib/components/TransferRow.svelte`, `PeerCard.svelte` | ✅ Built (an "Allow relayed transfers" setting was not added; the server operator decides with `-relay`) |
| 9 | Real-world test | you + a friend | ⏳ Open |
| 10 | Router port forwarding (UPnP-IGD, NAT-PMP fallback) | `internal/portmap`, `internal/node/natdetect.go` | ✅ Built, on by default (Settings → Network). The forwarded address is registered first; the lease (1 h) is renewed every 30 min and the forward is removed on graceful exit. Mappings on routers whose own external address is private or carrier-grade NAT are discarded. The rendezvous server has the same option for home hosting: `server -upnp` forwards TCP+UDP on its port and UDP on the detection port (exact port numbers, all-or-nothing), prints the address to share, renews the leases, and removes the forwards on Ctrl+C or console close (verified on a real router). **Verified on a real router**: UPnP forwarded UDP to this PC with a public external address, and the router confirmed the forward was gone after exit. |

## How it's tested

- **NAT simulator** (`internal/netsim`): an in-memory internet with full-cone, address-restricted, port-restricted and symmetric routers. `TestNATMatrix` runs all 25 sender/receiver placements (including "no router"), each pairing and sending a 5 MB file:
  - every pair delivers the file intact;
  - every pair without a symmetric NAT connects **directly** (typically < 100 ms);
  - pairs involving a symmetric NAT that can't be punched go through the **relay** (~2 s to connect).
- `TestRouterDetection`: every simulated placement is classified correctly (public / full-cone / address-restricted → open; port-restricted → restricted; symmetric → strict).
- With detection, `TestNATMatrix` also checks that the prediction matches reality for all 25 pairs and that relayed pairs connect in < 1.5 s (measured ~0.3 s; direct ~5–200 ms).
- `TestNoRelayFailsClearly`: two symmetric NATs and a server started with `-relay=false` fail with an explanation instead of hanging.
- Real sockets: the CLI smoke test (server + two `tpcli` devices on one machine) connects directly through the new introduction flow.

Still to do: a test that relayed bytes are opaque to the server (they are QUIC/TLS ciphertext by construction), and a home Wi-Fi ↔ phone-hotspot test across real NATs.

## Open questions

- Where the public rendezvous/relay server lives for general-public users (PRODUCT.md open decision); relay bandwidth costs money, so this needs an answer before a public release.
