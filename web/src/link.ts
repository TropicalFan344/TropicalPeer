// The browser side of share by link: signaling over the server's WebSocket,
// a WebRTC DataChannel to the sender's device, and the key check that keeps
// a server in the middle out (see internal/linkshare).

export type FileInfo = { name: string; size: number }
export type Hello = { name: string; from: string; files: FileInfo[]; total: number }
export type GetResult = { sha256?: string; error?: string }

type Handlers = {
  onHello: (h: Hello) => void
  onError: (code: string) => void
}

const authContext = 'tropicalpeer-link-v1'

function b64urlDecode(s: string): Uint8Array {
  const pad = s.replace(/-/g, '+').replace(/_/g, '/') + '==='.slice((s.length + 3) % 4)
  return Uint8Array.from(atob(pad), (c) => c.charCodeAt(0))
}

function b64urlEncode(b: Uint8Array): string {
  return btoa(String.fromCharCode(...b)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

function fingerprint(sdp: string | undefined): string {
  for (const line of (sdp ?? '').split(/\r?\n/)) {
    const m = line.match(/^a=fingerprint:(\S+) (\S+)/)
    if (m && m[1].toLowerCase() === 'sha-256') return m[2].toUpperCase()
  }
  throw new Error('no DTLS fingerprint')
}

export async function connect(link: string, secret: string, h: Handlers) {
  const key = await crypto.subtle.importKey('raw', b64urlDecode(secret), { name: 'HMAC', hash: 'SHA-256' }, false, ['sign', 'verify'])
  const mac = (role: string, session: string, senderFP: string, browserFP: string) =>
    new TextEncoder().encode([authContext, role, link, session, senderFP, browserFP].join('\n'))

  const ws = new WebSocket((location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/ws')
  let pc: RTCPeerConnection | undefined
  let dc: RTCDataChannel | undefined
  let session = ''
  let finished = false
  let authed = false
  const early: RTCIceCandidateInit[] = []

  // The file being received: chunks go to onChunk, one at a time.
  let current: { onChunk: (b: Uint8Array) => Promise<void>; writes: Promise<void>; resolve: (r: GetResult) => void } | undefined

  const failOnce = (code: string) => {
    if (finished) return
    finished = true
    current?.resolve({ error: code === 'cancel' ? 'The sender cancelled.' : 'The connection was lost.' })
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
      session = m.session
      pc = new RTCPeerConnection({ iceServers: m.ice ?? [] })
      ;(window as unknown as { __tpPC: RTCPeerConnection }).__tpPC = pc // for remote debugging
      dc = pc.createDataChannel('files', { ordered: true })
      dc.binaryType = 'arraybuffer'
      pc.onicecandidate = (e) => e.candidate && signal({ candidate: e.candidate.toJSON() })
      pc.onconnectionstatechange = () => {
        if (pc?.connectionState === 'failed') failOnce("couldn't connect to the sender's device")
      }
      dc.onopen = onOpen
      dc.onmessage = onMessage
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
    const sig = new Uint8Array(await crypto.subtle.sign('HMAC', key, mac('browser', session, ...fps)))
    dc!.send(JSON.stringify({ type: 'auth', mac: b64urlEncode(sig) }))
  }

  async function onMessage(ev: MessageEvent) {
    if (typeof ev.data !== 'string') {
      if (!authed || !current) return
      const chunk = new Uint8Array(ev.data as ArrayBuffer)
      const c = current
      c.writes = c.writes.then(() => c.onChunk(chunk))
      return
    }
    const m = JSON.parse(ev.data)
    if (!authed) {
      if (m.type === 'error') return failOnce(m.error)
      if (m.type !== 'auth') return
      const ok = await crypto.subtle.verify('HMAC', key, b64urlDecode(m.mac), mac('sender', session, ...fps))
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
        c.writes.then(
          () => c.resolve({ sha256: m.sha256 }),
          (e) => c.resolve({ error: String(e?.message ?? e) }),
        )
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

  return {
    get(index: number, onChunk: (b: Uint8Array) => Promise<void>): Promise<GetResult> {
      return new Promise((resolve) => {
        current = { onChunk, writes: Promise.resolve(), resolve }
        dc!.send(JSON.stringify({ type: 'get', index }))
      })
    },
    cancel() {
      finished = true
      current?.resolve({ error: 'cancelled' })
      current = undefined
      try {
        dc?.send(JSON.stringify({ type: 'cancel' }))
      } catch {}
      setTimeout(() => pc?.close(), 300)
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
