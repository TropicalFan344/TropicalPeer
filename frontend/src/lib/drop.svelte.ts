// Drag-and-drop of files from Explorer onto peer cards, or onto the Links
// page to make a link.
//
// The browser's drag events tell us *where* files are being dragged, so we
// can highlight the peer under the cursor. The browser can't see real file
// paths, though, so the drop itself comes from Wails (OnFileDrop), which
// reports the drop position and the absolute paths.
import { OnFileDrop } from '../../wailsjs/runtime/runtime'
import { app, sendTo, shareLink, toast } from './state.svelte'
import { t } from './i18n.svelte'

export const drag = $state({
  active: false, // files are being dragged over the window
  overPeer: '', // id of the peer card under the cursor
})

const peerIdAt = (el: Element | null) => el?.closest<HTMLElement>('[data-peer-id]')?.dataset.peerId ?? ''

const hasFiles = (e: DragEvent) => e.dataTransfer?.types.includes('Files') ?? false

function reset() {
  drag.active = false
  drag.overPeer = ''
}

export function initDrop() {
  window.addEventListener('dragenter', (e) => {
    if (!hasFiles(e)) return
    drag.active = true
    if (app.page !== 'links') app.page = 'peers' // files drop on a peer, or onto Links to make a link
  })
  window.addEventListener('dragover', (e) => {
    if (!hasFiles(e)) return
    drag.active = true
    drag.overPeer = peerIdAt(e.target as Element)
  })
  window.addEventListener('dragleave', (e) => {
    // relatedTarget is null only when the cursor leaves the window.
    if (hasFiles(e) && e.relatedTarget === null) reset()
  })
  window.addEventListener('drop', reset)

  OnFileDrop((x, y, paths) => {
    reset()
    if (app.page === 'links') {
      shareLink(paths)
      return
    }
    const id = peerIdAt(document.elementFromPoint(x, y))
    const peer = app.peers.find((p) => p.id === id)
    if (!peer) {
      toast(t(app.peers.length ? 'peers.dropOnPeer' : 'peers.pairFirstToDrop'), 'info')
      return
    }
    sendTo(peer, paths)
  }, false)
}
