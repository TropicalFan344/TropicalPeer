// The browser side of share by link: signaling over the server's WebSocket,
// a WebRTC DataChannel to the sender's device, and the checks that keep a
// server in the middle out (see internal/linkshare):
//  1. through the server, prove we have the link's secret ("hello"); only
//     then does the sender admit us and the server give STUN/TURN servers;
//  2. on the DataChannel, prove the secret again and check the sender's
//     signature, made with the key the link ID is a hash of; both bound to
//     the DTLS fingerprints.
import { ed25519 } from '@noble/curves/ed25519'
import { sha256 } from '@noble/hashes/sha2'

export type FileInfo = { name: string; size: number }
export type Hello = { name: string; from: string; files: FileInfo[]; total: number }
// error is a code: 'cancel' (the sender cancelled), 'lost', 'cancelled'
// (here), or the browser's own message when saving failed (savingFailed).
export type GetResult = { sha256?: string; error?: string; savingFailed?: boolean }

type Handlers = {
  onHello: (h: Hello) => void
  // gone, used, busy, auth, cancel, lost or unreachable
  onError: (code: string) => void
}

const authContext = 'tropicalpeer-link-v2'

function b64urlDecode(s: string): Uint8Array {
  const pad = s.replace(/-/g, '+').replace(/_/g, '/') + '==='.slice((s.length + 3) % 4)
  return Uint8Array.from(atob(pad), (c) => c.charCodeAt(0))
}

function b64urlEncode(b: Uint8Array): string {
  return btoa(String.fromCharCode(...b)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

// The DTLS fingerprint in a description. Exactly one is accepted: with
// several, the browser's DTLS could accept a certificate by one of them
// while the key check covers another.
function fingerprint(sdp: string | undefined): string {
  const found = new Set<string>()
  for (const line of (sdp ?? '').split(/\r?\n/)) {
    const m = line.match(/^a=fingerprint:(\S+) (\S+)/)
    if (m) found.add(m[1].toLowerCase() + ' ' + m[2].toUpperCase())
  }
  const [only] = found
  if (found.size !== 1 || !only.startsWith('sha-256 ')) throw new Error('no single sha-256 DTLS fingerprint')
  return only.slice('sha-256 '.length)
}

function hexFingerprint(b: Uint8Array): string {
  return Array.from(b, (x) => x.toString(16).toUpperCase().padStart(2, '0')).join(':')
}

// The certificate the sender actually presented, where the browser says
// (Chromium): it must be the one in its description.
function remoteCertMatches(pc: RTCPeerConnection, fp: string): boolean {
  const transport = pc.sctp?.transport as (RTCDtlsTransport & { getRemoteCertificates?: () => ArrayBuffer[] }) | undefined
  const certs = transport?.getRemoteCertificates?.()
  if (!certs?.length) return true // not available here; the single-fingerprint rule stands
  return hexFingerprint(sha256(new Uint8Array(certs[0]))) === fp
}

// The link ID for a public key, as the sender's app makes it
// (rendezvous.LinkIDFor): base32 of the first 10 bytes of a hash.
function linkIDFor(pub: Uint8Array): string {
  const prefix = new TextEncoder().encode('tropicalpeer-link-id\n')
  const h = sha256(new Uint8Array([...prefix, ...pub])).slice(0, 10)
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
  let bits = 0
  let value = 0
  let out = ''
  for (const b of h) {
    value = (value << 8) | b
    bits += 8
    while (bits >= 5) {
      out += alphabet[(value >>> (bits - 5)) & 31]
      bits -= 5
    }
  }
  return out
}

export async function connect(link: string, secret: string, h: Handlers) {
  const key = await crypto.subtle.importKey('raw', b64urlDecode(secret), { name: 'HMAC', hash: 'SHA-256' }, false, ['sign', 'verify'])
  const payload = (role: string, session: string, senderFP: string, browserFP: string) =>
    new TextEncoder().encode([authContext, role, link, session, senderFP, browserFP].join('\n'))
  const hmac = async (data: Uint8Array) => b64urlEncode(new Uint8Array(await crypto.subtle.sign('HMAC', key, data)))

  const ws = new WebSocket((location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/ws')
  let pc: RTCPeerConnection | undefined
  let dc: RTCDataChannel | undefined
  let session = ''
  let finished = false
  let authed = false
  const early: RTCIceCandidateInit[] = []
  let inbox = Promise.resolve()

  // The file being received: chunks go to onChunk, one at a time.
  let current:
    | { onChunk: (b: Uint8Array) => Promise<void>; writes: Promise<void>; resolve: (r: GetResult) => void; broken: boolean }
    | undefined

  const failOnce = (code: string) => {
    if (finished) return
    finished = true
    current?.resolve({ error: code === 'cancel' || code === 'unreadable' ? code : 'lost' })
    current = undefined
    h.onError(code)
    pc?.close()
    ws.close()
  }

  const signal = (data: unknown) => {
    if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: 'signal', data }))
  }

  ws.onmessage = async (ev) => {
    const m = JSON.parse(ev.data)
    if (m.type === 'error') return failOnce(m.error)
    if (m.type === 'ended') {
      if (!authed) failOnce('gone')
      return
    }
    if (m.type === 'opened') {
      // Prove we have the secret; the sender admits us only then.
      session = m.session
      const hello = await hmac(new TextEncoder().encode([authContext, 'hello', link, session].join('\n')))
      signal({ hello })
      return
    }
    if (m.type === 'admitted') {
      pc = new RTCPeerConnection({ iceServers: m.ice ?? [] })
      dc = pc.createDataChannel('files', { ordered: true })
      dc.binaryType = 'arraybuffer'
      pc.onicecandidate = (e) => e.candidate && signal({ candidate: e.candidate.toJSON() })
      pc.onconnectionstatechange = () => {
        if (pc?.connectionState === 'failed') failOnce('unreachable')
      }
      dc.onopen = onOpen
      // One message at a time, in order: checking the sender's key is
      // async, and what follows it (the file list) must wait for it.
      dc.onmessage = (ev) => {
        inbox = inbox.then(() => onMessage(ev)).catch((e) => {
          })
      }
      dc.onclose = () => failOnce('lost')
      const offer = await pc.createOffer()
      await pc.setLocalDescription(offer)
      signal({ sdp: pc.localDescription })
      return
    }
    if (m.type === 'signal' && pc) {
      const d = m.data
      if (d.sdp) {
        await pc.setRemoteDescription(d.sdp)
        for (const c of early.splice(0)) await pc.addIceCandidate(c).catch(() => {})
      } else if (d.candidate) {
        if (pc.remoteDescription) await pc.addIceCandidate(d.candidate).catch(() => {})
        else early.push(d.candidate)
      }
    }
  }
  ws.onerror = () => {
    if (!authed) failOnce('gone')
  }
  ws.onopen = () => ws.send(JSON.stringify({ type: 'open', link }))

  let fps: [string, string] = ['', ''] // sender, browser

  async function onOpen() {
    try {
      fps = [fingerprint(pc!.remoteDescription?.sdp), fingerprint(pc!.localDescription?.sdp)]
    } catch {
      return failOnce('auth')
    }
    if (!remoteCertMatches(pc!, fps[0])) return failOnce('auth')
    dc!.send(JSON.stringify({ type: 'auth', mac: await hmac(payload('browser', session, ...fps)) }))
  }

  async function onMessage(ev: MessageEvent) {
    if (typeof ev.data !== 'string') {
      if (!authed || !current) return
      const chunk = new Uint8Array(ev.data as ArrayBuffer)
      const c = current
      c.writes = c.writes
        .then(() => (c.broken ? undefined : c.onChunk(chunk)))
        .catch((e) => {
          // Saving failed, most often because the download was cancelled
          // in the browser: stop the sender instead of letting it send the
          // rest into nowhere.
          if (c.broken) return
          c.broken = true
          if (current === c) current = undefined
          c.resolve({ error: String(e?.message ?? e), savingFailed: true })
          stopSender()
        })
      return
    }
    const m = JSON.parse(ev.data)
    if (!authed) {
      if (m.type === 'error') return failOnce(m.error)
      if (m.type !== 'auth') return
      // Only the sender has the link's key: its ID is a hash of the public
      // key, and the signature covers this connection's fingerprints.
      let ok = false
      try {
        const pub = b64urlDecode(m.pub)
        ok = linkIDFor(pub) === link && ed25519.verify(b64urlDecode(m.sig), payload('sender', session, ...fps), pub)
      } catch {}
      if (!ok) return failOnce('auth')
      authed = true
      ws.close() // signaling is no longer needed
      return
    }
    switch (m.type) {
      case 'hello':
        h.onHello(m as Hello)
        break
      case 'end': {
        const c = current
        if (!c) return
        current = undefined
        c.writes.then(() => {
          if (!c.broken) c.resolve({ sha256: m.sha256 })
        })
        break
      }
      case 'cancel':
        failOnce('cancel')
        break
      case 'error':
        failOnce(m.error)
        break
    }
  }

  function stopSender() {
    finished = true
    try {
      dc?.send(JSON.stringify({ type: 'cancel' }))
    } catch {}
    setTimeout(() => pc?.close(), 300)
  }

  return {
    get(index: number, onChunk: (b: Uint8Array) => Promise<void>): Promise<GetResult> {
      return new Promise((resolve) => {
        current = { onChunk, writes: Promise.resolve(), resolve, broken: false }
        dc!.send(JSON.stringify({ type: 'get', index }))
      })
    },
    cancel() {
      current?.resolve({ error: 'cancelled' })
      current = undefined
      stopSender()
    },
    finish() {
      finished = true
      try {
        dc?.send(JSON.stringify({ type: 'done' }))
      } catch {}
      setTimeout(() => pc?.close(), 1000)
    },
  }
}
