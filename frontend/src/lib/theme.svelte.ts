// Appearance: follow Windows, or force light or dark. A per-device preference,
// so it lives in this WebView's storage rather than in the shared settings.
import { SystemBars, SystemBarsStyle, SystemBarType } from '@capacitor/core'
import { isAndroid } from './backend'
import { reducedMotion } from './motion'

export type Theme = 'system' | 'light' | 'dark'

const KEY = 'tropicalpeer.theme'

function read(): Theme {
  try {
    const v = localStorage.getItem(KEY)
    if (v === 'light' || v === 'dark') return v
  } catch {
    // Storage unavailable: follow the system.
  }
  return 'system'
}

export const theme = $state({ value: read() })

const systemDark = typeof matchMedia !== 'undefined' ? matchMedia('(prefers-color-scheme: dark)') : null

function apply(t: Theme) {
  if (t === 'system') delete document.documentElement.dataset.theme
  else document.documentElement.dataset.theme = t
  styleSystemBars()
}

// Android draws the clock and battery over the app; keep them readable on
// the ground colour, which follows the in-app choice, not the phone's.
function styleSystemBars() {
  if (!isAndroid) return
  const dark = theme.value === 'dark' || (theme.value === 'system' && !!systemDark?.matches)
  SystemBars.setStyle({ bar: SystemBarType.StatusBar, style: dark ? SystemBarsStyle.Dark : SystemBarsStyle.Light }).catch(() => {})
  // The navigation bar always sits on the jungle-green tab bar: light icons.
  SystemBars.setStyle({ bar: SystemBarType.NavigationBar, style: SystemBarsStyle.Dark }).catch(() => {})
}

export function setTheme(t: Theme) {
  theme.value = t
  // The garden changes light as a short crossfade, not a hard cut.
  if (document.startViewTransition && !reducedMotion()) document.startViewTransition(() => apply(t))
  else apply(t)
  try {
    if (t === 'system') localStorage.removeItem(KEY)
    else localStorage.setItem(KEY, t)
  } catch {
    // Not persisted; still applied for this session.
  }
}

/** Applies the saved choice before the first paint. */
export function initTheme() {
  apply(theme.value)
  systemDark?.addEventListener('change', styleSystemBars)
}
