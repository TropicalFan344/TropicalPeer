// Where the Go side lives. On desktop it's bound into the page by Wails
// (app.go); on Android it's the TropicalPeer Capacitor plugin around the Go
// engine (frontend/android/.../TropicalPeerPlugin.java, mobile/mobile.go).
// Both take the same method names and arguments and emit the same events,
// so api.ts only notices the difference for files and folders.
import { Capacitor, registerPlugin } from '@capacitor/core'
import * as Go from '../../wailsjs/go/main/App'
import { EventsOn } from '../../wailsjs/runtime/runtime'

export const isAndroid = Capacitor.getPlatform() === 'android'
/** The desktop app on a Mac (Finder, not Explorer). */
export const isMac = !isAndroid && /Macintosh/.test(navigator.userAgent)

/** A file picked to send: a path on desktop, a document on Android. */
export type PickedFile = string | { uri: string; name: string; size: number }

interface TropicalPeerPlugin {
  call(o: { method: string; args: string }): Promise<{ json: string }>
  pickFiles(): Promise<{ files: { uri: string; name: string; size: number }[] }>
  pickFolder(): Promise<{ files: { uri: string; name: string; size: number }[] }>
  pickMedia(): Promise<{ files: { uri: string; name: string; size: number }[] }>
  showFile(o: { path: string }): Promise<void>
  openFolder(): Promise<void>
  getReady(): Promise<ReadyState>
  setReady(o: { on: boolean }): Promise<ReadyState>
  openNotificationSettings(): Promise<void>
  getDownloadFolder(): Promise<DownloadFolder>
  chooseDownloadFolder(): Promise<DownloadFolder>
  resetDownloadFolder(): Promise<DownloadFolder>
  addListener(event: 'event', fn: (e: { name: string; json: string }) => void): Promise<unknown>
  addListener(event: 'share', fn: (e: { files: SharedFile[] }) => void): Promise<unknown>
}

type SharedFile = { uri: string; name: string; size: number }

/** Where Android puts received files: a readable path, and whether the user chose it. */
export interface DownloadFolder {
  name: string
  custom: boolean
}

/** Android's "Stay ready to receive", and whether notifications can reach the user. */
export interface ReadyState {
  on: boolean
  notifications: boolean
}

const native = isAndroid ? registerPlugin<TropicalPeerPlugin>('TropicalPeer') : null

// Errors reach the UI as plain strings, the way Wails delivers Go errors.
const message = (e: unknown) => (e instanceof Error ? e.message : String(e))

/** Runs a Go method by name. */
export async function call<T>(method: string, ...args: unknown[]): Promise<T> {
  if (!native) {
    const fn = (Go as unknown as Record<string, (...a: unknown[]) => Promise<T>>)[method]
    return fn(...args)
  }
  try {
    const { json } = await native.call({ method, args: JSON.stringify(args) })
    return JSON.parse(json) as T
  } catch (e) {
    throw message(e)
  }
}

const handlers = new Map<string, Set<(data: unknown) => void>>()
native?.addListener('event', ({ name, json }) => {
  const data = JSON.parse(json) as unknown
  handlers.get(name)?.forEach((fn) => fn(data))
})

/** Subscribes to a Go event; returns the unsubscribe function. */
export function subscribe(event: string, fn: (data: unknown) => void): () => void {
  if (!native) return EventsOn(event, fn)
  let set = handlers.get(event)
  if (!set) handlers.set(event, (set = new Set()))
  set.add(fn)
  return () => set.delete(fn)
}

/** Files shared to TropicalPeer from another app's share menu (Android). */
export function onShare(fn: (files: PickedFile[]) => void) {
  native?.addListener('share', ({ files }) => {
    if (files.length) fn(files)
  })
}

export async function getReady(): Promise<ReadyState | null> {
  return native ? native.getReady() : null
}

export async function setReady(on: boolean): Promise<ReadyState> {
  if (!native) throw 'Only on Android'
  try {
    return await native.setReady({ on })
  } catch (e) {
    throw message(e)
  }
}

async function nativeOr<T>(fn: (n: TropicalPeerPlugin) => Promise<T>): Promise<T> {
  if (!native) throw 'Only on Android'
  try {
    return await fn(native)
  } catch (e) {
    throw message(e)
  }
}

export const getDownloadFolder = () => nativeOr((n) => n.getDownloadFolder())
export const chooseDownloadFolder = () => nativeOr((n) => n.chooseDownloadFolder())
export const resetDownloadFolder = () => nativeOr((n) => n.resetDownloadFolder())

export async function openNotificationSettings(): Promise<void> {
  try {
    await native?.openNotificationSettings()
  } catch (e) {
    throw message(e)
  }
}

/** Opens the system file picker; an empty list means cancelled. */
export async function pickFiles(): Promise<PickedFile[]> {
  if (!native) return (await Go.PickFiles()) ?? []
  try {
    return (await native.pickFiles()).files
  } catch (e) {
    throw message(e)
  }
}

/** Opens the system folder picker; an empty list means cancelled. On
 *  Android the folder arrives as its files, named "Folder/sub/file". */
export async function pickFolder(): Promise<PickedFile[]> {
  if (!native) return (await Go.PickFolder()) ?? []
  try {
    return (await native.pickFolder()).files
  } catch (e) {
    throw message(e)
  }
}

/** Opens the photo picker (Android only; photos and videos, no camera). */
export async function pickMedia(): Promise<PickedFile[]> {
  if (!native) return pickFiles()
  try {
    return (await native.pickMedia()).files
  } catch (e) {
    throw message(e)
  }
}

export async function showFile(path: string): Promise<void> {
  if (!native) return Go.ShowFile(path)
  try {
    await native.showFile({ path })
  } catch (e) {
    throw message(e)
  }
}

/** Desktop: opens the folder. Android: opens Downloads, where received files go. */
export async function openFolder(dir: string): Promise<void> {
  if (!native) return Go.OpenFolder(dir)
  try {
    await native.openFolder()
  } catch (e) {
    throw message(e)
  }
}
