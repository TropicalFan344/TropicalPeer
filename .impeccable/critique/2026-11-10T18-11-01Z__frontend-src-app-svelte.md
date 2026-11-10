---
target: critique (app shell, all screens)
total_score: 28
max_score: 40
na_heuristics: 
p0_count: 0
p1_count: 2
target_identity: "file:F:\\Projects\\TropicalPeer\\frontend\\src\\App.svelte"
target_fingerprint: "sha256:2dd6ac523aab26b1f33e4c886c5775321e1a1d02cc09f1d70739958c4784b274"
target_path: "F:\\Projects\\TropicalPeer\\frontend\\src\\App.svelte"
timestamp: 2026-11-10T18-11-01Z
slug: frontend-src-app-svelte
---
Method: dual-agent (A: design review · B: detector + overlay)

## Design Health Score
| # | Heuristic | Score | Key Issue |
|---|---|---|---|
| 1 | Visibility of System Status | 4 | Every state has a line (finding a path, waiting, relayed, decline countdown, server ping) |
| 2 | Match System / Real World | 3 | Nav "Peers" vs copy "device"; phone-width browser shows Explorer copy (isAndroid-gated) |
| 3 | User Control and Freedom | 3 | Window-level Escape closes all stacked dialogs |
| 4 | Consistency and Standards | 3 | Two identical primaries on empty Peers/Links; Settings mixes apply-now with Save bar |
| 5 | Error Prevention | 3 | Half-typed code leaves Pair disabled without reason |
| 6 | Recognition Rather Than Recall | 2 | Pairing never says who does what; right-click/paste/Alt+arrow undiscoverable; hover-only ⋯ |
| 7 | Flexibility and Efficiency | 3 | Good accelerators, all hidden |
| 8 | Aesthetic and Minimalist Design | 3 | Phone offer strip ~280px; rail ID truncated |
| 9 | Error Recovery | 3 | Toasts cover Retry/Resume on Transfers |
| 10 | Help and Documentation | 1 | Only Settings ⓘ; no first-run guidance |
| **Total** | | **28/40** | **Good** |

## Design Specificity Verdict
Strongly product-specific (offer strip, wave pavement, garden beds, shoreline). Weak spot: first-run empty state carries none of the garden.
Detector: CLI 0 findings (frontend/src, web/, --no-config). Overlay: 6 rules on 9 views; dark-glow (self-detection), gray-on-color (rail-muted ~6.1:1), thin-border-wide-shadow (DESIGN float shadow), clipped-overflow-container (wave band) are false positives. Real minor: layout-transition width at SettingsPage.svelte:566; nested-cards code well at AddPeerDialog.svelte:164.

## Priority Issues
- [P1] Pairing doesn't say who does what and defaults to generating a ticking code. Fix: ask "Do you have a code?" first; start countdown only on Show; add "On the other device: Add peer → Enter a code". (/impeccable onboard)
- [P1] Dialog.svelte:15-17 window-level Escape, no focus trap/initial focus/restore. Fix: native <dialog> showModal or trap+restore, topmost-only Escape. (/impeccable harden)
- [P2] Empty Peers/Links duplicate btn-primary (PeersPage.svelte:60,71) and lack garden. Fix: hide header button when empty; ghost bed + 3-step line. (/impeccable onboard)
- [P2] Toasts cover row actions; ru status truncates size. Fix: move toasts / offset list; wrap status on desktop. (/impeccable adapt)
- [P3] Network jargon in main Settings. Fix: This device / Connection groups, Advanced for technical rows; Save bar scoped. (/impeccable distill)

## Persona Red Flags
Jordan: double Add peer, unrequested ticking code, "Peers", no tab hint, silent disabled Pair.
Sam: no focus trap; tabs without arrow keys/tabpanel (AddPeerDialog.svelte:133-151); card name lacks policy/status; ⋯ invisible until focus; offer only polite live; unlabeled stacked Accept/Decline; focus ring on rail low contrast.
Casey: offer strip and Add peer at top; download path truncated.
Grandma: rendezvous/listening port in Settings; "Declines in 2:50" pressure; red "Blocked" on unselected radio.

## Minor Observations
Red bed beside red "Removed you"; disabled primary at 45% looks active; peer settings scrolls at 720px; numbers not locale-formatted; countdown wave stretches the wave rule; PRODUCT.md says no folder sending but menu has working Send folder (PRODUCT.md stale).

## Questions to Consider
Why is Show my code the default? Should a declined send offer Retry? Does Grandma need to know a rendezvous server exists?
