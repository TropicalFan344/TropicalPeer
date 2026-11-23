// Ctrl+V (Cmd+V on a Mac) with a picture on the clipboard: in a device's
// compose bar (or onto its focused card, which opens the bar) it waits there
// with the words until the person sends; onto the Links page it makes a
// link; anywhere else ShareSheet asks which device.
//
// A clipboard picture has no path, so Go saves it to a file first
// (App.SavePasted); after that it's sent like any other file.
import { api, errorText } from './api'
import { app, openCompose, shareLink, toast } from './state.svelte'
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
    // A computer's card, focused or with its bar open (a narrow window has
    // no bar: phones write in the send chooser).
    const wide = !matchMedia('(max-width: 639.98px)').matches
    const cardId = (e.target as Element | null)?.closest?.<HTMLElement>('[data-peer-id]')?.dataset.peerId
    const peerId = wide ? app.compose.peerId || cardId || '' : ''
    void send(files, peerId)
  })
}

async function send(files: File[], peerId: string) {
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
  const peer = app.peers.find((p) => p.id === peerId)
  if (peer && !peer.removedUs) {
    openCompose(peer)
    app.compose.attached.push(
      ...paths.map((path, i) => ({
        path,
        name: path.split(/[\\/]/).pop() ?? path,
        preview: files[i]!.type.startsWith('image/') ? URL.createObjectURL(files[i]!) : '',
      })),
    )
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
