---
target: critique (app shell, all screens)
total_score: 30
max_score: 40
na_heuristics: 
p0_count: 0
p1_count: 2
target_identity: "file:F:\\Projects\\TropicalPeer\\frontend\\src\\App.svelte"
target_fingerprint: "sha256:2dd6ac523aab26b1f33e4c886c5775321e1a1d02cc09f1d70739958c4784b274"
target_path: "F:\\Projects\\TropicalPeer\\frontend\\src\\App.svelte"
timestamp: 2026-11-10T18-50-57Z
slug: frontend-src-app-svelte
---
Method: dual-agent (A: design review · B: detector + overlay). Branch first-pairing.

## Design Health Score
| # | Heuristic | Score | Key Issue |
|---|---|---|---|
| 1 | Visibility of System Status | 3 | Transfer status excellent; device cards show no online/offline presence |
| 2 | Match System / Real World | 4 | "Is a code showing on the other device?"; Devices naming consistent |
| 3 | User Control and Freedom | 3 | Card ⋯ dropdown has no Escape |
| 4 | Consistency and Standards | 3 | Three empty-state grammars; countdown drawn as wave |
| 5 | Error Prevention | 3 | Sending to an offline device fails only afterwards |
| 6 | Recognition Rather Than Recall | 3 | Compose undiscoverable; hover-only corner buttons; unlabeled folder icon |
| 7 | Flexibility and Efficiency | 3 | Drag, paste, right-click, Alt+arrow reorder |
| 8 | Aesthetic and Minimalist Design | 3 | Offer + text strip + toasts pile up (half window / full phone) |
| 9 | Error Recovery | 3 | "Removed you" card is a dead end (no Pair again) |
| 10 | Help and Documentation | 2 | Pairing footers good; nothing for "never connects" |
| **Total** | | **30/40** | **Good** |

## Design Specificity Verdict
Strongly product-grounded. Empty state now carries the garden. New risk: 7 bed colours collide early (two devices got the same silver-leaf bed).
Detector: CLI 0 findings. Overlay 4 rules / 10 views, all false positives (gray-on-color 6.1:1 rail-muted; float shadow; wave clipping x2). nested-cards and layout-transition from the last run are gone. Phone width clean.

## Priority Issues
- [P1] No presence on device cards (Peer in api.ts:46-55 has no online field); offline found only via error toast after picking files. Needs engine presence first. Fix: "Offline · seen 2 h ago" in policy slot, soften card, explain before picker. (/impeccable shape)
- [P1] Notification pile-up: offer + text strip + up to 4 toasts (state.svelte.ts:426). Fix: drop duplicate toasts (Paired with… at AddPeerDialog.svelte:80, Sent…), cap 2, fold texts into one row during an offer. (/impeccable distill)
- [P2] Card ⋯ menu: no Escape, no role=menu/arrow keys, no focus move; card name lacks status (PeerCard.svelte:137). Fix: menu pattern + aria-describedby status. (/impeccable harden)
- [P2] Blocked card opacity-60 (PeerCard.svelte:139) puts Blocked at 2.75:1, ID 2.63:1. Fix: fade bed only. (/impeccable polish)
- [P2] Flat pairing end: Done + duplicate toast. Fix: primary "Send files to X", quiet Done, no toast. (/impeccable onboard)

## Persona Red Flags
Jordan: server unreachable shown only on rail; countdown pressure; no nudge to send after pairing.
Sam: no skip link (7 tabs to Add device); menu no Escape; error toasts polite only (Toasts.svelte:19); card status not in name.
Casey: Accept/Decline at screen top; toasts at top; long names wrap 3 lines.
Relative receiving: red field reads as danger; "Declines in 2:49" pressure; folder in small blush; Show file toast gone after 8s.

## Minor Observations
3-line name wrap (PeerCard.svelte:142); unrounded MB in auto-accept limit; DE offer list truncates the count; RU gendered "прислал(а)"; Pause offered while waiting for accept; code label centred vs code left-aligned.

## Questions to Consider
Is heliconia right for an invitation? Do device IDs belong on every card? Why does an offline device look like an online one?
