// Where a download goes: straight to a file the person picks (Chromium on
// desktop), a stream through the service worker into the browser's
// downloads (most others), or, as a last resort, memory.

export interface Sink {
  write(chunk: Uint8Array): Promise<void>
  close(): Promise<void>
  abort(): Promise<void>
}

type Picker = (o: { suggestedName: string }) => Promise<{ createWritable(): Promise<FileSystemWritableFileStream> }>

export async function openSink(name: string, size: number, allowPicker: boolean): Promise<Sink> {
  const picker = (window as unknown as { showSaveFilePicker?: Picker }).showSaveFilePicker
  if (allowPicker && picker) {
    const handle = await picker({ suggestedName: name })
    const w = await handle.createWritable()
    return {
      write: (c) => w.write(c as Uint8Array<ArrayBuffer>),
      close: () => w.close(),
      abort: () => w.abort(),
    }
  }
  const sw = await serviceWorkerSink(name, size).catch(() => undefined)
  if (sw) return sw
  return memorySink(name)
}

async function serviceWorkerSink(name: string, size: number): Promise<Sink | undefined> {
  if (!('serviceWorker' in navigator)) return undefined
  const reg = await Promise.race([
    navigator.serviceWorker.ready,
    new Promise<undefined>((r) => setTimeout(() => r(undefined), 3000)),
  ])
  if (!reg?.active) return undefined
  const id = Array.from(crypto.getRandomValues(new Uint8Array(12)), (b) => b.toString(16).padStart(2, '0')).join('')
  const ch = new MessageChannel()
  let cancelled = false
  const ready = new Promise<void>((resolve, reject) => {
    const t = setTimeout(() => reject(new Error('service worker not answering')), 3000)
    ch.port1.onmessage = (m) => {
      if (m.data === 'ready') {
        clearTimeout(t)
        resolve()
      } else if (m.data === 'cancelled') {
        cancelled = true
      }
    }
  })
  reg.active.postMessage({ type: 'dl', id, name, size }, [ch.port2])
  await ready
  const frame = document.createElement('iframe')
  frame.hidden = true
  frame.src = './dl/' + id
  document.body.appendChild(frame)
  const cleanup = () => setTimeout(() => frame.remove(), 60_000)
  return {
    async write(c) {
      if (cancelled) throw new Error('The download was cancelled in the browser.')
      const buf = c.byteOffset === 0 && c.byteLength === c.buffer.byteLength ? c.buffer : c.slice().buffer
      ch.port1.postMessage(buf, [buf as ArrayBuffer])
    },
    async close() {
      ch.port1.postMessage('end')
      cleanup()
    },
    async abort() {
      ch.port1.postMessage('abort')
      cleanup()
    },
  }
}

function memorySink(name: string): Sink {
  const parts: Uint8Array[] = []
  return {
    async write(c) {
      parts.push(c.slice())
    },
    async close() {
      const url = URL.createObjectURL(new Blob(parts as BlobPart[]))
      const a = document.createElement('a')
      a.href = url
      a.download = name
      a.click()
      setTimeout(() => URL.revokeObjectURL(url), 60_000)
    },
    async abort() {
      parts.length = 0
    },
  }
}
