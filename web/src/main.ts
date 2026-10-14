import { sha256 } from '@noble/hashes/sha2'
import { connect, type Hello } from './link'
import { openSink, type Sink } from './sink'

const view = document.getElementById('view')!

function render(html: string) {
  view.innerHTML = html
}

function esc(s: string) {
  return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]!)
}

function size(n: number) {
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  while (n >= 1000 && i < units.length - 1) {
    n /= 1000
    i++
  }
  return (i === 0 ? n.toFixed(0) : n.toFixed(n < 10 ? 1 : 0)) + ' ' + units[i]
}

// On phones a download should land in Downloads like any other; the save
// dialog is only worth its extra step on a computer.
function isPhone() {
  const uad = (navigator as Navigator & { userAgentData?: { mobile: boolean } }).userAgentData
  return uad ? uad.mobile : /Android|iPhone|iPad|Mobile/i.test(navigator.userAgent)
}

function fail(title: string, detail: string) {
  render(`<h1>${esc(title)}</h1><p class="muted">${esc(detail)}</p>`)
}

const link = location.pathname.split('/').filter(Boolean).pop() ?? ''
const secret = location.hash.slice(1)

if (!/^[A-Z2-7]{16}$/.test(link) || !secret) {
  fail("This link isn't complete", 'Ask the sender for the link again, or scan the QR code once more.')
} else {
  render(`<h1>Connecting…</h1><p class="muted">Reaching the sender's device.</p>`)
  start()
}

async function start() {
  // The service worker streams downloads to disk where the file picker
  // isn't available; register it early so it's ready by the first click.
  if ('serviceWorker' in navigator) {
    navigator.serviceWorker.register('./sw.js', { scope: './' }).catch(() => {})
  }
  try {
    const session = await connect(link, secret, {
      onHello: (h) => showHello(session, h),
      onError: (code) => {
        if (code === 'gone') fail('This link has ended', "The sender stopped sharing, or their device is offline. Ask them to share it again.")
        else if (code === 'used') fail('This link was already used', 'It was for one person only. Ask the sender for a new link.')
        else if (code === 'busy') fail('Too many people right now', 'Try again in a minute.')
        else if (code === 'auth') fail("This link isn't complete", 'Ask the sender for the link again, or scan the QR code once more.')
        else if (code === 'cancel') fail('The sender cancelled', 'Ask them to share the files again.')
        else if (code === 'lost') fail('The connection was lost', 'Open the link again to start over.')
        else fail("Couldn't reach the sender", code)
      },
    })
  } catch (e) {
    fail("Couldn't reach the sender", String((e as Error).message ?? e))
  }
}

function showHello(session: Awaited<ReturnType<typeof connect>>, h: Hello) {
  const from = h.from ? esc(h.from) : 'Someone'
  const count = h.files.length === 1 ? '1 file' : `${h.files.length} files`
  render(`
    <h1>${from} is sending you ${count}</h1>
    <p class="you">You appear to them as <b>${esc(h.name)}</b>.</p>
    <ul class="files">${h.files.map((f) => `<li><span class="name">${esc(f.name)}</span><span class="size">${size(f.size)}</span></li>`).join('')}</ul>
    <button id="dl">Download ${size(h.total)}</button>
  `)
  document.getElementById('dl')!.addEventListener('click', () => download(session, h))
}

// Phones pause pages that leave the screen, which stalls the connection
// until it drops: keep the screen on while downloading, and remember if the
// page went to the background so a failure can say why.
let wakeLock: { release(): Promise<void> } | undefined
let wentAway = false
async function keepAwake(on: boolean) {
  try {
    if (on && !wakeLock) wakeLock = await (navigator as Navigator & { wakeLock?: { request(t: 'screen'): Promise<{ release(): Promise<void> }> } }).wakeLock?.request('screen')
    else if (!on && wakeLock) {
      await wakeLock.release()
      wakeLock = undefined
    }
  } catch {
    // not allowed (e.g. low battery): the note on screen still asks
  }
}
document.addEventListener('visibilitychange', () => {
  if (document.hidden) wentAway = true
  else if (wakeLock === undefined && downloading) keepAwake(true) // the lock is dropped while hidden
})
let downloading = false

async function download(session: Awaited<ReturnType<typeof connect>>, h: Hello) {
  let done = 0
  const started = performance.now()
  downloading = true
  wentAway = false
  keepAwake(true)
  try {
    await downloadAll(session, h, started, (n) => (done += n), () => done)
  } finally {
    downloading = false
    keepAwake(false)
  }
}

async function downloadAll(session: Awaited<ReturnType<typeof connect>>, h: Hello, started: number, add: (n: number) => void, doneSoFar: () => number) {
  const progress = (name: string) => {
    render(`
      <h1>Downloading…</h1>
      <p class="muted small">${esc(name)}</p>
      <div class="bar"><div id="fill"></div></div>
      <p class="muted small" id="stat"></p>
      <p class="small">Keep this page open until the download finishes.</p>
      <button class="secondary" id="stop">Cancel</button>
    `)
    document.getElementById('stop')!.addEventListener('click', () => {
      session.cancel()
      fail('Download cancelled', 'Open the link again to start over.')
    })
  }
  const tick = () => {
    const fill = document.getElementById('fill')
    const stat = document.getElementById('stat')
    if (!fill || !stat) return
    const secs = (performance.now() - started) / 1000
    const done = doneSoFar()
    fill.style.width = `${h.total ? (done / h.total) * 100 : 100}%`
    stat.textContent = `${size(done)} of ${size(h.total)}` + (secs > 1 ? ` · ${size(done / secs)}/s` : '')
  }

  for (let i = 0; i < h.files.length; i++) {
    const f = h.files[i]
    let sink: Sink
    try {
      sink = await openSink(f.name, f.size, h.files.length === 1 && !isPhone())
    } catch (e) {
      if ((e as DOMException).name === 'AbortError') {
        showHello(session, h) // the person closed the save dialog
        return
      }
      fail("Couldn't save the file", String((e as Error).message ?? e))
      return
    }
    progress(f.name)
    const hash = sha256.create()
    let last = 0
    const result = await session.get(i, (chunk) => {
      hash.update(chunk)
      add(chunk.byteLength)
      const now = performance.now()
      if (now - last > 150) {
        last = now
        tick()
      }
      return sink.write(chunk)
    })
    if (result.error) {
      await sink.abort()
      if (result.error !== 'cancelled') {
        fail('The download stopped', wentAway ? 'The page was in the background, and phones pause pages there. Open the link again and keep it on screen until the download finishes.' : result.error)
      }
      return
    }
    const got = Array.from(hash.digest(), (b) => b.toString(16).padStart(2, '0')).join('')
    if (got !== result.sha256) {
      await sink.abort()
      fail('The file arrived damaged', 'It was deleted. Open the link again to download it once more.')
      return
    }
    await sink.close()
  }
  session.finish()
  const secs = (performance.now() - started) / 1000
  render(`
    <h1 class="ok">Done</h1>
    <p>${h.files.length === 1 ? 'The file is' : 'The files are'} in your downloads.</p>
    <p class="muted small">${size(h.total)} in ${secs.toFixed(1)} s · ${size(h.total / Math.max(secs, 0.001))}/s</p>
  `)
}
