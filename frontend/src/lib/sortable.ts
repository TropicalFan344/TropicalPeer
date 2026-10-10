// Rearranging peer cards by hand: drag with a mouse, or hold and drag on a
// touch screen. The grid's children carry data-slot="<id>"; the caller keeps
// the order and re-renders it (with animate:flip, so the other cards glide
// aside), and this action moves the lifted card under the pointer.
//
// Mouse: a press only becomes a drag after a few pixels of movement, so a
// click still sends files. Touch: a drag starts after a still hold, so
// scrolling and page swipes stay as they were.
import { tick } from 'svelte'
import { dur } from './motion'

export interface SortableOptions {
  ids: () => string[]
  /** Live: the order while dragging. */
  onmove: (ids: string[]) => void
  /** Once: the order the card was dropped in (only when it changed). */
  ondrop: (ids: string[]) => void
  enabled: () => boolean
}

const HOLD_MS = 380 // touch: hold this long, still, to pick a card up
const TOUCH_SLOP = 8 // touch: moving further than this first means scrolling
const MOUSE_SLOP = 5 // mouse: moving further than this means dragging, not clicking
const EDGE = 56 // autoscroll when the pointer is this close to the scroller's edge

export function sortable(grid: HTMLElement, opts: SortableOptions) {
  let options = opts
  let press: {
    id: string
    pointerId: number
    touch: boolean
    x0: number
    y0: number
    x: number
    y: number
    timer?: ReturnType<typeof setTimeout>
  } | null = null
  let lifted: { slot: HTMLElement; grabX: number; grabY: number; start: string[]; order: string[] } | null = null
  let suppressClick = false
  let frame = 0

  const slotOf = (id: string) => grid.querySelector<HTMLElement>(`[data-slot="${CSS.escape(id)}"]`)

  const scroller = () => {
    for (let el = grid.parentElement; el; el = el.parentElement) {
      const o = getComputedStyle(el).overflowY
      if (o === 'auto' || o === 'scroll') return el
    }
    return document.scrollingElement as HTMLElement
  }

  function onpointerdown(e: PointerEvent) {
    if (press || lifted || !options.enabled() || e.button !== 0) return
    const target = e.target as Element
    // Only the card itself, not its menu button, nor a dialog it opened.
    if (!target.closest('article') || target.closest('[data-no-drag]')) return
    const id = target.closest<HTMLElement>('[data-slot]')?.dataset.slot
    if (!id || options.ids().length < 2) return
    const touch = e.pointerType !== 'mouse'
    press = { id, pointerId: e.pointerId, touch, x0: e.clientX, y0: e.clientY, x: e.clientX, y: e.clientY }
    if (touch) press.timer = setTimeout(lift, HOLD_MS)
    window.addEventListener('pointermove', onpointermove)
    window.addEventListener('pointerup', onpointerup)
    window.addEventListener('pointercancel', onpointerup)
  }

  function onpointermove(e: PointerEvent) {
    if (!press || e.pointerId !== press.pointerId) return
    press.x = e.clientX
    press.y = e.clientY
    const moved = Math.hypot(press.x - press.x0, press.y - press.y0)
    if (!lifted) {
      if (press.touch && moved > TOUCH_SLOP) end() // a scroll or a swipe, not a hold
      else if (!press.touch && moved > MOUSE_SLOP) lift()
      return
    }
    follow()
  }

  function lift() {
    if (!press) return
    const slot = slotOf(press.id)
    if (!slot) return end()
    const r = slot.getBoundingClientRect()
    const ids = options.ids()
    lifted = { slot, grabX: press.x0 - r.left, grabY: press.y0 - r.top, start: ids, order: ids }
    slot.dataset.lifted = ''
    slot.style.transition = 'none'
    document.body.classList.add('sorting')
    navigator.vibrate?.(10)
    follow()
    frame = requestAnimationFrame(autoscroll)
  }

  // Keep the lifted card under the pointer, and make room where it hovers.
  function follow() {
    if (!press || !lifted) return
    const g = grid.getBoundingClientRect()
    const px = press.x - g.left
    const py = press.y - g.top
    const { slot } = lifted
    // Layout positions (offsetLeft/Top) ignore the other cards' glide
    // transforms, so hovering mid-animation doesn't make them flicker.
    for (const other of grid.querySelectorAll<HTMLElement>('[data-slot]')) {
      if (other === slot) continue
      const inside =
        px >= other.offsetLeft &&
        px <= other.offsetLeft + other.offsetWidth &&
        py >= other.offsetTop &&
        py <= other.offsetTop + other.offsetHeight
      if (!inside) continue
      const order = lifted.order.filter((id) => id !== press!.id)
      order.splice(lifted.order.indexOf(other.dataset.slot!), 0, press.id)
      lifted.order = order
      options.onmove(order)
      tick().then(place) // the card's slot moved; re-place it under the pointer
      break
    }
    place()
  }

  function place() {
    if (!press || !lifted) return
    const g = grid.getBoundingClientRect()
    const { slot, grabX, grabY } = lifted
    const x = press.x - g.left - grabX - slot.offsetLeft
    const y = press.y - g.top - grabY - slot.offsetTop
    slot.style.transform = `translate(${x}px, ${y}px)`
  }

  function autoscroll() {
    if (!press || !lifted) return
    const el = scroller()
    const r = el === document.scrollingElement ? { top: 0, bottom: innerHeight } : el.getBoundingClientRect()
    const speed = press.y < r.top + EDGE ? -(r.top + EDGE - press.y) : press.y > r.bottom - EDGE ? press.y - (r.bottom - EDGE) : 0
    if (speed) {
      el.scrollTop += speed * 0.35
      follow()
    }
    frame = requestAnimationFrame(autoscroll)
  }

  function onpointerup(e: PointerEvent) {
    if (!press || e.pointerId !== press.pointerId) return
    if (lifted) {
      suppressClick = true // the release isn't a click on the card
      setTimeout(() => (suppressClick = false), 0)
    }
    end()
  }

  function end() {
    if (press?.timer) clearTimeout(press.timer)
    cancelAnimationFrame(frame)
    window.removeEventListener('pointermove', onpointermove)
    window.removeEventListener('pointerup', onpointerup)
    window.removeEventListener('pointercancel', onpointerup)
    if (lifted) {
      const { slot, start, order } = lifted
      // Settle into the slot.
      slot.style.transition = `transform ${dur(260)}ms var(--ease-out-expo)`
      slot.style.transform = ''
      delete slot.dataset.lifted
      setTimeout(() => (slot.style.transition = ''), dur(260))
      document.body.classList.remove('sorting')
      if (order.join() !== start.join()) options.ondrop(order)
    }
    press = null
    lifted = null
  }

  // Once a card is up, a touch drag moves the card, not the page or the pager.
  function ontouchmove(e: TouchEvent) {
    if (lifted && e.cancelable) e.preventDefault()
  }
  // A long press on Android would otherwise open the context menu.
  function oncontextmenu(e: Event) {
    if (press?.touch) e.preventDefault()
  }
  function onclick(e: MouseEvent) {
    if (suppressClick) {
      e.stopPropagation()
      e.preventDefault()
    }
  }

  // Keyboard: Alt + arrow keys move the focused card.
  function onkeydown(e: KeyboardEvent) {
    if (!e.altKey || !options.enabled()) return
    const step = { ArrowLeft: -1, ArrowUp: -1, ArrowRight: 1, ArrowDown: 1 }[e.key]
    const id = (e.target as Element).closest<HTMLElement>('[data-slot]')?.dataset.slot
    if (!step || !id) return
    e.preventDefault()
    const ids = options.ids()
    const i = ids.indexOf(id)
    const j = Math.max(0, Math.min(ids.length - 1, i + step))
    if (i === j) return
    const order = [...ids]
    order.splice(j, 0, ...order.splice(i, 1))
    options.onmove(order)
    options.ondrop(order)
    // Moving the element can drop focus; keep it on the card.
    tick().then(() => slotOf(id)?.querySelector<HTMLElement>('article button')?.focus())
  }

  grid.addEventListener('pointerdown', onpointerdown)
  grid.addEventListener('touchmove', ontouchmove, { passive: false })
  grid.addEventListener('contextmenu', oncontextmenu)
  grid.addEventListener('click', onclick, true)
  grid.addEventListener('keydown', onkeydown)
  return {
    update(next: SortableOptions) {
      options = next
    },
    destroy() {
      end()
      grid.removeEventListener('pointerdown', onpointerdown)
      grid.removeEventListener('touchmove', ontouchmove)
      grid.removeEventListener('contextmenu', oncontextmenu)
      grid.removeEventListener('click', onclick, true)
      grid.removeEventListener('keydown', onkeydown)
    },
  }
}
