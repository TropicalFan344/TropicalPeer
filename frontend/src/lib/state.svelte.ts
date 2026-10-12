// Global reactive app state, kept in sync with the Go backend via events.
import {
  api,
  errorText,
  isActive,
  on,
  type DeviceInfo,
  type Network,
  type Offer,
  type Peer,
  type ServerStatus,
  type PickedFile,
  type Transfer,
  type Update,
  isAndroid,
} from './api'
import { onShare } from './backend'
import { t, what as describe } from './i18n.svelte'

export type Page = 'peers' | 'transfers' | 'network' | 'settings'

export const app = $state({
  page: 'peers' as Page,
  device: null as DeviceInfo | null,
  server: { address: '', online: false, error: '' } as ServerStatus,
  network: {
    serverAddr: '',
    serverOnline: false,
    serverError: '',
    rttMs: 0,
    relay: false,
    detection: false,
    publicAddr: '',
    router: 'unknown',
    portMapping: true,
    forwarded: '',
    mappingErr: '',
    checking: false,
  } as Network,
  peers: [] as Peer[],
  transfers: [] as Transfer[], // newest first
  offers: [] as Offer[], // incoming, waiting for an answer, oldest first
  networkCheckedAt: 0, // when network info last arrived (ms)
  shared: [] as PickedFile[], // files shared from another app, waiting for a device (Android)
  restarting: false, // the node is restarting (Settings → Restart)
  settingsFocus: '', // id of a Settings row to bring into view when Settings opens
  // Set when the node can't run (e.g. no network port available).
  fatalError: '',
  update: null as Update | null,
})

export async function initState() {
  onShare((files) => {
    app.shared = files
  })
  on('server:status', (s) => {
    app.server = s
    api.getNetwork().then((n) => {
      app.network = n
    })
  })
  on('network:changed', (n) => {
    app.network = n
    if (!n.checking) app.networkCheckedAt = Date.now()
  })
  on('device:changed', (d) => {
    app.device = d
  })
  on('node:error', (e) => {
    app.fatalError = e
  })
  on('peers:changed', (p) => {
    app.peers = p
  })
  on('peer:removedUs', (p) => toast(t('peer.removedUsToast', { name: p.name }), 'info', 8000))
  on('transfer:update', upsertTransfer)
  on('transfers:cleared', () => {
    app.transfers = app.transfers.filter(isActive)
  })
  on('offer:new', (o) => {
    app.offers.push(o)
  })
  on('offer:closed', ({ id, reason }) => {
    if (dropOffer(id) && reason) toast(reason, 'info', 6000)
  })
  on('update:changed', applyUpdate)
  api.getUpdate().then(applyUpdate)

  const [device, server, network, peers, transfers, offers] = await Promise.all([
    api.getDevice(),
    api.getServerStatus(),
    api.getNetwork(),
    api.listPeers(),
    api.listTransfers(),
    api.listOffers(),
  ])
  app.device = device
  app.server = server
  app.network = network
  app.networkCheckedAt = Date.now()
  app.peers = peers
  app.transfers = transfers
  app.offers = offers
  // A phone may answer an offer from its notification while the app is in
  // the background: pick up what is still waiting when it comes back.
  document.addEventListener('visibilitychange', () => {
    if (!document.hidden) api.listOffers().then((o) => (app.offers = o))
  })
}

/** Keeps the update status, and gives notice before an update installs by itself. */
function applyUpdate(u: Update) {
  const prev = app.update
  app.update = u
  if (u.restartAt && u.restartAt !== prev?.restartAt) {
    const ms = Math.max(1000, u.restartAt - Date.now())
    toast(
      t(isAndroid ? 'update.installSoon' : 'update.restartSoon', { version: u.version, s: Math.round(ms / 1000) }),
      'info',
      ms,
      { label: t('update.notNow'), run: () => api.postponeUpdate() },
    )
  }
}

function dropOffer(id: string): boolean {
  const i = app.offers.findIndex((o) => o.id === id)
  if (i < 0) return false
  app.offers.splice(i, 1)
  return true
}

/** Answers an incoming offer. */
export async function respondOffer(offer: Offer, accept: boolean) {
  dropOffer(offer.id)
  try {
    await api.respondOffer(offer.id, accept)
  } catch (e) {
    toast(errorText(e), 'info', 5000)
  }
}

function upsertTransfer(x: Transfer) {
  const i = app.transfers.findIndex((y) => y.id === x.id)
  const prev = i >= 0 ? app.transfers[i] : null
  if (i >= 0) app.transfers[i] = x
  else app.transfers.unshift(x)

  // Announce how a transfer ended, once.
  // The other side cancelled while it was paused: nothing was running, so
  // the check below wouldn't notice.
  if (prev?.status === 'paused' && x.status === 'failed' && x.error) toast(x.error, 'info', 6000)
  if (prev && isActive(prev) && !isActive(x)) {
    const what = describe(x.files)
    const name = x.peerName
    if (x.status === 'paused') {
      // Pausing here needs no toast; the other side pausing does.
      if (x.pausedByPeer) toast(t('transfer.toastPaused', { what, name }), 'info', 5000)
    } else if (x.direction === 'receive') {
      if (x.status === 'done') {
        const first = x.saved[0]
        const one = x.saved.length === 1
        toast(
          t('transfer.toastReceived', { what, name }),
          'success',
          8000,
          first
            ? {
                label: t(one ? (isAndroid ? 'transfer.open' : 'transfer.showFile') : isAndroid ? 'transfer.openLocation' : 'transfer.openFolder'),
                run: () => (one ? showFile(first) : openFolder(x.saveDir)),
              }
            : undefined,
        )
      } else if (x.status === 'failed') toast(x.error || t('transfer.toastReceiveFailed', { name }), 'error', 6000)
    } else {
      if (x.status === 'done') toast(t('transfer.toastSent', { what, name }))
      else if (x.status === 'declined') toast(t('transfer.toastDeclined', { what, name }), 'info', 5000)
      else if (x.status === 'failed') toast(x.error || t('transfer.toastSendFailed', { name }), 'error', 6000)
    }
  }
}

export const activeTransfers = () => app.transfers.filter(isActive)

/** Starts sending; reports problems as toasts. */
export async function sendTo(peer: Peer, files: PickedFile[]) {
  if (files.length === 0) return
  try {
    await api.sendFiles(peer.id, files)
  } catch (e) {
    toast(errorText(e), 'error', 6000)
  }
}

/** What to choose before sending: files, a folder, or photos and videos (Android). */
export type PickKind = 'files' | 'folder' | 'media'

/** Opens the picker for kind, then sends what was chosen. */
export async function pickAndSend(peer: Peer, kind: PickKind = 'files') {
  let files: PickedFile[]
  try {
    files = await (kind === 'folder' ? api.pickFolder() : kind === 'media' ? api.pickMedia() : api.pickFiles())
  } catch (e) {
    toast(t('common.filePickerFailed', { error: errorText(e) }), 'error', 6000)
    return
  }
  if (files.length) await sendTo(peer, files)
}

/** Restarts networking without quitting (applies the listening port). */
export async function restartEngine() {
  if (app.restarting) return
  app.restarting = true
  try {
    await api.restart()
    toast(t('settings.restarted'))
  } catch (e) {
    toast(errorText(e), 'error', 6000)
  } finally {
    app.restarting = false
  }
}

// ---- Toasts ----

export interface Toast {
  id: number
  kind: 'success' | 'error' | 'info'
  text: string
  action?: { label: string; run: () => void }
}

export const toasts = $state<Toast[]>([])
let nextToast = 1

export function toast(text: string, kind: Toast['kind'] = 'success', ms = 3500, action?: Toast['action']) {
  const id = nextToast++
  if (toasts.length >= 4) toasts.shift()
  toasts.push({ id, kind, text, action })
  setTimeout(() => dismissToast(id), ms)
}

export function dismissToast(id: number) {
  const i = toasts.findIndex((t) => t.id === id)
  if (i >= 0) toasts.splice(i, 1)
}

// ---- Files on disk ----

export async function showFile(path: string) {
  try {
    await api.showFile(path)
  } catch (e) {
    toast(errorText(e), 'error')
  }
}

export async function openFolder(dir: string) {
  try {
    await api.openFolder(dir)
  } catch (e) {
    toast(errorText(e), 'error')
  }
}
