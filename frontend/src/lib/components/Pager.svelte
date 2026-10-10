<script lang="ts">
  // The phone's pages, side by side: swipe sideways to move between Peers,
  // Transfers and Settings, as in any Android app with bottom tabs. Vertical
  // scrolling inside a page stays native; a sideways drag moves the strip
  // under the finger, and on release the page settles quickly on the app's
  // ease-out (the browser's own snap animation is too slow to feel direct).
  // The strip scrolls rather than transforms, so fixed layers inside a page
  // (dialogs, sheets) still cover the screen.
  import { onMount } from 'svelte'
  import PeersPage from '../pages/PeersPage.svelte'
  import TransfersPage from '../pages/TransfersPage.svelte'
  import SettingsPage from '../pages/SettingsPage.svelte'
  import NetworkPage from '../pages/NetworkPage.svelte'
  import { app } from '../state.svelte'
  import { pages, navIndex, nav, setNavPos } from '../nav.svelte'
  import { easeOutExpo, dur } from '../motion'

  const SLOP = 8 // px of movement before a gesture picks an axis
  const FLICK = 0.35 // px/ms: a release this fast turns the page even if short
  const SETTLE_MS = 260

  let strip: HTMLDivElement
  const width = () => strip.clientWidth || 1
  const at = () => strip.scrollLeft / width()

  function show(x: number) {
    strip.scrollLeft = x * width()
    setNavPos(x)
    const near = Math.round(x)
    if (nav.near !== near) nav.near = near
  }

  // Touch events can outpace the screen; move the strip once per frame.
  let pending = -1
  let moveFrame = 0
  function showSoon(x: number) {
    pending = x
    if (!moveFrame)
      moveFrame = requestAnimationFrame(() => {
        moveFrame = 0
        if (pending >= 0 && touch) show(pending)
      })
  }

  // ---- Settling: one short animation to a page ----
  let anim = 0
  function settleTo(i: number) {
    cancelAnimationFrame(anim)
    const from = at()
    const ms = dur(SETTLE_MS * Math.min(1, 0.5 + Math.abs(i - from) / 2)) // a nudge settles faster than a jump
    const t0 = performance.now()
    const step = (now: number) => {
      const p = ms ? Math.min(1, (now - t0) / ms) : 1
      show(from + (i - from) * easeOutExpo(p))
      if (p < 1) anim = requestAnimationFrame(step)
      else if (navIndex(app.page) !== i) app.page = pages[i]
    }
    anim = requestAnimationFrame(step)
  }

  // ---- The finger ----
  let touch: { id: number; x0: number; y0: number; base: number; axis: '' | 'x' | 'y'; trail: { x: number; t: number }[] } | null = null

  function onpointerdown(e: PointerEvent) {
    if (e.pointerType !== 'touch' || touch) return
    cancelAnimationFrame(anim) // catch a page mid-settle
    touch = { id: e.pointerId, x0: e.clientX, y0: e.clientY, base: strip.scrollLeft, axis: '', trail: [{ x: e.clientX, t: e.timeStamp }] }
  }

  function onpointermove(e: PointerEvent) {
    if (!touch || e.pointerId !== touch.id) return
    // A peer card being moved owns the finger.
    if (document.body.classList.contains('sorting')) return end(false)
    const dx = e.clientX - touch.x0
    const dy = e.clientY - touch.y0
    if (!touch.axis) {
      if (Math.hypot(dx, dy) < SLOP) return
      touch.axis = Math.abs(dx) > Math.abs(dy) ? 'x' : 'y'
    }
    if (touch.axis !== 'x') return
    const max = (pages.length - 1) * width()
    showSoon(Math.max(0, Math.min(max, touch.base - dx)) / width())
    touch.trail.push({ x: e.clientX, t: e.timeStamp })
    if (touch.trail.length > 6) touch.trail.shift()
  }

  let swiped = false
  function onclickcapture(e: MouseEvent) {
    if (swiped) {
      e.stopPropagation()
      e.preventDefault()
    }
  }

  function onpointerup(e: PointerEvent) {
    if (touch && e.pointerId === touch.id) end(true)
  }

  function end(release: boolean) {
    const t = touch
    touch = null
    cancelAnimationFrame(moveFrame)
    moveFrame = 0
    if (!t || t.axis !== 'x') return
    // The finger lifting after a swipe isn't a tap on whatever is under it.
    swiped = true
    setTimeout(() => (swiped = false), 0)
    const from = navIndex(app.page)
    let i = Math.round(at())
    if (release) {
      const a = t.trail[0]
      const b = t.trail[t.trail.length - 1]
      const v = b.t > a.t ? (b.x - a.x) / (b.t - a.t) : 0 // px/ms, + = finger moving right
      if (Math.abs(v) > FLICK) i = from + (v < 0 ? 1 : -1)
    }
    settleTo(Math.max(0, Math.min(pages.length - 1, i)))
  }

  // A tab, the back button or a file drop changed the page: glide there.
  $effect(() => {
    const i = navIndex(app.page)
    if (!touch && Math.abs(at() - i) > 0.001) settleTo(i)
  })

  onMount(() => {
    // Turning the phone changes the page width; stay on the same page.
    const ro = new ResizeObserver(() => {
      cancelAnimationFrame(anim)
      show(navIndex(app.page))
    })
    ro.observe(strip)
    return () => {
      ro.disconnect()
      cancelAnimationFrame(anim)
    }
  })
</script>

<div
  bind:this={strip}
  role="presentation"
  class="strip flex min-h-0 flex-1 overflow-x-auto overflow-y-hidden"
  {onpointerdown}
  {onpointermove}
  {onpointerup}
  onpointercancel={() => end(false)}
  {onclickcapture}
>
  {#each pages as page, i (page)}
    <section class="pane min-w-0 shrink-0 basis-full overflow-y-auto" aria-hidden={nav.near !== i}>
      {#if page === 'peers'}
        <PeersPage />
      {:else if page === 'transfers'}
        <TransfersPage />
      {:else if app.page === 'network'}
        <NetworkPage />
      {:else}
        <SettingsPage />
      {/if}
    </section>
  {/each}
</div>

<style>
  /* The browser keeps vertical scrolling; sideways drags come to us. Each
     page is its own scroll container, and touch-action stops at the nearest
     one, so the pages carry it, not just the strip. */
  .strip,
  .pane {
    touch-action: pan-y;
  }
  /* A real (hidden-scrollbar) scroller, not overflow: hidden, so moving it
     is a composited scroll on the GPU rather than a repaint of the pages. */
  .strip {
    scrollbar-width: none;
    overscroll-behavior-x: none;
  }
  .pane {
    overscroll-behavior-y: contain;
  }
</style>
