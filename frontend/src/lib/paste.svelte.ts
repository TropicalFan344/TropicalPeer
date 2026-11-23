// Ctrl+V (Cmd+V on a Mac) with a picture on the clipboard sends it, like a
// file dropped on the window: to the device whose compose bar is open, onto
// the Links page to make a link, or else ShareSheet asks which device.
//
// A clipboard picture has no path, so Go saves it to a file first
// (App.SavePasted); after that it's sent like any other file.
import { api, errorText } from './api'
import { app, composeNote, sendTo, shareLink, toast } from './state.svelte'
import { t } from './i18n.svelte'

const maxSize = 200 << 20 // as in internal/desktopapp/pasted.go

const editable = (el: EventTarget | null) =>
  el instanceof HTMLElement && (el.isContentEditable || el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement)

function base64(f: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader()
    r.onload = () => resolve(String(r.result).replace(/^data:[^,]*,/, ''))
    r.onerror = () => reject(r.error)
    r.readAsDataURL(f)
  })
}

export function initPaste() {
  window.addEventListener('paste', (e) => {
    const files = [...(e.clipboardData?.files ?? [])]
    if (!files.length || e.defaultPrevented) return // a card took the words (PeerCard)
    // Typing in a field: words win. (Copying from Word or a web page puts a
    // picture of the words on the clipboard too.)
    if (editable(e.target) && e.clipboardData?.getData('text/plain').trim()) return
    e.preventDefault()
    void send(files)
  })
}

async function send(files: File[]) {
  if (files.some((f) => f.size > maxSize)) {
    toast(t('share.pasteTooBig'), 'error', 6000)
    return
  }
  let paths: string[]
  try {
    paths = await Promise.all(files.map(async (f) => api.savePasted(f.name, await base64(f))))
  } catch (e) {
    toast(t('share.pasteFailed', { error: errorText(e) }), 'error', 6000)
    return
  }
  if (app.page === 'links') {
    shareLink(paths)
    return
  }
  const peer = app.peers.find((p) => p.id === app.compose.peerId)
  if (peer) {
    sendTo(peer, paths, composeNote(peer)) // the words in the bar go with it
    return
  }
  setPreview(files.length === 1 && files[0]!.type.startsWith('image/') ? URL.createObjectURL(files[0]!) : '')
  app.shared = paths
}

/** A look at the pasted picture while ShareSheet asks where it goes. */
export const pasted = $state({ preview: '' })

export function setPreview(url: string) {
  if (pasted.preview) URL.revokeObjectURL(pasted.preview)
  pasted.preview = url
}
