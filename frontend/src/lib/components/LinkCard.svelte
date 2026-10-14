<script lang="ts">
  import { Copy, Check, X, Globe, Share2 } from '@lucide/svelte'
  import { slide } from 'svelte/transition'
  import QrCode from './QrCode.svelte'
  import Bed from './Bed.svelte'
  import WaveBand from './WaveBand.svelte'
  import { api, errorText, isAndroid, type Link, type Visitor } from '../api'
  import { toast, visitorActive } from '../state.svelte'
  import { formatSize } from '../format'
  import { t, what } from '../i18n.svelte'
  import { dur, easeOutExpo } from '../motion'

  let { link }: { link: Link } = $props()

  const title = $derived(what(link.files))
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
  const tone = (v: Visitor) => (v.status === 'failed' ? 'text-bad' : v.status === 'done' ? 'text-ok' : 'text-muted')
</script>

<article class="overflow-hidden rounded-lg border border-line bg-surface">
  <div class="flex gap-6 p-5 max-sm:flex-col max-sm:items-center">
    <!-- Pale stone in both themes, with room around the code: scanners need it. -->
    <!-- Phone: the code fills the width, so someone next to you can scan it. -->
    <div class="w-[184px] shrink-0 rounded-md bg-[#fbfcf9] p-2 ring-1 ring-line transition-opacity max-sm:w-full max-sm:max-w-[300px] {link.ended ? 'opacity-35' : ''}">
      <QrCode rows={link.qr} label={t('links.qrLabel', { what: title })} />
    </div>

    <div class="flex min-w-0 flex-1 flex-col gap-4 max-sm:w-full">
      <div>
        <div class="truncate text-base font-medium" title={link.files.map((f) => f.name).join('\n')}>{title}</div>
        <div class="text-sm text-muted">{formatSize(link.total)}</div>
      </div>

      {#if link.ended}
        <p class="text-sm text-bad">{link.ended}</p>
      {:else}
        {#if link.offline}
          <p class="text-sm text-warn">{t('links.reconnecting')}</p>
        {/if}
        <div class="flex items-center gap-2">
          <span class="selectable min-w-0 flex-1 truncate rounded-md border border-line bg-ground px-3 py-1.5 font-mono text-xs" title={link.url}>{link.url}</span>
          {#if isAndroid}
            <button class="btn-primary shrink-0 !px-3 !py-1.5 text-sm" onclick={share}>
              <Share2 size={15} /> {t('links.share')}
            </button>
          {:else}
            <button class="btn-secondary shrink-0 !px-3 !py-1.5 text-sm" onclick={copy}>
              {#if copied}<Check size={15} /> {t('common.copied')}{:else}<Copy size={15} /> {t('links.copy')}{/if}
            </button>
          {/if}
        </div>

        <label class="flex cursor-pointer items-start gap-3">
          <input
            type="checkbox"
            class="check mt-0.5 shrink-0"
            checked={link.unlimited}
            onchange={(e) => api.setLinkUnlimited(link.id, e.currentTarget.checked)}
          />
          <span class="text-sm">
            <span class="block font-medium">{t('links.anyone')}</span>
            <span class="text-muted">{t(link.unlimited ? 'links.anyoneOn' : 'links.anyoneOff')}</span>
          </span>
        </label>
      {/if}

      <div class="mt-auto flex flex-wrap items-center gap-3">
        <button class="btn-secondary" onclick={() => api.stopLink(link.id)}>
          <X size={16} /> {t(link.ended ? 'common.remove' : 'links.stop')}
        </button>
        {#if !link.ended && link.visitors.length === 0}
          <span class="flex items-center gap-1.5 text-sm text-muted">
            <Globe size={14} strokeWidth={1.75} /> {t('links.waitingForSomeone')}
          </span>
        {/if}
      </div>
    </div>
  </div>

  {#if link.visitors.length}
    <ul class="divide-y divide-line border-t border-line">
      {#each link.visitors as v (v.session)}
        <li class="flex items-center gap-4 px-5 py-3.5" transition:slide={{ duration: dur(220), easing: easeOutExpo }}>
          <Bed name={v.name || '?'} id={v.session} size={36} />
          <div class="min-w-0 flex-1">
            <div class="truncate font-medium">{v.name || t('links.someone')}</div>
            {#if v.status === 'sending'}
              <div class="mt-1.5">
                <WaveBand value={v.total ? v.done / v.total : 0} flowing label={t('links.sendingTo', { name: v.name })} />
              </div>
            {/if}
            <div class="mt-1 truncate text-sm {tone(v)}">{status(v)}</div>
          </div>
          {#if visitorActive(v)}
            <button class="btn-quiet shrink-0" aria-label={t('links.cancelFor', { name: v.name })} onclick={() => api.cancelLinkVisitor(link.id, v.session)}>
              <X size={16} /> {t('common.cancel')}
            </button>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</article>
