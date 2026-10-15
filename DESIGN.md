---
name: TropicalPeer
description: Peer-to-peer file sending for everyday people, dressed as a Burle Marx garden.
colors:
  ground: "#eef0ea"
  surface: "#fbfcf9"
  surface-2: "#e3e8df"
  line: "#d3dacf"
  ink: "#17211c"
  muted: "#536259"
  rail: "#0f3b2d"
  rail-raised: "#185040"
  rail-ink: "#e8f0ea"
  rail-muted: "#a3bdb0"
  rail-ok: "#6fdba6"
  rail-info: "#8fb6f7"
  rail-alert: "#f2826a"
  rail-alert-ink: "#2a0f09"
  primary: "#146c4c"
  primary-hover: "#0f5a3e"
  primary-ink: "#ffffff"
  primary-soft: "#d6eadf"
  sea: "#2357c4"
  sea-soft: "#dbe5f7"
  heliconia: "#c2412a"
  heliconia-soft: "#f6dfd7"
  offer: "#a8331d"
  offer-ink: "#ffffff"
  offer-muted: "#ffd9ce"
  ok: "#16774b"
  warn: "#8a5e00"
  bad: "#bf3229"
  wave-ink: "#17211c"
  wave-ground: "#fbfcf9"
  bed-jungle: "#1f5e44"
  bed-moss: "#55732f"
  bed-ochre: "#9a6c0d"
  bed-heliconia: "#b23d27"
  bed-iresine: "#7a2e52"
  bed-sea: "#2357c4"
  bed-silver-leaf: "#5f776c"
typography:
  display:
    fontFamily: "Jost Variable, Segoe UI Variable Display, Segoe UI, sans-serif"
    fontSize: "28px"
    fontWeight: 500
    lineHeight: 1.25
    letterSpacing: "-0.01em"
  wordmark:
    fontFamily: "Jost Variable, Segoe UI Variable Display, Segoe UI, sans-serif"
    fontSize: "22px"
    fontWeight: 500
    lineHeight: 1
    letterSpacing: "-0.02em"
  headline:
    fontFamily: "Jost Variable, Segoe UI Variable Display, Segoe UI, sans-serif"
    fontSize: "20px"
    fontWeight: 500
    lineHeight: 1.4
    letterSpacing: "-0.01em"
  title:
    fontFamily: "Jost Variable, Segoe UI Variable Display, Segoe UI, sans-serif"
    fontSize: "17px"
    fontWeight: 500
    lineHeight: 1.25
    letterSpacing: "-0.01em"
  section:
    fontFamily: "Jost Variable, Segoe UI Variable Display, Segoe UI, sans-serif"
    fontSize: "16px"
    fontWeight: 500
    lineHeight: 1.5
    letterSpacing: "-0.01em"
  body:
    fontFamily: "Segoe UI Variable Text, Segoe UI, system-ui, -apple-system, sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.45
    fontFeature: "tnum"
  label:
    fontFamily: "Segoe UI Variable Text, Segoe UI, system-ui, -apple-system, sans-serif"
    fontSize: "12px"
    fontWeight: 500
    lineHeight: 1.33
  mono-id:
    fontFamily: "Cascadia Mono, Consolas, ui-monospace, monospace"
    fontSize: "11px"
    fontWeight: 400
    lineHeight: 1.45
  mono-code:
    fontFamily: "Cascadia Mono, Consolas, ui-monospace, monospace"
    fontSize: "24px"
    fontWeight: 600
    lineHeight: 1.33
    letterSpacing: "0.025em"
rounded:
  sm: "4px"
  md: "6px"
  lg: "8px"
  full: "9999px"
spacing:
  xs: "4px"
  sm: "8px"
  md: "12px"
  lg: "16px"
  xl: "24px"
  2xl: "32px"
  page-x: "40px"
components:
  button-primary:
    backgroundColor: "{colors.primary}"
    textColor: "{colors.primary-ink}"
    typography: "{typography.body}"
    rounded: "{rounded.md}"
    padding: "8px 16px"
  button-primary-hover:
    backgroundColor: "{colors.primary-hover}"
  button-secondary:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.md}"
    padding: "8px 16px"
  button-secondary-hover:
    backgroundColor: "{colors.surface-2}"
  button-quiet:
    textColor: "{colors.muted}"
    rounded: "{rounded.md}"
    padding: "6px 10px"
  button-quiet-hover:
    backgroundColor: "{colors.surface-2}"
    textColor: "{colors.ink}"
  button-danger:
    backgroundColor: "{colors.bad}"
    textColor: "#ffffff"
    rounded: "{rounded.md}"
    padding: "8px 16px"
  field:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.md}"
    padding: "8px 12px"
  nav-item:
    textColor: "{colors.rail-muted}"
    rounded: "{rounded.md}"
    padding: "8px 12px"
  nav-item-active:
    backgroundColor: "{colors.rail-raised}"
    textColor: "{colors.rail-ink}"
  peer-card:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.lg}"
    padding: "16px"
  offer-strip:
    backgroundColor: "{colors.offer}"
    textColor: "{colors.offer-ink}"
    padding: "16px 40px"
  offer-accept:
    backgroundColor: "{colors.offer-ink}"
    textColor: "{colors.offer}"
    rounded: "{rounded.md}"
    padding: "8px 16px"
  wave-band:
    backgroundColor: "{colors.wave-ground}"
    textColor: "{colors.wave-ink}"
    rounded: "{rounded.full}"
    height: "12px"
  dialog:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.lg}"
    padding: "20px 24px 24px"
  toast:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.lg}"
    padding: "14px"
    width: "352px"
---

# Design System: TropicalPeer

## Overview

**Creative North Star: "The Burle Marx Garden"**

Grown-up tropical modernism after Roberto Burle Marx. A deep jungle-green rail meets a cool limestone-pavement ground along one long curved shoreline; every peer is a flat, organic garden bed of colour; and every transfer is laid as Copacabana wave pavement, basalt waves on white stone, whose length is the share of bytes moved. The app refuses the category default of white cards, avatar bubbles on a radar and a single blue button.

It is a desktop tool (Wails / WebView2, Windows first) for short, mid-task visits, so the density is calm and compact: hairline rules carry structure instead of padding, the ground is quiet, and colour arrives as flat fields that mean something (a peer's identity, an incoming offer, a state). Exactly one surface is loud: the full heliconia incoming-offer strip. Two themes ship from one token set: limestone day, and the "night garden" (dark, following Windows unless overridden in Settings); the rail stays the same jungle green in both.

Motion has two authored moments: the wave laying itself and drifting while waiting, and the beds being rearranged (a lifted peer card rides the float shadow while the others glide aside). Everything else is quiet state change on one exponential ease-out (`--ease-out-expo`, `motion.ts`): selection fills slide rather than jump, things that appear settle in from slightly smaller or lower, things that leave fade faster than they came. Durations: 100–200ms feedback, 220–320ms state and layout. Reduced motion removes the movement (durations go to 0) and keeps the state change.

**Key Characteristics:**
- Jungle-green rail with a single curved shoreline edge; limestone ground; basalt ink.
- Neutrals are all tinted from jungle or stone; no stray greys.
- Organic garden-bed peer marks: 4 hand-drawn outlines x 7 flat bed colours, picked deterministically from the device ID.
- Copacabana wave pavement is the filled part of every progress bar, and appears nowhere else but the rail's shoreline.
- Jost geometric display over the Segoe UI Variable platform face; Cascadia Mono for IDs and pairing codes.
- Hairlines (1px line colour) over padding and over shadow.

## Colors

Limestone and jungle neutrals, one jungle-green action colour, and a garden of flat bed colours that belong to peers, not to chrome.

### Primary
- **Jungle Leaf** (primary): every primary action (Add peer, Save changes, Pair), the focus ring, the caret, the selected appearance option, the "Auto-accepts" policy line and outgoing-transfer arrows. Hover deepens to **Deep Jungle** (primary-hover). **Leaf Wash** (primary-soft) is text selection.

### Secondary
- **Copacabana Sea** (sea): incoming direction (the received-transfer arrow) and info toasts; **Sea Wash** (sea-soft) is its tint.

### Tertiary
- **Heliconia** (heliconia): the warm accent; **Heliconia Wash** (heliconia-soft) backs the fatal-error banner and the destructive menu item's hover.
- **Offer Heliconia** (offer) with **Offer White** (offer-ink) and **Offer Blush** (offer-muted): the incoming-offer field only. These three hold the same values in both themes: the offer is the one loud surface in the app.

### Neutral
- **Limestone Ground** (ground): the app background and sticky footers.
- **Pale Stone** (surface): cards, lists, dialogs, menus, toasts, fields.
- **Worn Stone** (surface-2): hover fills and the add-peer tab tray.
- **Moss Hairline** (line): every border, divider and the scrollbar thumb.
- **Basalt Ink** (ink) and **Lichen Muted** (muted): text and secondary text.
- **Jungle Rail** (rail), **Raised Rail** (rail-raised), **Rail Mist** (rail-ink), **Rail Lichen** (rail-muted): the navigation rail and its shoreline. Rail state dots use **rail-ok**, **rail-info** (connecting: a light sea, matching the sea "still finding out" tone on the Network page), **rail-alert** (with **rail-alert-ink** on the alert badge); they are constant across themes because the rail is.
- **State**: ok, warn, bad, for status text (Done, Failed, Declined) and the danger button. Light values are set for at least 4.5:1 on ground and surface (ok #16774b, warn #8a5e00), since status words are 14px. Sea doubles as the "still finding out" state on the Network page.
- **Wave Basalt** (wave-ink) on **Wave Stone** (wave-ground): the pavement. In the night garden the pavement inverts to pale waves (#e2ebe5) on a dark stone track (#1f3029).

### Garden Beds
Seven flat bed colours (bed-jungle, bed-moss, bed-ochre, bed-heliconia, bed-iresine, bed-sea, bed-silver-leaf; in the stylesheet as `--bed-1` … `--bed-7`, identical in both themes), each dark enough to carry white Jost initials at 3:1 or better in both themes. They are identity, not semantics: never use a bed colour for state or chrome.

### Night Garden
The dark theme redefines the same tokens: ground #111c18, surface #172520, surface-2 #1f3029, line #2b4137, ink #e2ebe5, muted #95a99f, rail-raised #1a5442, rail-muted #9fbcae, primary #3cb483 (hover #52c595, ink #04221a, soft #143a2c), sea #7aa4f2 / #182a4b, heliconia #f2826a / #4a2218, ok #4cc38a, warn #e2b341, bad #f06a60. Components use only token names, so nothing outside the global stylesheet knows which theme is active.

### Named Rules
**The Tinted Neutral Rule.** Every neutral is tinted from jungle or stone. A pure grey or pure black in chrome is a defect.

**The One Loud Field Rule.** Full-saturation colour as a surface belongs to the incoming-offer strip alone. Everywhere else colour is a bed, a line of text, or a small mark.

**The Beds Are Identity Rule.** A peer's bed colour and outline come from its device ID and look the same on every screen (rail, card, offer, transfer row).

## Typography

**Display Font:** Jost Variable (with Segoe UI Variable Display, Segoe UI)
**Body Font:** Segoe UI Variable Text (with Segoe UI, system-ui)
**Label/Mono Font:** Cascadia Mono (with Consolas, ui-monospace)

**Character:** Jost's geometric modernism carries names and titles, the platform face carries everything you read and operate, so the app feels native on Windows while the garden speaks in its own voice.

### Hierarchy
- **Display** (500, 28px, 1.25): page titles (Peers, Transfers, Settings).
- **Wordmark** (500, 22px, 1, -0.02em): the rail wordmark, with "Peer" in rail-muted.
- **Headline** (500, 20px): empty-state headings ("Pair your first device").
- **Title** (500, 17px, 1.25): peer names on cards and the offer strip's sentence.
- **Section** (500, 16px): section headings (In progress, Finished, Settings rows).
- **Body** (400, 14px, 1.45, tabular numerals): all running text, statuses, buttons. Prose blocks cap at 60ch; settings hints at 40ch.
- **Label** (500 to 600, 12px): rail counts and the server line.
- **Mono ID** (400, 11px): device IDs under names. **Mono Code** (600, 24px, wide tracking): pairing codes.

### Named Rules
**The Display For Names Rule.** h1, h2 and peer names take Jost with -0.01em tracking and balanced wrapping; controls and body text never do.

**The Tabular Rule.** Body text is tabular-numeral by default so sizes, speeds and countdowns do not jitter.

## Layout

A fixed 240px rail on the left; the content column scrolls on its own. Pages are centred at a maximum of 1040px with 40px side padding, 36px top, 48px bottom, and 32px between page blocks. The page header puts title and one-line description on the left and the page's one action on the right.

The peer grid auto-fills columns of at least 19rem with 12px gaps: two columns at the default 1040px window, three once each card can keep 19rem. Three columns at the default size were rejected because names, IDs and policy lines wrapped or truncated. Transfers are a single bordered list with hairline dividers, rows 16px by 20px. Settings is a two-column row grid (an 18rem label column, a 40px gutter, the control) with 24px vertical padding and a hairline above each row; the save bar is sticky at the foot on the ground colour. Rows stack (label above control, 12px gap) when the window is narrower than 1000px. Each Settings label column is a title, one short muted line saying what the setting is for (40ch), and, where there is more to know, a 15px Lucide Info toggle after the title that opens the longer explanation inline (180ms slide, ink at 80%) rather than a hover tooltip, which touch can't reach. Status inside a row is a word, not a sentence ("Active", "Not available on this network"); the reasons live on the Network page. Settings ends with a Restart TropicalPeer row (secondary button with a Lucide RotateCw icon, spinner and "Restarting…" while it runs; a confirm dialog only when transfers are running): networking restarts in place, never the app. When the saved listening port differs from the one in use, the Listening port row adds a sea line ("Switches to UDP 7779 after a restart.") with an underlined primary "Restart now", and the save toast offers the same action. On Android the Download folder row shows the folder as a mono path with a secondary Change… (Android's folder picker) and, once changed, a quiet Use default. Settings carries one network control, the Port forwarding row: a native checkbox whose description is the live status in plain words, and a primary text link to the Network page.

The incoming-offer strip pins above the page at full content width, padded to the same 40px gutter as the page. Toasts sit bottom-right, 352px wide, 24px from the window edges.

Spacing runs on a 4px base (4, 8, 12, 16, 24, 32, 40). The direction contract asked for a strict 8px grid; the build uses 4px half-steps (12, 14, 36) where density needed them, and that is what is recorded.

### Named Rules
**The Hairline Rule.** Structure comes from 1px line-colour rules and dividers, not from extra padding or nested cards.

## Elevation & Depth

Flat by default. Surfaces sit on the ground separated by hairlines; tonal steps (ground, surface, surface-2) do the layering. Shadow appears only as a response or on floating layers: a peer card gains the soft card shadow on hover and the float shadow when it is a live drop target; menus, toasts and dialogs float. Shadows are tinted with basalt ink in day and deepen to black in the night garden.

### Shadow Vocabulary
- **Card** (`box-shadow: 0 1px 2px rgb(23 33 28 / 0.06), 0 6px 20px -8px rgb(23 33 28 / 0.18)`): hover on peer cards; the selected add-peer tab.
- **Float** (`box-shadow: 0 2px 4px rgb(23 33 28 / 0.08), 0 18px 40px -12px rgb(23 33 28 / 0.35)`): dialogs, menus, toasts, the drop-target card.

### Named Rules
**The Flat-At-Rest Rule.** Nothing on the page carries a shadow at rest. Shadow is state (hover, drop) or a floating layer.

## Shapes

Gentle, consistent corners: small 4px for segmented options, 6px for buttons, fields, nav items and icon buttons, 8px for containers (cards, lists, dialogs, menus, toasts), full pills for the wave band and count badges. Against that rectilinear frame, the garden's organic geometry appears in exactly two places: the hand-drawn bed outlines that mark peers, and the curves of the wave (the pavement tile and the rail's single long shoreline). The shoreline is one slow curve top to bottom, a coast, not a frill.

### Named Rules
**The Two Curves Rule.** Organic curves are reserved for garden beds and the wave/shoreline. Containers stay gently rectangular.

## Components

### Buttons
Calm and solid; one primary per view.
- **Shape:** gently rounded (6px).
- **Primary:** Jungle Leaf fill, white text (dark ink in the night garden), 8px by 16px, 500 weight, optional 18px leading icon with an 8px gap. Hover deepens to primary-hover.
- **Secondary:** Pale Stone fill with a hairline border; hover moves to Worn Stone and a slightly darker border.
- **Quiet:** text-only muted action at 14px (6px by 10px), hover fills Worn Stone and darkens to ink. Used for row actions (Cancel, Retry, Open folder).
- **Danger:** bad-colour fill, white text, hover fades to 90% opacity. Only inside confirmation dialogs.
- **Disabled:** 45% opacity, not-allowed cursor, no hover change.
- **Focus:** a 2px primary outline at 2px offset, globally.

### Cards / Containers
- **Corner Style:** 8px.
- **Background:** Pale Stone on Limestone Ground.
- **Shadow Strategy:** flat at rest; see Elevation.
- **Border:** hairline line colour; hover darkens it toward muted; while files are dragged every card shows a dashed primary border, and the one under the cursor turns solid primary, scales to 1.03 and floats (300ms exponential ease-out).
- **Internal Padding:** 16px.

### Peer Card
The whole card is the send target: click to choose files, or drop on it. A 48px garden bed, the name in Jost Title, the device ID in Mono ID, and one policy line with a 14px icon (Auto-accepts in primary, Asks first in muted, Blocked in bad with the card content at 60% opacity). An overflow menu appears top-right on hover or focus (it scales in from its top-right corner, 160ms). Cards are rearranged by hand: drag with a mouse (a press only becomes a drag after 5px, so a click still sends), hold and drag on touch (380ms still hold, a light vibration, then the card follows the finger), or Alt+arrow keys. The lifted card scales to 1.03 with the float shadow and a primary edge; the others glide to their new places (FLIP, 300ms); the card settles into its slot over 260ms on release. The order is saved per device in peers.json (`order`); newly paired peers join at the end once the user has arranged the list, and newly paired cards scale in from 0.96 while removed ones fade. While sending, the policy line becomes "Sending · n%" or "Waiting for an answer…" and a 6px wave band runs along the card's foot.

### Inputs / Fields
- **Style:** Pale Stone fill, hairline border, 6px radius, 8px by 12px, muted placeholder; text is selectable.
- **Focus:** border turns primary with a 2px primary ring at 25%.
- **Error:** a bad-coloured 14px message beneath; the field itself does not change.
- **Segmented choice:** a hairline-bordered tray with 4px-radius options; the selected option fills primary, and the fill slides (300ms, measuring each option, since widths change by language) to the new choice. A theme change then crossfades the whole window (260ms view transition). The add-peer dialog's two tabs use the same sliding raised tab.

### Navigation
The jungle rail: wordmark, then Peers / Transfers / Links / Settings as 6px-radius items with 18px line icons (1.75 stroke). Rest is Rail Lichen; hover is a 60% raised fill; current is Raised Rail with Rail Mist text: one raised fill that slides between items (320ms) and steps aside while the Network page is open. Desktop pages settle in from 6px below over 220ms when switched. Count badges are pills: active transfers in Rail Mist on rail, pending offers in rail-alert, people downloading from links in Rail Mist on Links. The rail foot carries this device's bed, name and ID with a copy button, and the server status line (dot: ok, rail-info with a ping while connecting, alert; label; address; a chevron). That status line is a button and the only way into the Network page: network detail stays out of the main navigation (PRODUCT.md principle 5). It takes the raised fill on hover and while the Network page is open.

### Offer Strip (signature)
Incoming files arrive as a full-width Offer Heliconia field pinned above the page: the sender's 44px bed, a Title-size sentence ("Moon Laptop wants to send you …"), size in Offer White and file names and destination folder in Offer Blush, a live "Declines in m:ss" countdown, then an outlined Decline and a solid white Accept whose text is offer-coloured. Multiple offers stack with 1px separators and slide in over 220ms.

### Wave Band (signature)
Transfer progress laid as Copacabana pavement: a pill track of wave stone with an inset hairline, filled from the left by a repeating 28px basalt wave tile whose width is exactly the share of bytes moved (300ms ease-out on width). A 2px square leading edge marks where the pavement has reached, like the last row of laid stones. While connecting or waiting, the whole track drifts at 35% opacity (1.6s linear loop); reduced motion stops the drift. 12px tall in transfer rows and pairing, 6px on peer cards. When a transfer runs through the server's relay, its status line carries a quiet muted "Relayed" mark (Lucide Route icon, 13px) whose tooltip explains it is still end-to-end encrypted, just slower; while a path is being found the status reads "Finding a path to …" over the drifting band. A transfer that moved from the relay to a direct path mid-way carries the parallel mark "Now direct" ("Went direct" once finished): muted text with an ok-tinted Lucide Zap icon, so it stays distinct from an ok-toned status line.

### Phone (Android)
Below 640px the same garden is turned on its side. The jungle rail becomes a bottom tab bar (64px plus the system navigation inset) with Peers, Transfers, Links and Settings as 22px icons over 12px labels; the current tab sits in a Raised Rail pill that slides between tabs, and the rail's shoreline curve runs along the bar's top edge. Badges ride the icons: active transfers in Rail Mist, pending offers in rail-alert, and on Settings a rail-info or rail-alert dot only while the server is connecting or unreachable (the desktop's always-on server line has no room here). On Android the four pages sit side by side and swipe sideways (the strip follows the finger 1:1; on release it settles in 260ms on the ease-out, turning a page when dragged past half or flicked faster than 0.35px/ms; one page per swipe, each page keeping its scroll; vertical scrolling stays native); the tab pill tracks the finger during the swipe, and the nearest tab lights up before the page settles. Network is reached from Settings and leads back with a quiet "‹ Settings" link; the Settings tab stays current while it is open. The device ID moves under the device name in Settings, with a Copy ID quiet button. Files shared from another app open a "Send …" sheet (title: the file name or "N files", the total size under it, then each paired device as a 60px row with its 40px bed, Jost name and mono ID); with no peers it offers Add peer and keeps the files waiting. Below the devices, after a hairline, a "Share by link" row (a 22px primary Lucide Link2, the label and one muted line) is the side path for someone without the app; it opens the New link sheet with the files. On Android, Settings carries "Stay ready to receive" between Appearance and Download folder: a checkbox that applies at once, its state in a word (On in ok; "On, but notifications are off" in warn, with an Allow notifications link). Notifications use a flat white garden-bed silhouette as the status icon, tinted Jungle Leaf. A peer card's ⋯ opens its actions as a bottom sheet titled with the peer's name: Send files, Peer settings, a hairline, then Remove peer in bad, each a 52px row with a 20px icon. On desktop the same actions stay a dropdown, which overflows the card rather than being clipped by it.

The status bar gets its own ground-coloured strip, so content never scrolls beneath the clock; insets arrive as `--sat`/`--sab`/`--sal`/`--sar` (Capacitor's `--safe-area-inset-*`, falling back to `env()`). Pages use 16px side padding, 20px top and 24px between blocks. Dialogs become bottom sheets: full width, square bottom corners, a 36px grip, rising 48px over 240ms with exponential ease-out, padded above the navigation inset. Toasts span the width 12px from the edges and drop in from the top (12px under the status bar, newest first), where Android shows its own notifications; while an offer strip is showing they sit 12px below it (`--offer-h`), so they never cover Accept and Decline. The offer strip stacks: bed and sentence, then size and countdown, then Decline and Accept as two equal 44px buttons; on Android it drops the folder (files always land in Downloads/TropicalPeer). The offer strip is sticky at the top of the content on every layout, because an offer expires and must be seen wherever the page is scrolled. Transfer rows let the status wrap instead of truncating and put actions on their own line, aligned with the text ("Open", and "Open file location" with a Lucide FolderSearch icon on Android, opening the chosen folder in Files). The status bar's icons follow the in-app theme; the navigation bar's stay light, since it sits on the jungle.

Touch (`pointer: coarse`, any width): buttons and fields are at least 44px (quiet buttons 40px), fields use 16px text, controls that only revealed on hover (the peer card's menu) are always shown, and presses answer with the hover fill. On Android, copy says "tap" rather than "click" or "drag from Explorer", the folder pickers give way to a fixed "Downloads/TropicalPeer", and "Match Windows" reads "Match phone".

### Network Page
A diagnostic page in the Settings row grammar (same 18rem label column, hairlines, stacking below 1000px). It opens with a verdict: one plain-language sentence answering "can other devices reach this one?", set in the 20px headline step with a 10px state dot (the only dot on the page), and one muted explanatory line. Below it, five rows: Rendezvous server, Public address, Router, Port forwarding, Relay. Each value is a medium-weight word tinted by meaning, with a muted explanation under it; there are no per-row dots. Tones: ok (working: Connected, Open, Restricted, Active, Available), sea (still finding out: Connecting…, Checking…, Trying…), warn (limits what works: Strict, forwarding Not available when the router is strict, relay turned off), bad (blocks transfers: Unreachable, relay off behind an unforwarded strict router), muted (off by choice or unknown, nothing to act on). The verdict headline takes its tone's colour too, except muted, which stays ink. Text links on the page are primary with a 40% primary underline at rest, so an action never reads as an ok-toned status. Addresses are always Cascadia Mono at 14px. Anything that can only be known through the server turns muted with "Last known" while the server is unreachable. Technical terms (symmetric NAT, UPnP) live in tooltips, never in the visible copy. The subtitle carries freshness ("Checked just now"); "Check again" is the single secondary action, showing a spinner while it runs.

### Links Page
Share by link, for people without the app (they download in a browser). The page follows the page grammar: title, one-line description, New link as the one primary action (repeated in the empty state, which is a dashed drop zone).

- **New link** never creates a link straight away: a dialog (a sheet on phones) first asks "Who can download?" with two option rows, One person (Lucide User) and Anyone with the link (Users), each a title and one muted line; the chosen row takes a primary border on Leaf Wash. A muted line names the device people will see the files come from. The primary action is Choose files… (New link) or Create link (files already dropped or shared). The last choice is remembered on the device.
- **Link card:** a bordered Pale Stone card, 16px padding. On a computer the QR code (132px, dark modules on pale stone in both themes, because scanners need it) sits at the left and never stretches. To its right, in the order the sender asks: the title in Jost Title (file name or "N files") with the size and file names in a muted line under it and Stop link as a quiet action at the right; then a status line: the scope as an ink word with a 14px primary User/Users icon, a muted dot, and the state in muted text ("Waiting for someone to open it", "Indigo Sun has it open…", "2 downloaded · 1 downloading"). Last comes the handover: the mono URL and Copy on a computer; on Android (and below 640px) a primary Share and a secondary QR code button that opens the code full width under the card.
- **Used:** a one-person link whose person finished is spent: the QR fades to 30%, the status says "Used by …", and the handover becomes a secondary "Let anyone use it" (Lucide Unlock) instead of a dead Copy. An ended link (server lost for 15 minutes) fades the same way with the reason in bad.
- **Visitors** are rows under a hairline: a 32px bed from the visitor's generated name (beds here mark a browser session, not a device), the name, a 12px wave band while sending, and a status that wraps. Only a finished download is toned (ok); left, lost and turned-away stay muted, since a closed tab is not a fault. Actions are quiet: Stop while sending, Turn away while someone is only on the page. Past the people still there, the last three finished rows show, with "Show n earlier" for the rest.
- **Stop link** asks first only while someone is downloading (danger button in a dialog).

### Receiver Page (web/)
The page a stranger opens from a link is the garden in miniature, served by the rendezvous server: a jungle band across the top with the two-tone Jost wordmark and the shoreline curve along its foot, then a 34rem column on the ground. The sender's 52px bed (picked from their name) sits beside the Jost headline "… is sending you N files", then "You appear to them as …", the file list as a hairline-divided Pale Stone list, a full-width primary Download, and one muted line with a primary shield icon saying the files come straight from the sender's device, encrypted, and are not stored on a server. While downloading: "File 1 of 2 · name", the 12px wave pavement (clipped to the share of bytes, waves running; still under reduced motion), totals and speed, the keep-open note, and Cancel as a quiet text button. Sizes use the app's rule (1024-based). Screen readers hear each new state's heading once through a hidden live region, not every progress tick.

### Transfer Row
A 40px bed, a direction arrow (primary up-right for sent, sea down-left for received), the file title, a wave band while active, then a status line toned by outcome (ok for done, bad for failed or declined, muted otherwise) and quiet actions on the right.

### Dialogs and Toasts
Dialogs (bottom sheets on phones; see Phone) are 8px-radius Pale Stone panels (max 448px wide by default) over a jungle-tinted, 2px-blurred scrim (`--scrim`, rgb(9 32 24 / 0.42); never plain black), scaling in from 0.96 over 160ms. Toasts are 8px-radius Pale Stone cards with the float shadow, an 18px tone icon (ok, bad, sea), and an optional primary text action; they rise 12px over 220ms with exponential ease-out.

## Do's and Don'ts

### Do:
- **Do** use the Copacabana wave only as the filled part of a progress bar (length = bytes moved) and as the rail's shoreline.
- **Do** mark every peer with its garden bed, everywhere the peer appears, from the device ID hash.
- **Do** keep components on token names (ground, surface, line, ink, primary...) so the night garden switches in the global stylesheet alone.
- **Do** separate with 1px line-colour hairlines and dividers before reaching for padding or shadow.
- **Do** set page titles, section headings and peer names in Jost 500 with -0.01em tracking; set everything operable in Segoe UI Variable at 14px.
- **Do** show device IDs and pairing codes in Cascadia Mono, selectable.
- **Do** give the incoming offer the full heliconia field, with Accept as the one solid white button.

### Don't:
- **Don't** use pure greys or pure black in chrome; tint every neutral from jungle or stone.
- **Don't** fill any surface other than the offer strip with full-saturation colour.
- **Don't** use bed colours for status, buttons or chrome.
- **Don't** use the wave as decoration, a divider or a loading spinner unrelated to a transfer.
- **Don't** add shadows to surfaces at rest.
- **Don't** fall back to circular avatar bubbles, a radar of devices, or white cards with a single blue button.
