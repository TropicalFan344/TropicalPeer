<script lang="ts">
  import { Ellipsis, Trash2, Hand, Zap, ShieldBan, SlidersHorizontal, FolderOpen, FolderUp, Send } from '@lucide/svelte'
  import Bed from './Bed.svelte'
  import Dialog from './Dialog.svelte'
  import WaveBand from './WaveBand.svelte'
  import PeerSettingsDialog from './PeerSettingsDialog.svelte'
  import { api, errorText, isActive, type Peer } from '../api'
  import { app, pickAndSend, toast } from '../state.svelte'
  import { drag } from '../drop.svelte'
  import { formatSize } from '../format'
  import { closeOnBack } from '../back'
  import { t } from '../i18n.svelte'
  import { fade, scale, slide } from 'svelte/transition'
  import { dur, easeOutExpo } from '../motion'

  let { peer }: { peer: Peer } = $props()

  const policy = $derived.by(() => {
    switch (peer.policy) {
      case 'auto':
        return {
          label: peer.maxAutoSize > 0 ? t('policy.autoUpTo', { size: formatSize(peer.maxAutoSize) }) : t('policy.auto'),
          icon: Zap,
          cls: 'text-primary',
        }
      case 'block':
        return { label: t('policy.blocked'), icon: ShieldBan, cls: 'text-bad' }
      default:
        return { label: t('policy.ask'), icon: Hand, cls: 'text-muted' }
    }
  })

  // Outgoing transfers to this peer that are still running.
  const sending = $derived(app.transfers.filter((t) => t.peerId === peer.id && t.direction === 'send' && isActive(t)))
  const sendingShare = $derived.by(() => {
    const total = sending.reduce((n, t) => n + t.total, 0)
    return total > 0 ? sending.reduce((n, t) => n + t.done, 0) / total : 0
  })
  const sendingWaiting = $derived(sending.length > 0 && sending.every((t) => t.status !== 'sending'))
  const findingPath = $derived(sending.some((t) => t.status === 'connecting'))

  const dropHere = $derived(drag.active && drag.overPeer === peer.id)

  let menuOpen = $state(false)
  let settingsOpen = $state(false)
  let confirmRemove = $state(false)
  let removing = $state(false)

  async function remove() {
    removing = true
    try {
      await api.removePeer(peer.id)
      toast(t('peer.removed', { name: peer.name }))
    } catch (e) {
      toast(errorText(e), 'error')
    } finally {
      removing = false
      confirmRemove = false
    }
  }

  function closeMenuOnOutside(node: HTMLElement) {
    // pointerdown covers mouse, pen and touch alike.
    const handler = (e: PointerEvent) => {
      if (!node.contains(e.target as Node)) menuOpen = false
    }
    document.addEventListener('pointerdown', handler)
    return { destroy: () => document.removeEventListener('pointerdown', handler) }
  }

  $effect(() => {
    if (menuOpen) return closeOnBack(() => (menuOpen = false))
  })

  // Phones get the peer's actions as a bottom sheet of large rows; a small
  // dropdown under a thumb-sized button is the desktop's habit, not Android's.
  const phone = typeof matchMedia !== 'undefined' && matchMedia('(max-width: 639.98px)').matches
  let sheetOpen = $state(false)
  function openMenu() {
    if (phone) sheetOpen = true
    else menuOpen = !menuOpen
  }
</script>

<article
  data-peer-id={peer.id}
  class="group relative rounded-lg border bg-surface {menuOpen ? 'z-20' : ''} transition-[transform,border-color,box-shadow] duration-300 ease-out-expo
    {dropHere
    ? 'z-10 scale-[1.03] border-primary shadow-float'
    : drag.active
      ? 'border-dashed border-primary/50'
      : 'border-line hover:border-muted/40 hover:shadow-card'}"
>
  <!-- Whole-card click target: choose files to send -->
  <button class="absolute inset-0" aria-label={t('peer.sendTo', { name: peer.name })} onclick={() => pickAndSend(peer)}></button>

  <div class="pointer-events-none flex items-center gap-4 p-4 pr-12 {peer.policy === 'block' && !drag.active ? 'opacity-60' : ''}">
    <Bed name={peer.name} id={peer.id} size={48} />
    <div class="min-w-0 flex-1">
      <div class="display truncate text-[17px] leading-tight font-medium">{peer.name}</div>
      <div class="font-mono text-[11px] text-muted">{peer.id}</div>
      {#if dropHere}
        <div class="mt-1.5 text-sm font-medium text-primary">{t('peer.dropTo', { name: peer.name })}</div>
      {:else if sending.length}
        <div class="mt-1.5 text-sm text-ink">
          {sendingWaiting
            ? t(findingPath ? 'peer.findingPath' : 'peer.waitingAnswer')
            : t('peer.sendingPct', { pct: Math.floor(sendingShare * 100) })}
        </div>
      {:else}
        <div class="mt-1.5 flex items-center gap-1.5 text-sm {policy.cls}">
          <policy.icon size={14} strokeWidth={1.75} />
          <span class="truncate">{policy.label}</span>
          {#if peer.saveDir}
            <span class="ml-1 text-muted" title={t('peer.savesTo', { dir: peer.saveDir })}><FolderOpen size={14} strokeWidth={1.75} /></span>
          {/if}
        </div>
      {/if}
    </div>
  </div>

  {#if sending.length}
    <div class="pointer-events-none px-4 pb-3.5" transition:slide={{ duration: dur(220), easing: easeOutExpo }}>
      <WaveBand value={sendingShare} waiting={sendingWaiting} height={6} label={t('peer.sendingTo', { name: peer.name })} />
    </div>
  {/if}

  <div class="absolute top-3 right-3 z-10" data-no-drag use:closeMenuOnOutside>
    <!-- Hidden until hover with a mouse; always there on touch, which can't hover. -->
    <button
      class="rounded-md p-1.5 text-muted opacity-0 transition-opacity group-hover:opacity-100 hover:bg-surface-2
        hover:text-ink focus-visible:opacity-100 pointer-coarse:-m-1 pointer-coarse:p-2.5 pointer-coarse:opacity-100
        {menuOpen ? 'opacity-100' : ''}"
      aria-label={t('peer.moreOptions', { name: peer.name })}
      aria-expanded={menuOpen || sheetOpen}
      onclick={openMenu}
    >
      <Ellipsis size={18} />
    </button>
    {#if menuOpen}
      <div
        class="absolute right-0 mt-1 w-44 origin-top-right rounded-lg border border-line bg-surface p-1 text-left shadow-float pointer-coarse:w-52"
        in:scale={{ start: 0.95, duration: dur(160), easing: easeOutExpo }}
        out:fade={{ duration: dur(100) }}
      >
        <button
          class="flex w-full items-center gap-2.5 rounded-md px-3 py-2 text-sm hover:bg-surface-2 pointer-coarse:py-3"
          onclick={() => {
            menuOpen = false
            pickAndSend(peer)
          }}
        >
          <Send size={15} strokeWidth={1.75} /> {t('peer.menuSend')}
        </button>
        <button
          class="flex w-full items-center gap-2.5 rounded-md px-3 py-2 text-sm hover:bg-surface-2 pointer-coarse:py-3"
          onclick={() => {
            menuOpen = false
            pickAndSend(peer, true)
          }}
        >
          <FolderUp size={15} strokeWidth={1.75} /> {t('peer.menuSendFolder')}
        </button>
        <button
          class="flex w-full items-center gap-2.5 rounded-md px-3 py-2 text-sm hover:bg-surface-2 pointer-coarse:py-3"
          onclick={() => {
            menuOpen = false
            settingsOpen = true
          }}
        >
          <SlidersHorizontal size={15} strokeWidth={1.75} /> {t('peer.menuSettings')}
        </button>
        <div class="my-1 h-px bg-line"></div>
        <button
          class="flex w-full items-center gap-2.5 rounded-md px-3 py-2 text-sm text-bad hover:bg-heliconia-soft pointer-coarse:py-3"
          onclick={() => {
            menuOpen = false
            confirmRemove = true
          }}
        >
          <Trash2 size={15} strokeWidth={1.75} /> {t('peer.remove')}
        </button>
      </div>
    {/if}
  </div>
</article>

{#if sheetOpen}
  <Dialog title={peer.name} onclose={() => (sheetOpen = false)}>
    <div class="-mx-2 -mt-2 flex flex-col">
      <button
        class="flex min-h-13 items-center gap-4 rounded-md px-3 text-left active:bg-surface-2"
        onclick={() => {
          sheetOpen = false
          pickAndSend(peer)
        }}
      >
        <Send size={20} strokeWidth={1.75} class="text-muted" /> {t('peer.sheetSend')}
      </button>
      <button
        class="flex min-h-13 items-center gap-4 rounded-md px-3 text-left active:bg-surface-2"
        onclick={() => {
          sheetOpen = false
          pickAndSend(peer, true)
        }}
      >
        <FolderUp size={20} strokeWidth={1.75} class="text-muted" /> {t('peer.sheetSendFolder')}
      </button>
      <button
        class="flex min-h-13 items-center gap-4 rounded-md px-3 text-left active:bg-surface-2"
        onclick={() => {
          sheetOpen = false
          settingsOpen = true
        }}
      >
        <SlidersHorizontal size={20} strokeWidth={1.75} class="text-muted" /> {t('peer.sheetSettings')}
      </button>
      <div class="mx-3 my-1 h-px bg-line"></div>
      <button
        class="flex min-h-13 items-center gap-4 rounded-md px-3 text-left text-bad active:bg-heliconia-soft"
        onclick={() => {
          sheetOpen = false
          confirmRemove = true
        }}
      >
        <Trash2 size={20} strokeWidth={1.75} /> {t('peer.remove')}
      </button>
    </div>
  </Dialog>
{/if}

{#if settingsOpen}
  <PeerSettingsDialog
    {peer}
    onclose={() => (settingsOpen = false)}
    onremove={() => {
      settingsOpen = false
      confirmRemove = true
    }}
  />
{/if}

{#if confirmRemove}
  <Dialog title={t('peer.removeTitle', { name: peer.name })} onclose={() => (confirmRemove = false)}>
    <p class="text-muted">{t('peer.removeBody', { name: peer.name })}</p>
    <div class="mt-6 flex justify-end gap-3">
      <button class="btn-secondary" onclick={() => (confirmRemove = false)}>{t('common.cancel')}</button>
      <button class="btn-danger" disabled={removing} onclick={remove}>{t('common.remove')}</button>
    </div>
  </Dialog>
{/if}
