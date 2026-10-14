// Streams a download from the page to disk: the page posts chunks on a
// MessagePort, and a navigation to /r/dl/<id> answers with a stream of them.
const pending = new Map()

self.addEventListener('install', () => self.skipWaiting())
self.addEventListener('activate', (e) => e.waitUntil(self.clients.claim()))

self.addEventListener('message', (e) => {
  const d = e.data
  if (!d || d.type !== 'dl') return
  const port = e.ports[0]
  const stream = new ReadableStream({
    start(ctrl) {
      port.onmessage = (m) => {
        const v = m.data
        if (v === 'end') ctrl.close()
        else if (v === 'abort') ctrl.error(new Error('aborted'))
        else ctrl.enqueue(new Uint8Array(v))
      }
    },
    cancel() { port.postMessage('cancelled') },
  })
  pending.set(d.id, { stream, name: d.name, size: d.size })
  port.postMessage('ready')
})

self.addEventListener('fetch', (e) => {
  const url = new URL(e.request.url)
  const m = url.pathname.match(/\/r\/dl\/([A-Za-z0-9]+)$/)
  if (!m) return
  const dl = pending.get(m[1])
  if (!dl) return
  pending.delete(m[1])
  const headers = {
    'Content-Type': 'application/octet-stream',
    'Content-Disposition': "attachment; filename*=UTF-8''" + encodeURIComponent(dl.name),
    'X-Content-Type-Options': 'nosniff',
  }
  if (dl.size >= 0) headers['Content-Length'] = String(dl.size)
  e.respondWith(new Response(dl.stream, { headers }))
})
