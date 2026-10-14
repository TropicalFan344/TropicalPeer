<script lang="ts">
  import { Users, ArrowLeftRight, Link2, Settings, Copy, Check, ChevronRight } from '@lucide/svelte'
  import { app, activeTransfers, activeVisitors, type Page } from '../state.svelte'
  import Bed from './Bed.svelte'
  import { t } from '../i18n.svelte'
  import { scale } from 'svelte/transition'
  import { dur, easeOutExpo } from '../motion'
  import { nav, swipePages, navIndicator } from '../nav.svelte'

  const items: { page: Page; label: string; icon: typeof Users }[] = [
    { page: 'peers', label: 'nav.peers', icon: Users },
    { page: 'transfers', label: 'nav.transfers', icon: ArrowLeftRight },
    { page: 'links', label: 'nav.links', icon: Link2 },
    { page: 'settings', label: 'nav.settings', icon: Settings },
  ]

  const active = $derived(activeTransfers().length)
  const downloading = $derived(activeVisitors())
  const serverState = $derived(app.server.online ? 'online' : app.server.error ? 'offline' : 'connecting')
  const serverLabel = $derived(t({ online: 'server.online', offline: 'server.unreachable', connecting: 'server.connecting' }[serverState]))
  const serverDot = $derived({ online: 'bg-rail-ok', offline: 'bg-rail-alert', connecting: 'bg-rail-info' }[serverState])

  let copied = $state(false)
  async function copyId() {
    if (!app.device) return
    await navigator.clipboard.writeText(app.device.id)
    copied = true
    setTimeout(() => (copied = false), 1500)
  }
</script>

<aside class="rail relative z-10 flex w-60 shrink-0 flex-col bg-rail text-rail-ink">
  <div class="px-6 pt-7 pb-8">
    <div class="display text-[22px] leading-none font-medium tracking-[-0.02em]">
      Tropical<span class="text-rail-muted">Peer</span>
    </div>
  </div>

  <nav class="relative mx-3 flex flex-col gap-0.5" aria-label={t('nav.main')}>
    <!-- The current page's raised fill, sliding between items. Network has no
         item here (it opens from the status line below), so the fill steps aside. -->
    <span
      use:navIndicator={(x) => `translateY(${x * 38}px)`}
      class="pointer-events-none absolute inset-x-0 top-0 h-9 rounded-md bg-rail-raised transition-opacity duration-200
        {swipePages ? '' : 'glide'} {app.page === 'network' ? 'opacity-0' : ''}"
      aria-hidden="true"
    ></span>
    {#each items as item, i (item.page)}
      {@const current = app.page === item.page}
      {@const lit = swipePages ? nav.near === i && app.page !== 'network' : current}
      <button
        class="relative flex h-9 items-center gap-3 rounded-md px-3 text-left font-medium transition-colors duration-200
          {lit ? 'text-rail-ink' : 'text-rail-muted hover:bg-rail-raised/60 hover:text-rail-ink'}"
        aria-current={current ? 'page' : undefined}
        onclick={() => (app.page = item.page)}
      >
        <item.icon size={18} strokeWidth={1.75} />
        {t(item.label)}
        {#if item.page === 'transfers' && active}
          <span class="ml-auto min-w-5 rounded-full bg-rail-ink px-1.5 text-center text-xs font-semibold text-rail" in:scale={{ start: 0.6, duration: dur(180), easing: easeOutExpo }}>
            {active}
          </span>
        {/if}
        {#if item.page === 'links' && downloading}
          <span class="ml-auto min-w-5 rounded-full bg-rail-ink px-1.5 text-center text-xs font-semibold text-rail" in:scale={{ start: 0.6, duration: dur(180), easing: easeOutExpo }}>
            {downloading}
          </span>
        {/if}
        {#if item.page === 'peers' && app.offers.length}
          <span class="ml-auto min-w-5 rounded-full bg-rail-alert px-1.5 text-center text-xs font-semibold text-rail-alert-ink" in:scale={{ start: 0.6, duration: dur(180), easing: easeOutExpo }}>
            {app.offers.length}
          </span>
        {/if}
      </button>
    {/each}
  </nav>

  <div class="mt-auto px-4 pb-5">
    {#if app.device?.id}
      <div class="flex items-center gap-3 border-t border-rail-raised pt-4 pb-3">
        <Bed name={app.device.name} id={app.device.id} size={36} />
        <div class="min-w-0 flex-1">
          <div class="truncate text-sm font-medium">{app.device.name}</div>
          <div class="selectable truncate font-mono text-[11px] text-rail-muted">{app.device.id}</div>
        </div>
        <button
          class="rounded-md p-1.5 text-rail-muted transition-colors hover:bg-rail-raised hover:text-rail-ink"
          aria-label={copied ? t('device.idCopied') : t('device.copyId')}
          title={t('device.copyIdShort')}
          onclick={copyId}
        >
          {#if copied}<Check size={15} />{:else}<Copy size={15} />{/if}
        </button>
      </div>
    {/if}
    <!-- The server status doubles as the doorway to the Network page. -->
    <button
      class="-mx-2 flex w-[calc(100%+1rem)] items-center gap-2.5 rounded-md px-2 py-1.5 text-left text-xs transition-colors
        {app.page === 'network' ? 'bg-rail-raised' : 'hover:bg-rail-raised/60'}"
      title={app.server.error || t('server.networkTitle')}
      aria-current={app.page === 'network' ? 'page' : undefined}
      onclick={() => (app.page = 'network')}
    >
      <span class="relative flex size-2 shrink-0">
        {#if serverState === 'connecting'}
          <span class="absolute inline-flex size-full animate-ping rounded-full opacity-60 {serverDot}"></span>
        {/if}
        <span class="relative inline-flex size-2 rounded-full {serverDot}"></span>
      </span>
      <span class="font-medium whitespace-nowrap">{serverLabel}</span>
      <span class="min-w-0 flex-1 truncate text-rail-muted">{app.server.address}</span>
      <ChevronRight size={14} class="shrink-0 text-rail-muted" aria-hidden="true" />
      <span class="sr-only">{t('server.networkStatus')}</span>
    </button>
  </div>
</aside>

<style>
  /* Items are 36px with 2px between, so one step is 38px (navIndicator above). */
  .glide {
    transition:
      transform 320ms var(--ease-out-expo),
      opacity 200ms;
  }
  @media (prefers-reduced-motion: reduce) {
    .glide {
      transition: opacity 200ms;
    }
  }
  /* The shoreline: the rail meets the pavement along one gentle wave. */
  .rail::after {
    content: '';
    position: absolute;
    top: 0;
    bottom: 0;
    right: -11px;
    width: 12px;
    background: var(--rail);
    /* One long, slow curve from top to bottom: a shoreline, not a frill. */
    mask: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 12 100' preserveAspectRatio='none'%3E%3Cpath d='M0 0h3c8 22 9 40 4 58S2 88 6 100H0z'/%3E%3C/svg%3E")
      no-repeat 0 0 / 100% 100%;
    pointer-events: none;
  }
</style>
