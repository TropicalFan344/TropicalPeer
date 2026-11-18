<script lang="ts" module>
  // Open dialogs, newest last. Only the top one answers Escape and keeps
  // keyboard focus, so a confirm opened from a dialog closes on its own.
  const open: symbol[] = []
</script>

<script lang="ts">
  import { onMount, tick, type Snippet } from 'svelte'
  import { fade, fly, scale } from 'svelte/transition'
  import { X } from '@lucide/svelte'
  import { closeOnBack } from '../back'
  import { dur, easeOutExpo } from '../motion'
  import { t } from '../i18n.svelte'

  let {
    title,
    onclose,
    children,
    width = 'max-w-md',
  }: { title: string; onclose: () => void; children: Snippet; width?: string } = $props()

  const me = Symbol()
  let panel: HTMLElement | undefined = $state()
  let scrim: HTMLElement | undefined = $state()
  let content: HTMLElement | undefined = $state()
  // Where focus goes back to when the dialog closes: the button that opened it.
  const opener = document.activeElement as HTMLElement | null

  onMount(() => {
    open.push(me)
    // A field inside may have taken focus already; otherwise the panel takes
    // it, so a screen reader announces the dialog and Tab starts inside it.
    tick().then(() => {
      if (panel && !panel.contains(document.activeElement)) panel.focus()
    })
    return () => {
      open.splice(open.indexOf(me), 1)
      if (opener?.isConnected) opener.focus()
    }
  })

  const focusable = () =>
    [...(panel?.querySelectorAll<HTMLElement>('button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])') ?? [])].filter(
      (el) => !(el as HTMLButtonElement).disabled && el.offsetParent !== null,
    )

  function onkeydown(e: KeyboardEvent) {
    if (open.at(-1) !== me) return
    if (e.key === 'Escape') {
      e.preventDefault()
      onclose()
    } else if (e.key === 'Tab' && panel) {
      // Tab stays inside the dialog: past the last control it wraps to the first.
      const els = focusable()
      if (!els.length) return e.preventDefault()
      const first = els[0]!
      const last = els[els.length - 1]!
      const at = document.activeElement
      const inside = panel.contains(at)
      if (e.shiftKey && (!inside || at === first || at === panel)) {
        e.preventDefault()
        last.focus()
      } else if (!e.shiftKey && (!inside || at === last)) {
        e.preventDefault()
        first.focus()
      }
    }
  }

  // Android's back button closes the sheet before anything else.
  $effect(() => closeOnBack(() => onclose()))

  // On a phone the dialog is a sheet rising from the bottom edge, in reach of
  // the thumb; on desktop it scales in at the centre.
  const phone = typeof matchMedia !== 'undefined' && matchMedia('(max-width: 639.98px)').matches
  const enter = (node: Element) =>
    phone ? fly(node, { y: 48, duration: dur(240), easing: easeOutExpo }) : scale(node, { start: 0.96, duration: dur(160) })

  // A finger pulls the sheet down, and letting go far enough or with a flick
  // closes it. Only with its content scrolled to the top: until then the same
  // swipe scrolls, and it takes a new swipe to close.
  const scrolledDown = (target: Element) => {
    if ((content?.scrollTop ?? 0) > 0) return true
    for (let el: Element | null = target; el && el !== panel; el = el.parentElement) if (el.scrollTop > 0) return true
    return false
  }

  $effect(() => {
    const sheet = panel
    if (!sheet) return
    const settleMs = () => dur(220)
    let drag: { id: number; x: number; y: number; from: number; moving: boolean; at: number; last: number; speed: number } | null = null
    let closing = false
    let timer: ReturnType<typeof setTimeout>

    const follow = (offset: number) => {
      sheet.style.transform = `translateY(${offset}px)`
      if (scrim) scrim.style.opacity = String(Math.max(0, 1 - offset / sheet.offsetHeight))
    }
    const animate = (ms: number) => {
      sheet.style.transition = `transform ${ms}ms var(--ease-out-expo)`
      if (scrim) scrim.style.transition = `opacity ${ms}ms linear`
    }
    const clear = () => {
      sheet.style.transform = sheet.style.transition = ''
      if (scrim) scrim.style.opacity = scrim.style.transition = ''
    }
    const settle = () => {
      animate(settleMs())
      follow(0)
      timer = setTimeout(clear, settleMs())
    }
    const dismiss = () => {
      closing = true
      animate(settleMs())
      follow(sheet.offsetHeight)
      timer = setTimeout(() => {
        onclose()
        // Still here: whoever opened it decided to keep it open.
        timer = setTimeout(() => {
          closing = false
          clear()
        }, 400)
      }, settleMs())
    }

    const start = (e: TouchEvent) => {
      if (drag?.moving) settle()
      drag = null
      const target = e.target as Element
      if (e.touches.length !== 1 || closing || open.at(-1) !== me || !matchMedia('(max-width: 639.98px)').matches) return
      // A drag in a field moves the caret; one over selected text adjusts it.
      if (target.closest('input, textarea, select, [contenteditable="true"]') || !(getSelection()?.isCollapsed ?? true)) return
      if (scrolledDown(target)) return
      const t = e.touches[0]!
      clearTimeout(timer)
      drag = { id: t.identifier, x: t.clientX, y: t.clientY, from: 0, moving: false, at: t.clientY, last: e.timeStamp, speed: 0 }
    }
    const move = (e: TouchEvent) => {
      const t = drag && [...e.touches].find((x) => x.identifier === drag!.id)
      if (!drag || !t) return
      const dy = t.clientY - drag.y
      if (!drag.moving) {
        // The first move decides: anything but a pull down stays a normal touch.
        if (dy <= 0 || Math.abs(t.clientX - drag.x) > dy || scrolledDown(e.target as Element)) {
          drag = null
          return
        }
        drag.moving = true
        drag.from = dy
        sheet.style.transition = 'none'
        if (scrim) scrim.style.transition = 'none'
      }
      if (e.cancelable) e.preventDefault()
      const dt = e.timeStamp - drag.last
      if (dt > 0) drag.speed = (t.clientY - drag.at) / dt
      drag.at = t.clientY
      drag.last = e.timeStamp
      follow(Math.max(0, dy - drag.from))
    }
    const end = (e: TouchEvent) => {
      const d = drag
      if (!d || [...e.touches].some((x) => x.identifier === d.id)) return
      drag = null
      if (!d.moving) return
      const offset = Math.max(0, d.at - d.y - d.from)
      const flick = e.timeStamp - d.last < 100 && d.speed > 0.5 && offset > 20
      if (e.type === 'touchend' && (flick || offset > Math.min(140, sheet.offsetHeight * 0.3))) dismiss()
      else settle()
    }

    sheet.addEventListener('touchstart', start, { passive: true })
    sheet.addEventListener('touchmove', move, { passive: false }) // must be able to stop the scroll
    sheet.addEventListener('touchend', end)
    sheet.addEventListener('touchcancel', end)
    return () => {
      clearTimeout(timer)
      sheet.removeEventListener('touchstart', start)
      sheet.removeEventListener('touchmove', move)
      sheet.removeEventListener('touchend', end)
      sheet.removeEventListener('touchcancel', end)
    }
  })
</script>

<svelte:window {onkeydown} />

<div class="fixed inset-0 z-40 grid grid-cols-[minmax(0,1fr)] place-items-center p-6 max-sm:place-items-end max-sm:p-0">
  <button
    class="absolute inset-0 cursor-default bg-scrim backdrop-blur-[2px]"
    aria-label={t('common.closeDialog')}
    tabindex="-1"
    bind:this={scrim}
    onclick={onclose}
    transition:fade={{ duration: 150 }}
  ></button>
  <div
    class="relative flex max-h-[calc(100vh-3rem)] w-full {width} flex-col rounded-lg border border-line bg-surface shadow-float outline-none
      max-sm:max-h-[calc(100dvh-var(--sat)-1.5rem)] max-sm:max-w-none max-sm:rounded-b-none max-sm:border-x-0 max-sm:border-b-0"
    bind:this={panel}
    role="dialog"
    aria-modal="true"
    tabindex="-1"
    aria-label={title}
    transition:enter
  >
    <!-- The grip says "this is a sheet" before anyone reads it. -->
    <div class="mx-auto mt-2 h-1 w-9 rounded-full bg-line sm:hidden" aria-hidden="true"></div>
    <header class="flex shrink-0 items-center justify-between px-6 pt-5 max-sm:px-5 max-sm:pt-3">
      <h2 class="text-lg font-medium">{title}</h2>
      <button
        class="rounded-md p-1 text-muted hover:bg-surface-2 hover:text-ink max-sm:-mr-3 max-sm:p-[13px]"
        aria-label={t('common.close')}
        onclick={onclose}
      >
        <X size={18} />
      </button>
    </header>
    <div class="min-h-0 overflow-y-auto overscroll-contain px-6 pt-4 pb-6 max-sm:px-5 max-sm:pb-[calc(var(--sab)+1.5rem)]" bind:this={content}>
      {@render children()}
    </div>
  </div>
</div>
