<script lang="ts">
  // The phone's navigation: the jungle rail laid along the bottom edge, its
  // shoreline curve now on top. Same four places as the desktop rail.
  import { Users, ArrowLeftRight, Link2, Settings } from '@lucide/svelte'
  import { app, activeTransfers, activeVisitors, type Page } from '../state.svelte'
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
  // The desktop rail shows the server line at all times; here a dot on
  // Settings speaks up only when something is wrong or still connecting.
  const serverDot = $derived(app.server.online ? '' : app.server.error ? 'bg-rail-alert' : 'bg-rail-info')
  const current = (page: Page) => app.page === page || (page === 'settings' && app.page === 'network')
  // Mid-swipe the item nearest the finger lights up, before the page settles.
  const lit = (page: Page, i: number) => (swipePages ? nav.near === i : current(page))
</script>

<nav class="shore relative z-10 shrink-0 bg-rail text-rail-ink sm:hidden" aria-label={t('nav.main')}>
  <div class="relative mx-[calc(var(--sal)+8px)] mr-[calc(var(--sar)+8px)] flex h-[var(--tabbar)] items-stretch">
    <!-- One raised pill under the current item, sliding between them (and
         tracking the finger while a page is swiped). -->
    <span use:navIndicator={(x) => `translateX(${x * 100}%)`} class="pointer-events-none absolute top-0 left-0 flex h-full w-1/4 justify-center pt-[calc((var(--tabbar)_-_52px)/2)] {swipePages ? '' : 'glide'}" aria-hidden="true">
      <span class="h-8 w-16 rounded-full bg-rail-raised"></span>
    </span>
    {#each items as item, i (item.page)}
      {@const on = lit(item.page, i)}
      <button
        class="relative flex flex-1 flex-col items-center justify-center gap-1 text-xs font-medium transition-colors duration-200
          {on ? 'text-rail-ink' : 'text-rail-muted active:text-rail-ink'}"
        aria-current={current(item.page) ? 'page' : undefined}
        onclick={() => (app.page = item.page)}
      >
        <span class="relative flex h-8 w-16 items-center justify-center">
          <item.icon size={22} strokeWidth={1.75} />
          {#if item.page === 'transfers' && active}
            <span class="badge bg-rail-ink text-rail" in:scale={{ start: 0.6, duration: dur(180), easing: easeOutExpo }}>{active}</span>
          {:else if item.page === 'links' && downloading}
            <span class="badge bg-rail-ink text-rail" in:scale={{ start: 0.6, duration: dur(180), easing: easeOutExpo }}>{downloading}</span>
          {:else if item.page === 'peers' && app.offers.length}
            <span class="badge bg-rail-alert text-rail-alert-ink" in:scale={{ start: 0.6, duration: dur(180), easing: easeOutExpo }}>{app.offers.length}</span>
          {:else if item.page === 'settings' && serverDot}
            <span class="absolute top-1 right-4 size-2 rounded-full ring-2 ring-rail {serverDot}" aria-hidden="true"></span>
            <span class="sr-only">{t(app.server.error ? 'server.unreachableSr' : 'server.connectingSr')}</span>
          {/if}
        </span>
        {t(item.label)}
      </button>
    {/each}
  </div>
  <div class="h-[var(--sab)]"></div>
</nav>

<style>
  /* Without a pager to drive it, the pill glides to the new item. */
  .glide {
    transition: transform 320ms var(--ease-out-expo);
  }
  @media (prefers-reduced-motion: reduce) {
    .glide {
      transition-duration: 0ms;
    }
  }
  .badge {
    position: absolute;
    top: -2px;
    right: 6px;
    min-width: 18px;
    height: 18px;
    padding-inline: 5px;
    border-radius: 9999px;
    font-size: 11px;
    font-weight: 600;
    line-height: 18px;
    text-align: center;
    box-shadow: 0 0 0 2px var(--rail);
  }
  /* The shoreline: where the jungle meets the pavement, one long slow curve
     (the desktop rail's edge, turned to lie along the top). */
  .shore::before {
    content: '';
    position: absolute;
    left: 0;
    right: 0;
    top: -9px;
    height: 10px;
    background: var(--rail);
    mask: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 10' preserveAspectRatio='none'%3E%3Cpath d='M0 10V7C22-1 40-1 58 4S88 11 100 6v4z'/%3E%3C/svg%3E")
      no-repeat 0 0 / 100% 100%;
    pointer-events: none;
  }
</style>
