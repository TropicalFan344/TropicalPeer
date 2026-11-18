import { sha256 } from '@noble/hashes/sha2'
import { connect, maxReplyFiles, offerFits, type Hello } from './link'
import { openSink, type Sink } from './sink'
import { initI18n, t } from './i18n'
import { bed } from './bed'
import '@fontsource-variable/jost'

const view = document.getElementById('view')!
const announce = document.getElementById('announce')!

// Screen readers hear each new state's heading and first line once, not every
// progress tick, and keyboard focus moves to the new state.
function render(html: string) {
  atHome = false
  dropInto = undefined
  view.innerHTML = html
  const heading = view.querySelector('h1')
  announce.textContent = [heading?.textContent, view.querySelector('p')?.textContent].filter(Boolean).join('. ')
  heading?.setAttribute('tabindex', '-1')
  heading?.focus({ preventScroll: true })
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
      onError: (code) => {
        connectionGone = true
        if (onGone) onGone()
        else failWith(code)
      },
      onReplies: (on) => {
        if (hello) hello.reply = on
        if (atHome && !connectionGone) homeScreen()
      },
    })
  } catch {
    failWith('unreachable')
  }
}

type Session = Awaited<ReturnType<typeof connect>>

// ---- Sending files to the link's owner ----

const senderName = (h: Hello) => h.from || t('someone')

// After a finished download the connection only serves for sending files
// back: losing it takes that away, but the download still succeeded.
let connectionGone = false
let onGone: (() => void) | undefined

let hello: Hello | undefined
// The screen the person comes back to, and whether it's showing now.
let homeScreen = () => {}
let atHome = false
// The files picked so far, and what they wrote: both survive a failed try.
let chosen: File[] = []
let visitorWords = ''
let sendingBack = false
let dropInto: ((files: File[]) => void) | undefined

window.addEventListener('beforeunload', (e) => {
  if (sendingBack) e.preventDefault()
})

// Files can be dropped anywhere on a screen that takes them.
const dragsFiles = (e: DragEvent) => !!dropInto && !!e.dataTransfer?.types.includes('Files')
window.addEventListener('dragover', (e) => {
  if (!dragsFiles(e)) return
  e.preventDefault()
  document.body.classList.add('dropping')
})
window.addEventListener('dragleave', (e) => {
  if (!e.relatedTarget) document.body.classList.remove('dropping')
})
window.addEventListener('drop', (e) => {
  document.body.classList.remove('dropping')
  if (!dragsFiles(e)) return
  e.preventDefault()
  const files = Array.from(e.dataTransfer?.files ?? [])
  if (files.length) dropInto?.(files)
})

const canSend = (h: Hello) => !!h.reply && !connectionGone
const picker = '<input type="file" id="pick" multiple hidden>'

function addFiles(files: File[]) {
  const key = (f: File) => `${f.name}/${f.size}/${f.lastModified}`
  const have = new Set(chosen.map(key))
  chosen = chosen.concat(files.filter((f) => !have.has(key(f))))
}

// Wires a button to the file picker on this screen.
function wirePicker(buttonId: string, picked: () => void) {
  const button = document.getElementById(buttonId)
  const pick = document.getElementById('pick') as HTMLInputElement | null
  if (!button || !pick) return
  button.addEventListener('click', () => pick.click())
  pick.addEventListener('change', () => {
    addFiles(Array.from(pick.files ?? []))
    pick.value = ''
    if (chosen.length) picked()
  })
}

function replyControls(h: Hello) {
  if (!canSend(h)) return ''
  return `<button class="secondary" id="reply">${esc(t('replyButton', { from: senderName(h) }))}</button>
    <p class="hint">${esc(t('replyHint'))}</p>${picker}`
}

// Call after drawing a screen people come back to.
function enterHome(session: Session, h: Hello, screen: () => void, button = 'reply') {
  homeScreen = screen
  atHome = true
  if (!canSend(h)) return
  wirePicker(button, () => showReview(session, h))
  dropInto = (files) => {
    addFiles(files)
    showReview(session, h)
  }
}

const fileRows = (files: File[]) => files.map((f) => `<li><span class="name">${esc(f.name)}</span><span class="size">${size(f.size)}</span></li>`).join('')

// Lucide's x and plus, for the review list.
const xIcon = `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M18 6 6 18"/><path d="m6 6 12 12"/></svg>`
const plusIcon = `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M5 12h14"/><path d="M12 5v14"/></svg>`

// One look at what is about to be offered, before anyone is bothered.
function showReview(session: Session, h: Hello) {
  const from = senderName(h)
  const total = chosen.reduce((n, f) => n + f.size, 0)
  const fits = offerFits(chosen)
  render(`
    <h1>${esc(t('replyReviewTitle', { from }))}</h1>
    <p class="muted">${esc(t('replyReviewBody'))}</p>
    <ul class="files">${chosen
      .map(
        (f, i) => `<li><span class="name">${esc(f.name)}</span><span class="size">${size(f.size)}</span>
          <button class="remove" data-i="${i}" aria-label="${esc(t('replyRemove', { name: f.name }))}">${xIcon}</button></li>`,
      )
      .join('')}</ul>
    <div class="under-list">
      <button class="quiet" id="add">${plusIcon}<span>${esc(t('replyAddFiles'))}</span></button>
      <span class="muted small">${esc(t('files', { count: chosen.length }))} · ${size(total)}</span>
    </div>
    ${picker}
    <label class="field"><span>${esc(t('replyNoteLabel'))}</span>
      <input id="note" type="text" maxlength="200" autocomplete="name" value="${esc(visitorWords)}"></label>
    ${fits ? '' : `<p class="bad small" role="alert">${esc(t('replyTooMany', { max: maxReplyFiles }))}</p>`}
    <button id="ask"${fits ? '' : ' disabled'}>${esc(t('replyAsk', { from }))}</button>
    <button class="quiet" id="back">${esc(t('back'))}</button>
  `)
  const note = document.getElementById('note') as HTMLInputElement
  const keepWords = () => (visitorWords = note.value)
  view.querySelectorAll<HTMLButtonElement>('.remove').forEach((b) =>
    b.addEventListener('click', () => {
      keepWords()
      chosen.splice(Number(b.dataset.i), 1)
      if (chosen.length) showReview(session, h)
      else homeScreen()
    }),
  )
  wirePicker('add', () => {
    keepWords()
    showReview(session, h)
  })
  dropInto = (files) => {
    keepWords()
    addFiles(files)
    showReview(session, h)
  }
  document.getElementById('back')!.addEventListener('click', () => {
    keepWords()
    homeScreen()
  })
  document.getElementById('ask')!.addEventListener('click', () => {
    keepWords()
    sendBack(session, h)
  })
}

type Action = { label: string; run: () => void }

// How a try ended: the first action is the way forward.
function replyEnded(title: string, body: string, actions: Action[], ok = false) {
  const buttons = actions.map((a, i) => `<button class="${i ? 'quiet' : ''}" data-a="${i}">${esc(a.label)}</button>`).join('')
  render(`<h1${ok ? ' class="ok"' : ''}>${esc(title)}</h1><p>${esc(body)}</p>${buttons}${picker}`)
  view.querySelectorAll<HTMLButtonElement>('button[data-a]').forEach((b) => b.addEventListener('click', () => actions[Number(b.dataset.a)].run()))
}

const clock = (secs: number) => `${Math.floor(secs / 60)}:${String(secs % 60).padStart(2, '0')}`

async function sendBack(session: Session, h: Hello) {
  const from = senderName(h)
  const files = chosen.slice()
  const total = files.reduce((n, f) => n + f.size, 0)
  const cancel = () => session.cancelSend()
  render(`
    <h1>${esc(t('replyWaitingTitle', { from }))}</h1>
    <p class="muted">${esc(t('replyWaitingBody'))}</p>
    <div class="wave-track waiting" aria-hidden="true"><div class="wave-laid-length"><div class="wave-fill"></div></div></div>
    <p class="muted small" id="left" role="timer"></p>
    <ul class="files">${fileRows(files)}</ul>
    <button class="quiet" id="stop">${esc(t('cancel'))}</button>
  `)
  document.getElementById('stop')!.addEventListener('click', cancel)

  let countdown: ReturnType<typeof setInterval> | undefined
  const started = performance.now()
  let current = 0
  let last = 0
  const tick = (sent: number) => {
    const fill = document.getElementById('fill')
    const bar = document.getElementById('bar')
    const stat = document.getElementById('stat')
    if (!fill || !bar || !stat) return
    const p = total ? (sent / total) * 100 : 100
    const secs = (performance.now() - started) / 1000
    fill.style.clipPath = `inset(0 ${100 - p}% 0 0)`
    bar.setAttribute('aria-valuenow', String(Math.round(p)))
    stat.textContent = t('progress', { done: size(sent), total: size(total) }) + (secs > 1 ? ' · ' + t('perSecond', { speed: size(sent / secs) }) : '')
  }
  const showFile = () => {
    const which = files.length > 1 ? t('fileOf', { n: current + 1, count: files.length }) + ' · ' : ''
    const line = document.getElementById('which')
    if (line) line.textContent = which + files[current].name
  }

  sendingBack = true
  downloading = true
  wentAway = false
  keepAwake(true)
  const whenGone = onGone
  onGone = undefined // while sending, a lost connection is the news
  const { code, saved } = await session
    .sendFiles(files, visitorWords, {
      asking(seconds) {
        const line = document.getElementById('left')
        if (!line) return
        if (!seconds) {
          line.textContent = t('replyWaitingNoLimit')
          return
        }
        const until = performance.now() + seconds * 1000
        const show = () => (line.textContent = t('replyWaitingLeft', { time: clock(Math.max(0, Math.ceil((until - performance.now()) / 1000))) }))
        show()
        countdown = setInterval(show, 1000)
      },
      accepted() {
        clearInterval(countdown)
        render(`
          <h1>${esc(t('replySendingTitle'))}</h1>
          <p class="muted small" id="which"></p>
          <div class="wave-track" id="bar" role="progressbar" aria-label="${esc(t('replySendingTitle'))}" aria-valuemin="0" aria-valuemax="100" aria-valuenow="0">
            <div class="wave-laid-length" id="fill" style="clip-path: inset(0 100% 0 0)"><div class="wave-fill"></div></div>
          </div>
          <p class="muted small" id="stat"></p>
          <p class="small">${esc(t('replyKeepOpen'))}</p>
          <button class="quiet" id="stop">${esc(t('cancel'))}</button>
        `)
        document.getElementById('stop')!.addEventListener('click', cancel)
        showFile()
      },
      file(i) {
        current = i
        showFile()
      },
      progress(sent) {
        const now = performance.now()
        if (now - last > 150) {
          last = now
          tick(sent)
        }
      },
    })
    .finally(() => {
      clearInterval(countdown)
      sendingBack = false
      downloading = false
      keepAwake(false)
      onGone = whenGone
    })

  // What arrived doesn't need sending again.
  chosen = chosen.filter((f) => !files.slice(0, saved).includes(f))
  showSendResult(session, h, code, saved, files.length)
}

function showSendResult(session: Session, h: Hello, code: string, saved: number, count: number) {
  const from = senderName(h)
  const back: Action = { label: t('back'), run: () => homeScreen() }
  const change: Action = { label: t('replyChangeFiles'), run: () => showReview(session, h) }
  const again: Action = { label: t('replyAskAgain'), run: () => sendBack(session, h) }
  const title = saved ? t('replyPartialTitle', { saved, count }) : t('replyNotSentTitle')
  switch (code) {
    case '': {
      const more: Action = { label: t('replySendMore'), run: () => document.getElementById('pick')?.click() }
      replyEnded(t('replyDoneTitle'), t('replyDoneBody', { count, from }), h.ask ? [more] : [more, back], true)
      const pick = document.getElementById('pick') as HTMLInputElement
      pick.addEventListener('change', () => {
        addFiles(Array.from(pick.files ?? []))
        if (chosen.length) showReview(session, h)
      })
      dropInto = (files) => {
        addFiles(files)
        showReview(session, h)
      }
      return
    }
    case 'cancelled':
      return chosen.length ? showReview(session, h) : homeScreen()
    case 'noanswer':
      return replyEnded(t('replyNoAnswerTitle', { from }), t('replyNoAnswerBody'), [again, back])
    case 'busy':
      return replyEnded(title, t('replyBusy', { from }), [again, back])
    case 'declined':
      return replyEnded(title, t('replyDeclined', { from }), [change, back])
    case 'off':
    case 'limit':
      // Asking again can't work, so the way to it goes too.
      h.reply = false
      chosen = []
      return replyEnded(title, t(code === 'off' ? 'replyOff' : 'replyLimit', { from }), h.ask ? [] : [back])
    case 'lost':
      if (wentAway) fail(t('replyStoppedTitle'), t('replyBackgroundBody'))
      return // otherwise the page already says the connection was lost
  }
  const bodies: Record<string, string> = {
    space: 'replySpace',
    invalid: 'replyInvalid',
    stopped: 'replyStopped',
    disk: 'replyDisk',
    damaged: 'replyDamaged',
    unreadable: 'replyUnreadable',
  }
  replyEnded(title, t(bodies[code] ?? 'replyDamaged', { from }), [change, back])
}

const youAppear = (h: Hello) => esc(t('youAppear', { name: '\u0000' })).replace('\u0000', `<b>${esc(h.name)}</b>`)

// A link that only asks for files: choosing them is the whole page.
function showAsk(session: Session, h: Hello) {
  const from = senderName(h)
  render(`
    <div class="sender">${bed(h.from || '?', 52)}<h1>${esc(t('askTitle', { from }))}</h1></div>
    ${h.purpose ? `<p class="purpose">${esc(h.purpose)}</p>` : ''}
    <p class="you">${youAppear(h)}</p>
    <button id="choose">${esc(t('askChoose'))}</button>${picker}
    ${isPhone() ? '' : `<p class="hint">${esc(t('askDrop'))}</p>`}
    <p class="direct">${shield}<span>${esc(t('directUp'))}</span></p>
  `)
  enterHome(session, h, () => showAsk(session, h), 'choose')
}

function showHello(session: Session, h: Hello) {
  hello = h
  if (h.ask) return showAsk(session, h)
  const files = t('files', { count: h.files.length })
  const title = t('sending', { from: h.from || t('someone'), files })
  render(`
    <div class="sender">${bed(h.from || '?', 52)}<h1>${esc(title)}</h1></div>
    <p class="you">${youAppear(h)}</p>
    <ul class="files">${h.files.map((f) => `<li><span class="name">${esc(f.name)}</span><span class="size">${size(f.size)}</span></li>`).join('')}</ul>
    <button id="dl">${esc(t('download', { size: size(h.total) }))}</button>
    ${replyControls(h)}
    <p class="direct">${shield}<span>${esc(t('direct'))}</span></p>
  `)
  document.getElementById('dl')!.addEventListener('click', () => download(session, h))
  enterHome(session, h, () => showHello(session, h))
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
  // With replies on the connection stays up, for sending files back.
  if (h.reply) session.downloaded()
  else session.finish()
  const secs = (performance.now() - started) / 1000
  const stats = t('doneStats', { size: size(h.total), secs: secs.toFixed(1), speed: size(h.total / Math.max(secs, 0.001)) })
  const showDone = () => {
    render(`
      <h1 class="ok">${esc(t('doneTitle'))}</h1>
      <p>${esc(t('doneBody', { count: h.files.length }))}</p>
      <p class="muted small">${esc(stats)}</p>
      ${replyControls(h)}
    `)
    enterHome(session, h, showDone)
  }
  onGone = showDone
  showDone()
}
