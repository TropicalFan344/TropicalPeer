// Typed wrappers around the Go methods (app.go on desktop, mobile/ on
// Android; see backend.ts) and the events they emit.
import * as backend from './backend'
import { call, isAndroid, isMac, type PickedFile } from './backend'

export { isAndroid, isMac, type PickedFile }

export interface DeviceInfo {
  name: string
  id: string
  port: number
  version: string
}

export interface ServerStatus {
  address: string
  online: boolean
  error: string
}

export interface Settings {
  serverHost: string
  serverPort: number
  listenPort: number
  downloadDir: string
  portMapping: boolean
}

export interface Network {
  serverAddr: string
  serverOnline: boolean
  serverError: string
  rttMs: number // server round trip, 0 = unknown
  relay: boolean // the server relays when direct paths fail
  detection: boolean // the server supports router detection
  publicAddr: string
  router: 'unknown' | 'open' | 'restricted' | 'strict'
  portMapping: boolean // forwarding turned on in settings
  forwarded: string // "UPnP 203.0.113.7:7778" when the router forwards our port
  mappingErr: string
  checking: boolean
}

export type Policy = 'ask' | 'auto' | 'block'

export interface Peer {
  id: string
  name: string
  policy: Policy
  maxAutoSize: number
  saveDir: string
  addedAt: number
  /** The peer removed this device: sending fails until the two pair again. */
  removedUs: boolean
}

export type PeerUpdate = Pick<Peer, 'name' | 'policy' | 'maxAutoSize' | 'saveDir'>

export interface Invite {
  code: string
  expiresAt: number
}

export type TransferStatus =
  | 'connecting'
  | 'waiting'
  | 'sending'
  | 'receiving'
  | 'done'
  | 'declined'
  | 'failed'
  | 'cancelled'
  | 'paused'

export interface Transfer {
  id: string
  peerId: string
  peerName: string
  direction: 'send' | 'receive'
  status: TransferStatus
  files: { name: string; size: number }[]
  fileIndex: number
  done: number
  total: number
  speed: number
  relayed: boolean // runs through the rendezvous server's relay
  upgraded: boolean // moved from the relay to a direct path mid-transfer
  resuming: boolean // send: the connection dropped; trying to pick up where it stopped
  pausedByPeer: boolean // paused on the other device, not here
  error: string
  saveDir: string // receive: destination folder
  saved: string[] // receive: full paths of saved files
  startedAt: number
  endedAt: number
}

export const isActive = (t: Transfer) =>
  t.status === 'connecting' || t.status === 'waiting' || t.status === 'sending' || t.status === 'receiving'

export interface Offer {
  id: string
  peerId: string
  peerName: string
  files: { name: string; size: number }[]
  total: number
  saveDir: string
  expiresAt: number
}

/** New versions (see internal/appcore/updates.go). */
export interface Update {
  state: 'idle' | 'checking' | 'current' | 'downloading' | 'ready' | 'installing' | 'failed' | 'unsupported'
  current: string
  version: string // the newer version, when there is one
  done: number
  total: number
  error: string
  checkedAt: number // unix ms
  auto: boolean
  restartAt: number // unix ms when it installs by itself; 0 = not scheduled
}

export const api = {
  getDevice: () => call<DeviceInfo>('GetDevice'),
  getServerStatus: () => call<ServerStatus>('GetServerStatus'),
  getNetwork: () => call<Network>('GetNetwork'),
  recheckNetwork: () => call<void>('RecheckNetwork'),
  /** Restarts networking in place, applying the listening port. */
  restart: () => call<void>('Restart'),
  getSettings: () => call<Settings>('GetSettings'),
  saveSettings: (s: Settings) => call<void>('SaveSettings', s),
  setDeviceName: (name: string) => call<void>('SetDeviceName', name),
  /** Desktop only: Android decides where received files go. */
  chooseFolder: (title: string, start: string) => call<string>('ChooseFolder', title, start),
  listPeers: () => call<Peer[]>('ListPeers'),
  removePeer: (id: string) => call<void>('RemovePeer', id),
  /** Saves the order the user arranged the peers in. */
  reorderPeers: (ids: string[]) => call<void>('ReorderPeers', ids),
  updatePeer: (id: string, u: PeerUpdate) => call<Peer>('UpdatePeer', id, u),
  startInvite: () => call<Invite>('StartInvite'),
  cancelInvite: () => call<void>('CancelInvite'),
  joinWithCode: (code: string) => call<Peer>('JoinWithCode', code),
  pickFiles: backend.pickFiles,
  pickFolder: backend.pickFolder,
  pickMedia: backend.pickMedia,
  sendFiles: (peerId: string, files: PickedFile[]) =>
    call<Transfer>(isAndroid ? 'SendDocuments' : 'SendFiles', peerId, files),
  cancelTransfer: (id: string) => call<void>('CancelTransfer', id),
  retryTransfer: (id: string) => call<Transfer>('RetryTransfer', id),
  pauseTransfer: (id: string) => call<void>('PauseTransfer', id),
  resumeTransfer: (id: string) => call<Transfer>('ResumeTransfer', id),
  listTransfers: () => call<Transfer[]>('ListTransfers'),
  clearFinishedTransfers: () => call<void>('ClearFinishedTransfers'),
  listOffers: () => call<Offer[]>('ListOffers'),
  respondOffer: (id: string, accept: boolean) => call<void>('RespondOffer', id, accept),
  showFile: backend.showFile,
  openFolder: backend.openFolder,
  getUpdate: () => call<Update>('GetUpdate'),
  checkForUpdate: () => call<Update>('CheckForUpdate'),
  setAutoUpdate: (on: boolean) => call<void>('SetAutoUpdate', on),
  /** Restarts into the new version (computer) or opens Android's installer. */
  installUpdate: () => call<void>('InstallUpdate'),
  postponeUpdate: () => call<void>('PostponeUpdate'),
}

interface EventMap {
  'server:status': ServerStatus
  'network:changed': Network
  'device:changed': DeviceInfo
  'node:error': string
  'peers:changed': Peer[]
  'pair:done': Peer
  'pair:failed': string
  'transfer:update': Transfer
  'transfers:cleared': null
  'offer:new': Offer
  'offer:closed': { id: string; reason: string }
  'peer:removedUs': Peer
  'update:changed': Update
}

export function on<K extends keyof EventMap>(event: K, fn: (data: EventMap[K]) => void): () => void {
  return backend.subscribe(event, fn as (data: unknown) => void)
}

// Go errors arrive as rejected promises carrying a plain string.
export function errorText(e: unknown): string {
  if (typeof e === 'string') return e
  if (e instanceof Error) return e.message
  return String(e)
}
