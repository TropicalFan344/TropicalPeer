// The Android back button, the way phone apps behave: close whatever is on
// top (a sheet, a menu), then step back toward Peers, then leave the app.
import { App } from '@capacitor/app'
import { isAndroid } from './api'
import { app } from './state.svelte'

const closers: (() => void)[] = []

/** Registers something back should close first; returns the unregister function. */
export function closeOnBack(close: () => void): () => void {
  closers.push(close)
  return () => {
    const i = closers.lastIndexOf(close)
    if (i >= 0) closers.splice(i, 1)
  }
}

export function initBack() {
  if (!isAndroid) return
  App.addListener('backButton', () => {
    const top = closers.at(-1)
    if (top) top()
    else if (app.page === 'network') app.page = 'settings'
    else if (app.page !== 'peers') app.page = 'peers'
    else App.minimizeApp() // stay running so transfers and offers keep arriving
  })
}
