// Where the navigation indicator sits. The rail and the phone tab bar draw
// one raised fill that slides between items; its position is 0 = Peers,
// 1 = Transfers, then Links (desktop) and Settings, fractional while a page is being swiped so
// the fill follows the finger. Each fill registers with use:navIndicator
// and is moved by setting its own transform: a custom property on the root
// would restyle every page on every frame of a swipe.
import { isAndroid } from './api'
import type { Page } from './state.svelte'

// Links (share by link) are desktop-only for now.
export const pages: Page[] = isAndroid ? ['peers', 'transfers', 'settings'] : ['peers', 'transfers', 'links', 'settings']

/** The nav slot of a page; Network lives under Settings. */
export const navIndex = (p: Page) => pages.indexOf(p === 'network' ? 'settings' : p)

/** The Android app swipes between pages; desktop switches in place. */
export const swipePages = isAndroid

export const nav = $state({
  near: 0, // the slot nearest the pager's current position, for active styling mid-swipe
})

type Place = (x: number) => string
const indicators = new Map<HTMLElement, Place>()
let pos = 0

export function setNavPos(x: number) {
  pos = x
  for (const [el, place] of indicators) el.style.transform = place(x)
}

/** Registers a nav fill; place turns a position into its transform. */
export function navIndicator(el: HTMLElement, place: Place) {
  indicators.set(el, place)
  el.style.transform = place(pos)
  return { destroy: () => indicators.delete(el) }
}
