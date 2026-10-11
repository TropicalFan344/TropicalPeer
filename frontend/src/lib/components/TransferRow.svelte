<script lang="ts">
  import { ArrowUpRight, ArrowDownLeft, X, RotateCcw, FolderOpen, FolderSearch, FileSearch, Route, Zap, Pause, Play } from '@lucide/svelte'
  import Bed from './Bed.svelte'
  import WaveBand from './WaveBand.svelte'
  import { api, errorText, isActive, isAndroid, type Transfer } from '../api'
  import { openFolder, showFile, toast } from '../state.svelte'
  import { formatSize } from '../format'
  import { t as tr, what } from '../i18n.svelte'

  let { transfer: t }: { transfer: Transfer } = $props()

  const sending = $derived(t.direction === 'send')
  const active = $derived(isActive(t))
  const paused = $derived(t.status === 'paused')
  let asking = $state(false) // resuming an incoming one asks the sender, which takes a moment
  const waiting = $derived(t.status === 'connecting' || t.status === 'waiting')
  const title = $derived(what(t.files))
  const current = $derived(
    t.files.length > 1 && (t.status === 'sending' || t.status === 'receiving') ? t.files[t.fileIndex]?.name : '',
  )

  function eta(): string {
    if (t.speed <= 0) return ''
    const s = Math.max(0, Math.round((t.total - t.done) / t.speed))
    if (s < 60) return tr('transfer.secondsLeft', { s })
    if (s < 3600) return tr('transfer.minutesLeft', { m: Math.floor(s / 60), s: s % 60 })
    return tr('transfer.hoursLeft', { h: Math.floor(s / 3600), m: Math.floor((s % 3600) / 60) })
  }

  const status = $derived.by(() => {
    const progress = [
      tr('transfer.progress', { done: formatSize(t.done), total: formatSize(t.total) }),
      t.speed > 0 ? tr('transfer.perSecond', { speed: formatSize(t.speed) }) : '',
      eta(),
    ]
      .filter(Boolean)
      .join(' · ')
    switch (t.status) {
      case 'connecting':
        return t.resuming
          ? `${tr('transfer.reconnecting', { name: t.peerName })} · ${tr('transfer.progress', { done: formatSize(t.done), total: formatSize(t.total) })}`
          : tr('transfer.findingPath', { name: t.peerName })
      case 'waiting':
        return tr('transfer.waitingAccept', { name: t.peerName })
      case 'sending':
      case 'receiving':
        return progress
      case 'done':
        return tr(sending ? 'transfer.sentTo' : 'transfer.from', { name: t.peerName, size: formatSize(t.total) })
      case 'declined':
        return t.error ? tr('transfer.declinedReason', { name: t.peerName, reason: t.error }) : tr('transfer.declined', { name: t.peerName })
      case 'cancelled':
        return tr('transfer.cancelled')
      case 'paused':
        return `${tr(t.pausedByPeer ? 'transfer.pausedBy' : 'transfer.paused', { name: t.peerName })} · ${tr('transfer.progress', { done: formatSize(t.done), total: formatSize(t.total) })}`
      case 'failed':
        return t.error || tr('transfer.failed')
    }
  })

  const statusTone = $derived(
    t.status === 'failed' || t.status === 'declined' ? 'text-bad' : t.status === 'done' ? 'text-ok' : 'text-muted',
  )

  async function retry() {
    try {
      await api.retryTransfer(t.id)
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  async function resume() {
    asking = true
    try {
      await api.resumeTransfer(t.id)
    } catch (e) {
      toast(errorText(e), 'error', 6000)
    } finally {
      asking = false
    }
  }
</script>

<!-- Phone: actions drop to their own line under the status, lined up with the text. -->
<div class="flex items-center gap-4 px-5 py-4 max-sm:flex-wrap max-sm:gap-x-3.5 max-sm:gap-y-2 max-sm:px-4">
  <Bed name={t.peerName} id={t.peerId} size={40} />

  <div class="min-w-0 flex-1">
    <div class="flex items-center gap-2">
      {#if sending}
        <ArrowUpRight size={16} class="shrink-0 text-primary" aria-label={tr('transfer.sent')} />
      {:else}
        <ArrowDownLeft size={16} class="shrink-0 text-sea" aria-label={tr('transfer.received')} />
      {/if}
      <span class="truncate font-medium" title={t.files.map((f) => f.name).join('\n')}>{title}</span>
    </div>

    {#if active || paused}
      <div class="mt-2">
        <WaveBand
          value={t.total > 0 ? t.done / t.total : 0}
          waiting={waiting && !paused}
          flowing={t.status === 'sending' || t.status === 'receiving'}
          label={tr(sending ? 'transfer.sendingLabel' : 'transfer.receivingLabel', { title })}
        />
      </div>
    {/if}

    <!-- Phone: the status wraps rather than losing its numbers to an ellipsis. -->
    <div class="mt-1.5 flex items-center gap-3 text-sm max-sm:flex-wrap max-sm:gap-y-0.5">
      <span class="truncate {statusTone} max-sm:overflow-visible max-sm:whitespace-normal">{status}</span>
      {#if t.upgraded && !t.relayed && t.status !== 'failed'}
        <span
          class="flex shrink-0 items-center gap-1 text-muted"
          title={tr('transfer.directTip')}
        >
          <Zap size={13} strokeWidth={1.75} class="text-ok" />
          {tr(active ? 'transfer.nowDirect' : 'transfer.wentDirect')}
        </span>
      {:else if t.relayed && t.status !== 'failed' && t.status !== 'declined'}
        <span
          class="flex shrink-0 items-center gap-1 text-muted"
          title={tr('transfer.relayedTip')}
        >
          <Route size={13} strokeWidth={1.75} /> {tr('transfer.relayed')}
        </span>
      {/if}
      {#if current}<span class="ml-auto truncate text-muted">{current}</span>{/if}
    </div>
  </div>

  <div class="flex shrink-0 items-center gap-1 max-sm:w-full max-sm:pl-11 max-sm:empty:hidden">
    {#if active}
      <button class="btn-quiet" aria-label={tr('transfer.pauseLabel', { title })} onclick={() => api.pauseTransfer(t.id)}>
        <Pause size={16} /> {tr('transfer.pause')}
      </button>
      <button class="btn-quiet" aria-label={tr('transfer.cancelLabel', { title })} onclick={() => api.cancelTransfer(t.id)}>
        <X size={16} /> {tr('common.cancel')}
      </button>
    {:else if paused}
      <button class="btn-quiet" disabled={asking} onclick={resume}>
        <Play size={16} /> {tr('transfer.resume')}
      </button>
      <button class="btn-quiet" aria-label={tr('transfer.cancelLabel', { title })} onclick={() => api.cancelTransfer(t.id)}>
        <X size={16} /> {tr('common.cancel')}
      </button>
    {:else if !sending && t.status === 'done' && t.saved?.length}
      {#if t.saved.length === 1}
        <button class="btn-quiet" onclick={() => showFile(t.saved[0]!)}>
          <FileSearch size={16} /> {tr(isAndroid ? 'transfer.open' : 'transfer.showFile')}
        </button>
      {/if}
      <!-- Android: the folder the user chose for received files (Settings), shown in Files. -->
      <button class="btn-quiet" onclick={() => openFolder(t.saveDir)}>
        {#if isAndroid}
          <FolderSearch size={16} /> {tr('transfer.openLocation')}
        {:else}
          <FolderOpen size={16} /> {tr('transfer.openFolder')}
        {/if}
      </button>
    {:else if sending && t.status !== 'done'}
      <!-- Some of it arrived: sending again picks up from there. -->
      <button class="btn-quiet" onclick={retry}><RotateCcw size={16} /> {tr(t.done > 0 ? 'transfer.resume' : 'transfer.retry')}</button>
    {/if}
  </div>
</div>
