<script lang="ts">
  import { onDestroy } from 'svelte'
  import { Ellipsis, Trash2, Hand, Zap, ShieldBan, SlidersHorizontal, FolderOpen, FolderUp, Send, UserX, MessageSquareText, Moon } from '@lucide/svelte'
  import ComposeBar from './ComposeBar.svelte'
  import Bed from './Bed.svelte'
  import Dialog from './Dialog.svelte'
  import WaveBand from './WaveBand.svelte'
  import PeerSettingsDialog from './PeerSettingsDialog.svelte'
  import SendChooser from './SendChooser.svelte'
  import { api, errorText, isActive, type Peer } from '../api'
  import { app, closeCompose, composeNote, openCompose, pickAndSend, toast } from '../state.svelte'
  import { drag } from '../drop.svelte'
  import { formatAgo, formatSize } from '../format'
  import { closeOnBack } from '../back'
  import { i18n, t } from '../i18n.svelte'
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

  // Presence: "Offline · seen 2 hr. ago" takes the policy line's place (the
  // policy is about receiving; this is about whether a send can work now).
  let now = $state(Date.now())
  const tick = setInterval(() => (now = Date.now()), 60_000)
  onDestroy(() => clearInterval(tick))
  const offline = $derived(peer.presence === 'offline' && !peer.removedUs)
  const offlineText = $derived.by(() => {
    const when = peer.lastSeen ? formatAgo(peer.lastSeen, i18n.lang, now) : ''
    return when ? t('peer.offlineSeen', { when }) : t('peer.offline')
  })
  const statusId = $derived(`peer-status-${peer.id}`)

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
  // Phones ask what to send first: photos, files or a folder.
  let chooserOpen = $state(false)

  // A peer that removed this device refuses whatever we send: say so
  // instead of opening a picker for nothing.
  // An offline one would only fail after the files were picked: say so first,
  // and still let them try (it may be opening right now).
  function send() {
    if (peer.removedUs) toast(t('transfer.removedYou', { name: peer.name }), 'info', 6000)
    else if (offline) toast(t('transfer.offline', { name: peer.name }), 'info', 6000, { label: t('peer.sendAnyway'), run: choose })
    else choose()
  }
  function choose() {
    if (phone) chooserOpen = true
    else pickAndSend(peer, 'files', composeNote(peer)) // words in the open bar go too
  }
  // A computer's card opens up to write (💬, right-click or a paste).
  const composing = $derived(app.compose.peerId === peer.id)
  const toggleCompose = () => (composing ? closeCompose() : openCompose(peer))

  // Pasting onto a focused card opens its compose bar, filled in; nothing
  // goes until the person sends it (a clipboard can hold a password).
  function onpaste(e: ClipboardEvent) {
    if (phone || (e.target as HTMLElement).closest('[data-no-drag]')) return
    const text = e.clipboardData?.getData('text/plain') ?? ''
    if (!text.trim()) return
    e.preventDefault()
    openCompose(peer, text)
  }
  function oncontextmenu(e: MouseEvent) {
    if (phone || (e.target as HTMLElement).closest('[data-no-drag]')) return
    e.preventDefault()
    openCompose(peer)
  }
  // Phones write in the send chooser.
  let chooserText = $state(false)
  function writeOnPhone() {
    if (peer.removedUs) toast(t('transfer.removedYou', { name: peer.name }), 'info', 6000)
    else {
      chooserText = true
      chooserOpen = true
    }
  }
  function openMenu() {
    if (phone) sheetOpen = true
    else menuOpen = !menuOpen
  }

  // The dropdown is a menu for the keyboard too: focus moves into it,
  // arrows move between items, Escape (or Tab) closes it.
  let menuButton = $state<HTMLButtonElement>()
  let menuEl = $state<HTMLElement>()
  const menuItems = () => [...(menuEl?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? [])]
  $effect(() => {
    if (menuOpen && menuEl) menuItems()[0]?.focus()
  })
  function closeMenu(refocus: boolean) {
    menuOpen = false
    if (refocus) menuButton?.focus()
  }
  function onMenuKey(e: KeyboardEvent) {
    const items = menuItems()
    const i = items.indexOf(document.activeElement as HTMLElement)
    const go = (n: number) => {
      e.preventDefault()
      items[(n + items.length) % items.length]?.focus()
    }
    if (e.key === 'ArrowDown') go(i + 1)
    else if (e.key === 'ArrowUp') go(i - 1)
    else if (e.key === 'Home') go(0)
    else if (e.key === 'End') go(items.length - 1)
    else if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      closeMenu(true)
    } else if (e.key === 'Tab') closeMenu(false)
  }
</script>

<article
  data-peer-id={peer.id}
  {onpaste}
  {oncontextmenu}
  class="group relative rounded-lg border bg-surface {menuOpen ? 'z-20' : ''} transition-[transform,scale,border-color,box-shadow] duration-300 ease-out-expo
    has-[>button:active]:scale-[0.985]
    {dropHere
    ? 'z-10 scale-[1.03] border-primary shadow-float'
    : drag.active
      ? 'border-dashed border-primary/50'
      : 'border-line hover:border-muted/40 hover:shadow-card'}"
>
  <!-- Whole-card click target: choose files to send -->
  <button class="absolute inset-0" aria-label={t('peer.sendTo', { name: peer.name })} aria-describedby={statusId} onclick={send}></button>

  <div class="pointer-events-none flex items-center gap-4 p-4 pr-12 sm:pr-20">
    <!-- Blocked or offline fades the bed alone: the words stay readable. -->
    <div class="relative shrink-0">
      <div class="transition-[opacity,filter] duration-300 {(peer.policy === 'block' || offline) && !drag.active ? 'opacity-45 grayscale-[0.6]' : ''}">
        <Bed name={peer.name} id={peer.id} size={48} />
      </div>
      {#if peer.presence === 'online' && !peer.removedUs}
        <span
          class="absolute -right-0.5 -bottom-0.5 size-3 rounded-full bg-ok ring-[2.5px] ring-surface"
          aria-hidden="true"
          in:scale={{ start: 0.4, duration: dur(200), easing: easeOutExpo }}
        ></span>
      {/if}
    </div>
    <div class="min-w-0 flex-1" id={statusId}>
      {#if peer.presence === 'online' && !peer.removedUs}<span class="sr-only">{t('peer.online')}.</span>{/if}
      <div class="display truncate text-[17px] leading-tight font-medium">{peer.name}</div>
      <div class="font-mono text-[11px] text-muted">{peer.id}</div>
      {#if dropHere}
        <div class="mt-1.5 text-sm font-medium text-primary">
          {composeNote(peer) ? t('text.dropWithNote', { name: peer.name }) : t('peer.dropTo', { name: peer.name })}
        </div>
      {:else if sending.length}
        <div class="mt-1.5 text-sm text-ink">
          {sendingWaiting
            ? t(findingPath ? 'peer.findingPath' : 'peer.waitingAnswer')
            : t('peer.sendingPct', { pct: Math.floor(sendingShare * 100) })}
        </div>
      {:else if peer.removedUs}
        <div class="mt-1.5 flex items-center gap-1.5 text-sm text-bad">
          <UserX size={14} strokeWidth={1.75} />
          <span class="truncate">{t('peer.removedUs')}</span>
        </div>
      {:else if offline}
        <div class="mt-1.5 flex items-center gap-1.5 text-sm text-muted">
          <Moon size={14} strokeWidth={1.75} class="shrink-0" />
          <span class="truncate">{offlineText}</span>
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
      <WaveBand value={sendingShare} waiting={sendingWaiting} flowing={!sendingWaiting} height={6} label={t('peer.sendingTo', { name: peer.name })} />
    </div>
  {/if}

  {#if composing}
    <ComposeBar {peer} />
  {/if}

  <div class="group/corner absolute top-3 right-3 z-10" data-no-drag use:closeMenuOnOutside>
    {#if !phone}
      <!-- Left of the ⋯ while the card is hovered (or the menu open). With the
           bar open it stays when the pointer leaves, gliding into the ⋯'s
           place as that fades, and back out when the pointer returns. -->
      <button
        class="absolute top-0 right-full mr-0.5 rounded-md p-1.5 text-muted opacity-0 transition-[opacity,translate] duration-200 ease-out-expo
          group-hover:opacity-100 hover:bg-surface-2 hover:text-ink focus-visible:opacity-100 pointer-coarse:opacity-100
          {composing && !menuOpen
          ? 'translate-x-[calc(100%+0.125rem)] text-primary opacity-100 group-hover:translate-x-0 group-has-[:focus-visible]/corner:translate-x-0 pointer-coarse:translate-x-0'
          : ''}"
        aria-label={t('text.compose', { name: peer.name })}
        title={t('text.compose', { name: peer.name })}
        aria-expanded={composing}
        onclick={toggleCompose}
      >
        <MessageSquareText size={18} />
      </button>
    {/if}
    <!-- Hidden until hover with a mouse; always there on touch, which can't hover. -->
    <button
      class="rounded-md p-1.5 text-muted opacity-0 transition-opacity group-hover:opacity-100 hover:bg-surface-2
        hover:text-ink focus-visible:opacity-100 pointer-coarse:-m-2 pointer-coarse:p-[13px] pointer-coarse:opacity-100
        {menuOpen ? 'opacity-100' : ''}"
      bind:this={menuButton}
      aria-label={t('peer.moreOptions', { name: peer.name })}
      aria-haspopup={phone ? 'dialog' : 'menu'}
      aria-expanded={menuOpen || sheetOpen}
      onclick={openMenu}
    >
      <Ellipsis size={18} />
    </button>
    {#if menuOpen}
      <div
        bind:this={menuEl}
        role="menu"
        tabindex="-1"
        aria-label={t('peer.moreOptions', { name: peer.name })}
        onkeydown={onMenuKey}
        class="absolute right-0 mt-1 w-44 origin-top-right rounded-lg border border-line bg-surface p-1 text-left shadow-float pointer-coarse:w-52"
        in:scale={{ start: 0.95, duration: dur(160), easing: easeOutExpo }}
        out:fade={{ duration: dur(100) }}
      >
        <button
          role="menuitem"
          tabindex="-1"
          class="flex w-full items-center gap-2.5 rounded-md px-3 py-2 text-sm focus-visible:bg-surface-2 focus-visible:outline-none hover:bg-surface-2 pointer-coarse:py-3"
          onclick={() => {
            menuOpen = false
            pickAndSend(peer)
          }}
        >
          <Send size={15} strokeWidth={1.75} /> {t('peer.menuSend')}
        </button>
        <button
          role="menuitem"
          tabindex="-1"
          class="flex w-full items-center gap-2.5 rounded-md px-3 py-2 text-sm focus-visible:bg-surface-2 focus-visible:outline-none hover:bg-surface-2 pointer-coarse:py-3"
          onclick={() => {
            menuOpen = false
            pickAndSend(peer, 'folder')
          }}
        >
          <FolderUp size={15} strokeWidth={1.75} /> {t('peer.menuSendFolder')}
        </button>
        <button
          role="menuitem"
          tabindex="-1"
          class="flex w-full items-center gap-2.5 rounded-md px-3 py-2 text-sm focus-visible:bg-surface-2 focus-visible:outline-none hover:bg-surface-2 pointer-coarse:py-3"
          onclick={() => {
            menuOpen = false
            openCompose(peer)
          }}
        >
          <MessageSquareText size={15} strokeWidth={1.75} /> {t('text.menuSend')}
        </button>
        <button
          role="menuitem"
          tabindex="-1"
          class="flex w-full items-center gap-2.5 rounded-md px-3 py-2 text-sm focus-visible:bg-surface-2 focus-visible:outline-none hover:bg-surface-2 pointer-coarse:py-3"
          onclick={() => {
            menuOpen = false
            settingsOpen = true
          }}
        >
          <SlidersHorizontal size={15} strokeWidth={1.75} /> {t('peer.menuSettings')}
        </button>
        <div class="my-1 h-px bg-line" role="separator"></div>
        <button
          role="menuitem"
          tabindex="-1"
          class="flex w-full items-center gap-2.5 rounded-md px-3 py-2 text-sm focus-visible:bg-surface-2 focus-visible:outline-none text-bad hover:bg-heliconia-soft pointer-coarse:py-3"
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
          pickAndSend(peer, 'folder')
        }}
      >
        <FolderUp size={20} strokeWidth={1.75} class="text-muted" /> {t('peer.sheetSendFolder')}
      </button>
      <button
        class="flex min-h-13 items-center gap-4 rounded-md px-3 text-left active:bg-surface-2"
        onclick={() => {
          sheetOpen = false
          writeOnPhone()
        }}
      >
        <MessageSquareText size={20} strokeWidth={1.75} class="text-muted" /> {t('text.sheetSend')}
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

{#if chooserOpen}
  <SendChooser
    {peer}
    focusText={chooserText}
    onclose={() => {
      chooserOpen = false
      chooserText = false
    }}
  />
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
