---
version: 1
slug: "frontend-src-app-svelte"
primary_target: "frontend/src/App.svelte"
related_targets: []
---

# Surface: TropicalPeer app shell (all screens)

Scope: the desktop app window: Peers, Transfers, Settings, dialogs, incoming-offer prompts, toasts.
Visitor mode: Operate.

Audience and job: general public, often non-technical, mid-task: pair a device once, then send or receive files with one gesture. Frequency: a few times a day; sessions are short.
Constraints: must not feel childish/toy-like or busy/cluttered (user). Keep every working behaviour (pairing, policies, drag-and-drop, cancel/retry, live progress). Follows the OS light/dark setting.

## Direction contract

THESIS: Grown-up tropical modernism after Roberto Burle Marx: flat garden-bed colour fields and one Copacabana wave pavement that carries every transfer. Refuses the category default of white cards, avatar bubbles on a radar and a single blue button.

OWN-WORLD: Deep jungle-green rail field meeting a cool limestone-pavement ground along one curved shoreline; basalt ink; flat garden-bed colours (jungle, moss, ochre, heliconia, iresine purple, sea blue, silver-leaf) as each peer's organic bed; Jost geometric display over the platform UI face; hairline rules, no stray greys (neutrals tinted from jungle or stone); one 8px grid.

STORY: The visitor sees their peers as a tidy garden of beds, drops files on one, and watches the black-and-white wave pavement lay itself from them toward the peer until it's done. Incoming files arrive as a heliconia-marked strip asking to accept.

FIRST VIEWPORT: Left jungle rail (wordmark, Peers/Transfers/Settings, this device, server status) with a wave shoreline edge; right: Peers title and Add peer action top-right; any incoming offer strip pinned above; a grid of compact peer cards, two columns at the default 1040px window and three once each card keeps at least 19rem (cited: at three columns in 720px of content the peer name, device ID and policy line wrap or truncate, which breaks scanning), each an organic colour bed with initials, name, ID and policy line; active sends show a wave band along the card's foot.

FORM: Burle Marx Garden (tropical modernism), candidate 3 of 7 on my grounded list; seed key 42428b14. Raises kept from declined challengers: one 8px grid (Crouwel), wave length = bytes moved (Labanotation), hairlines over padding (Japanese high-density), one geometry for all transfer states (Botanical folio), no stray greys (Ikeda).

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

Signature move: the Copacabana wave pavement band (basalt waves on stone) as the filled part of every progress bar, length proportional to bytes; it drifts slowly while connecting or waiting. Used nowhere else except the rail's shoreline curve.
Motion grammar: one authored moment, the wave laying and drifting; drop targets swell slightly with exponential ease-out; everything visible by default.

## Unresolved

- App icon / logo does not exist yet (window shows the Wails default).
