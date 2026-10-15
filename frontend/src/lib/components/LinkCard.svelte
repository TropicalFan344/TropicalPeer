<script lang="ts">
  // One shared link, answering the sender's questions in the order they ask
  // them: what is this, is anyone using it, how do I hand it over. The QR is
  // the handover for someone in the room, so it stays beside the card on a
  // computer and opens on demand on a phone, where Share is the usual way.
  import { Copy, Check, X, User, Users, Share2, QrCode as QrIcon, Unlock } from '@lucide/svelte'
  import { slide } from 'svelte/transition'
  import QrCode from './QrCode.svelte'
  import Bed from './Bed.svelte'
  import WaveBand from './WaveBand.svelte'
  import Dialog from './Dialog.svelte'
  import { api, errorText, isAndroid, type Link, type Visitor } from '../api'
  import { stopLink, toast, visitorActive } from '../state.svelte'
  import { formatSize } from '../format'
  import { t, what } from '../i18n.svelte'
  import { dur, easeOutExpo } from '../motion'

  let { link }: { link: Link } = $props()

  const title = $derived(what(link.files))
  const names = $derived(link.files.map((f) => f.name).join(', '))

  // A one-person link is spent once its person has the files, and held while
  // someone has it open.
  const doneBy = $derived(link.visitors.find((v) => v.status === 'done'))
  const used = $derived(!link.unlimited && !!doneBy)
  const holder = $derived(link.unlimited ? undefined : link.visitors.find(visitorActive))
  const live = $derived(!link.ended && !used)

  const count = (s: (v: Visitor) => boolean) => link.visitors.filter(s).length
  const line = $derived.by((): { text: string; tone: string } => {
    if (link.ended) return { text: link.ended, tone: 'text-bad' }
    if (link.offline) return { text: t('links.reconnecting'), tone: 'text-warn' }
    if (used) return { text: t('links.usedBy', { name: doneBy!.name }), tone: 'text-muted' }
    if (holder) return { text: t('links.heldBy', { name: holder.name }), tone: 'text-muted' }
    if (!link.unlimited || !link.visitors.length) return { text: t('links.waitingForSomeone'), tone: 'text-muted' }
    const parts = [
      [count((v) => v.status === 'done'), 'links.countDone'],
      [count((v) => v.status === 'sending'), 'links.countSending'],
      [count((v) => v.status === 'waiting' || v.status === 'connecting'), 'links.countOpen'],
    ] as const
    const text = parts
      .filter(([n]) => n > 0)
      .map(([n, key]) => t(key, { count: n }))
      .join(' · ')
    return { text: text || t('links.waitingForSomeone'), tone: 'text-muted' }
  })

  let copied = $state(false)
  async function copy() {
    await navigator.clipboard.writeText(link.url)
    copied = true
    setTimeout(() => (copied = false), 1500)
  }

  // On a phone the link usually goes out through a chat app.
  async function share() {
    try {
      await api.shareText(link.url, title)
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  let showQr = $state(false)

  // Stopping cuts off anyone mid-download, so that case asks first.
  let confirmStop = $state(false)
  function stop() {
    if (!link.ended && link.visitors.some((v) => v.status === 'sending')) confirmStop = true
    else stopLink(link.id)
  }

  function status(v: Visitor): string {
    switch (v.status) {
      case 'connecting':
        return t('links.visitorConnecting')
      case 'waiting':
        return t('links.visitorWaiting')
      case 'sending':
        return [
          t('transfer.progress', { done: formatSize(v.done), total: formatSize(v.total) }),
          v.speed > 0 ? t('transfer.perSecond', { speed: formatSize(v.speed) }) : '',
        ]
          .filter(Boolean)
          .join(' · ')
      case 'done':
        return t('links.visitorDone', { size: formatSize(v.total) })
      case 'refused':
        return t('links.visitorRefused')
      case 'cancelled':
        return v.error || t('transfer.cancelled')
      default:
        return v.error || t('transfer.failed')
    }
  }

  // Someone closing a tab is not a fault, so only a finished download is
  // coloured; everything else stays muted.
  const tone = (v: Visitor) => (v.status === 'done' ? 'text-ok' : 'text-muted')

  // A busy "anyone" link collects visitors; past the people still here, the
  // last few finished ones are enough unless asked for.
  const KEEP = 3
  let showAll = $state(false)
  const finished = $derived(link.visitors.filter((v) => !visitorActive(v)))
  const hidden = $derived(showAll ? 0 : Math.max(0, finished.length - KEEP))
  const shown = $derived(hidden ? link.visitors.filter((v) => visitorActive(v) || finished.indexOf(v) >= hidden) : link.visitors)
</script>

<article class="overflow-hidden rounded-lg border border-line bg-surface">
  <div class="flex gap-5 p-4">
    {#if !isAndroid}
      <!-- Pale stone in both themes, with room around the code: scanners need it. -->
      <div
        class="aspect-square w-[132px] shrink-0 self-start rounded-md bg-[#fbfcf9] p-1.5 ring-1 ring-line transition-opacity duration-200 max-sm:hidden
          {live ? '' : 'opacity-30'}"
      >
        <QrCode rows={link.qr} label={t('links.qrLabel', { what: title })} />
      </div>
    {/if}

    <div class="flex min-w-0 flex-1 flex-col">
      <div class="flex items-start gap-3">
        <div class="min-w-0 flex-1">
          <h3 class="display truncate text-[17px] leading-tight font-medium">{title}</h3>
          <p class="mt-0.5 truncate text-sm text-muted" title={link.files.length > 1 ? names : undefined}>
            {formatSize(link.total)}{link.files.length > 1 ? ` · ${names}` : ''}
          </p>
        </div>
        <button class="btn-quiet -mt-1 -mr-1.5 shrink-0" onclick={stop}>
          <X size={16} /> {t(link.ended ? 'common.remove' : 'links.stop')}
        </button>
      </div>

      <p class="mt-2.5 flex items-start gap-1.5 text-sm" aria-live="polite">
        {#if !link.ended}
          {@const Scope = link.unlimited ? Users : User}
          <span class="flex shrink-0 items-center gap-1.5 font-medium text-ink">
            <Scope size={14} strokeWidth={2} class="text-primary" />
            {t(link.unlimited ? 'links.anyone' : 'links.onePerson')}
          </span>
          <span class="text-muted" aria-hidden="true">·</span>
        {/if}
        <span class={line.tone}>{line.text}</span>
      </p>

      {#if live}
        <div class="mt-auto flex items-center gap-2 pt-4">
          {#if isAndroid}
            <button class="btn-primary flex-1" onclick={share}><Share2 size={16} /> {t('links.share')}</button>
            <button class="btn-secondary" aria-expanded={showQr} onclick={() => (showQr = !showQr)}>
              <QrIcon size={16} /> {t('links.qrShow')}
            </button>
          {:else}
            <span class="selectable min-w-0 flex-1 truncate rounded-md border border-line bg-ground px-3 py-1.5 font-mono text-xs max-sm:hidden" title={link.url}>{link.url}</span>
            <button class="btn-secondary shrink-0 !px-3 !py-1.5 text-sm max-sm:flex-1" onclick={copy}>
              {#if copied}<Check size={15} /> {t('common.copied')}{:else}<Copy size={15} /> {t('links.copy')}{/if}
            </button>
            <button class="btn-secondary shrink-0 !px-3 !py-1.5 text-sm sm:hidden" aria-expanded={showQr} onclick={() => (showQr = !showQr)}>
              <QrIcon size={15} /> {t('links.qrShow')}
            </button>
          {/if}
        </div>
      {:else if used}
        <!-- Spent: the way forward is to open it up, not to copy a dead link. -->
        <div class="mt-auto pt-4">
          <button class="btn-secondary" onclick={() => api.setLinkUnlimited(link.id, true)}>
            <Unlock size={16} /> {t('links.openToAnyone')}
          </button>
        </div>
      {/if}
    </div>
  </div>

  {#if showQr && live}
    <div class="flex justify-center px-4 pb-4 {isAndroid ? '' : 'sm:hidden'}" transition:slide={{ duration: dur(220), easing: easeOutExpo }}>
      <div class="w-full max-w-[300px] rounded-md bg-[#fbfcf9] p-2 ring-1 ring-line">
        <QrCode rows={link.qr} label={t('links.qrLabel', { what: title })} />
      </div>
    </div>
  {/if}

  {#if link.visitors.length}
    <ul class="divide-y divide-line border-t border-line">
      {#each shown as v (v.session)}
        <li class="flex items-center gap-3.5 px-4 py-3" transition:slide={{ duration: dur(220), easing: easeOutExpo }}>
          <Bed name={v.name || '?'} id={v.session} size={32} />
          <div class="min-w-0 flex-1">
            <div class="truncate font-medium">{v.name || t('links.someone')}</div>
            {#if v.status === 'sending'}
              <div class="mt-1.5">
                <WaveBand value={v.total ? v.done / v.total : 0} flowing label={t('links.sendingTo', { name: v.name })} />
              </div>
            {/if}
            <div class="mt-0.5 text-sm {tone(v)}">{status(v)}</div>
          </div>
          {#if v.status === 'sending'}
            <button class="btn-quiet shrink-0" aria-label={t('links.cancelFor', { name: v.name })} onclick={() => api.cancelLinkVisitor(link.id, v.session)}>
              <X size={16} /> {t('links.stopVisitor')}
            </button>
          {:else if visitorActive(v)}
            <button class="btn-quiet shrink-0" aria-label={t('links.turnAwayFor', { name: v.name })} onclick={() => api.cancelLinkVisitor(link.id, v.session)}>
              {t('links.turnAway')}
            </button>
          {/if}
        </li>
      {/each}
      {#if hidden || showAll}
        <li>
          <button class="w-full px-4 py-2.5 text-left text-sm text-muted hover:bg-surface-2 hover:text-ink" onclick={() => (showAll = !showAll)}>
            {showAll ? t('links.showLess') : t('links.showMore', { count: hidden })}
          </button>
        </li>
      {/if}
    </ul>
  {/if}
</article>

{#if confirmStop}
  <Dialog title={t('links.stopConfirmTitle')} onclose={() => (confirmStop = false)}>
    <p class="text-muted">{t('links.stopConfirmBody')}</p>
    <div class="mt-6 flex justify-end gap-3">
      <button class="btn-secondary" onclick={() => (confirmStop = false)}>{t('common.cancel')}</button>
      <button
        class="btn-danger"
        onclick={() => {
          confirmStop = false
          stopLink(link.id)
        }}
      >
        <X size={16} /> {t('links.stop')}
      </button>
    </div>
  </Dialog>
{/if}
