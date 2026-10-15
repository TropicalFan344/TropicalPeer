import { sha256 } from '@noble/hashes/sha2'
import { connect, type Hello } from './link'
import { openSink, type Sink } from './sink'
import { initI18n, t } from './i18n'
import { bed } from './bed'
import '@fontsource-variable/jost'

const view = document.getElementById('view')!
const announce = document.getElementById('announce')!

// Screen readers hear each new state's heading once, not every progress tick.
function render(html: string) {
  view.innerHTML = html
  announce.textContent = view.querySelector('h1')?.textContent ?? ''
}

function esc(s: string) {
  return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]!)
}

// Sizes read the same as in the app (frontend/src/lib/format.ts).
function size(bytes: number) {
  if (bytes < 1024) return `${Math.round(bytes)} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = bytes / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v >= 100 || Number.isInteger(v) ? Math.round(v) : v.toFixed(1)} ${units[i]}`
}

// Lucide's shield-check, for the line that says how the files travel.
const shield = `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z"/><path d="m9 12 2 2 4-4"/></svg>`

// On phones a download should land in Downloads like any other; the save
// dialog is only worth its extra step on a computer.
function isPhone() {
  const uad = (navigator as Navigator & { userAgentData?: { mobile: boolean } }).userAgentData
  return uad ? uad.mobile : /Android|iPhone|iPad|Mobile/i.test(navigator.userAgent)
}

function fail(title: string, detail: string) {
  render(`<h1>${esc(title)}</h1><p class="muted">${esc(detail)}</p>`)
}

/** A failure the page knows by code: gone, used, busy, auth, cancel, lost, unreachable. */
function failWith(code: string) {
  const known = ['gone', 'used', 'busy', 'auth', 'cancel', 'lost', 'unreachable', 'unreadable']
  const c = known.includes(code) ? code : 'unreachable'
  fail(t(c + 'Title'), t(c + 'Body'))
}

const link = location.pathname.split('/').filter(Boolean).pop() ?? ''
const secret = location.hash.slice(1)

initI18n().then(() => {
  if (!/^[A-Z2-7]{16}$/.test(link) || !secret) {
    failWith('auth')
  } else {
    render(`<h1>${esc(t('connectingTitle'))}</h1><p class="muted">${esc(t('connectingBody'))}</p>`)
    start()
  }
})

async function start() {
  // The service worker streams downloads to disk where the file picker
  // isn't available; register it early so it's ready by the first click.
  if ('serviceWorker' in navigator) {
    navigator.serviceWorker.register('./sw.js', { scope: './' }).catch(() => {})
  }
  try {
    const session = await connect(link, secret, {
      onHello: (h) => showHello(session, h),
      onError: failWith,
    })
  } catch {
    failWith('unreachable')
  }
}

function showHello(session: Awaited<ReturnType<typeof connect>>, h: Hello) {
  const files = t('files', { count: h.files.length })
  const title = t('sending', { from: h.from || t('someone'), files })
  const you = esc(t('youAppear', { name: '\u0000' })).replace('\u0000', `<b>${esc(h.name)}</b>`)
  render(`
    <div class="sender">${bed(h.from || '?', 52)}<h1>${esc(title)}</h1></div>
    <p class="you">${you}</p>
    <ul class="files">${h.files.map((f) => `<li><span class="name">${esc(f.name)}</span><span class="size">${size(f.size)}</span></li>`).join('')}</ul>
    <button id="dl">${esc(t('download', { size: size(h.total) }))}</button>
    <p class="direct">${shield}<span>${esc(t('direct'))}</span></p>
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
  const pct = () => (h.total ? (doneSoFar() / h.total) * 100 : 100)
  const progress = (i: number) => {
    const name = h.files[i].name
    const which = h.files.length > 1 ? t('fileOf', { n: i + 1, count: h.files.length }) + ' · ' : ''
    const p = pct()
    render(`
      <h1>${esc(t('downloading'))}</h1>
      <p class="muted small">${esc(which + name)}</p>
      <div class="wave-track" id="bar" role="progressbar" aria-label="${esc(t('downloading'))}" aria-valuemin="0" aria-valuemax="100" aria-valuenow="${Math.round(p)}">
        <div class="wave-laid-length" id="fill" style="clip-path: inset(0 ${100 - p}% 0 0)"><div class="wave-fill"></div></div>
      </div>
      <p class="muted small" id="stat"></p>
      <p class="small">${esc(t('keepOpen'))}</p>
      <button class="quiet" id="stop">${esc(t('cancel'))}</button>
    `)
    document.getElementById('stop')!.addEventListener('click', () => {
      session.cancel()
      fail(t('cancelledTitle'), t('cancelledBody'))
    })
  }
  const tick = () => {
    const fill = document.getElementById('fill')
    const bar = document.getElementById('bar')
    const stat = document.getElementById('stat')
    if (!fill || !bar || !stat) return
    const secs = (performance.now() - started) / 1000
    const done = doneSoFar()
    const p = pct()
    fill.style.clipPath = `inset(0 ${100 - p}% 0 0)`
    bar.setAttribute('aria-valuenow', String(Math.round(p)))
    stat.textContent = t('progress', { done: size(done), total: size(h.total) }) + (secs > 1 ? ' · ' + t('perSecond', { speed: size(done / secs) }) : '')
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
      fail(t('saveFailedTitle'), t('saveFailedBody', { error: String((e as Error).message ?? e) }))
      return
    }
    progress(i)
    tick()
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
    if (result.savingFailed) {
      await sink.abort().catch(() => {})
      fail(t('cancelledTitle'), t('browserCancelledBody'))
      return
    }
    if (result.error) {
      await sink.abort()
      if (result.error === 'cancel' || result.error === 'unreadable') failWith(result.error)
      else if (result.error !== 'cancelled') fail(t('stoppedTitle'), t(wentAway ? 'backgroundBody' : 'lostBody'))
      return
    }
    const got = Array.from(hash.digest(), (b) => b.toString(16).padStart(2, '0')).join('')
    if (got !== result.sha256) {
      await sink.abort()
      fail(t('damagedTitle'), t('damagedBody'))
      return
    }
    await sink.close()
  }
  session.finish()
  const secs = (performance.now() - started) / 1000
  render(`
    <h1 class="ok">${esc(t('doneTitle'))}</h1>
    <p>${esc(t('doneBody', { count: h.files.length }))}</p>
    <p class="muted small">${esc(t('doneStats', { size: size(h.total), secs: secs.toFixed(1), speed: size(h.total / Math.max(secs, 0.001)) }))}</p>
  `)
}
