<script lang="ts">
  import { onMount } from 'svelte'
  import { fly } from 'svelte/transition'
  import { CircleAlert } from '@lucide/svelte'
  import Sidebar from './lib/components/Sidebar.svelte'
  import TabBar from './lib/components/TabBar.svelte'
  import Toasts from './lib/components/Toasts.svelte'
  import ShareSheet from './lib/components/ShareSheet.svelte'
  import OfferStrip from './lib/components/OfferStrip.svelte'
  import PeersPage from './lib/pages/PeersPage.svelte'
  import SettingsPage from './lib/pages/SettingsPage.svelte'
  import TransfersPage from './lib/pages/TransfersPage.svelte'
  import NetworkPage from './lib/pages/NetworkPage.svelte'
  import LinksPage from './lib/pages/LinksPage.svelte'
  import Pager from './lib/components/Pager.svelte'
  import { app, initState } from './lib/state.svelte'
  import { initDrop } from './lib/drop.svelte'
  import { isAndroid } from './lib/api'
  import { initBack } from './lib/back'
  import { t, applyLanguage } from './lib/i18n.svelte'
  import { swipePages, navIndex, setNavPos } from './lib/nav.svelte'
  import { dur, easeOutExpo } from './lib/motion'

  onMount(() => {
    applyLanguage()
    initState()
    if (!isAndroid) initDrop() // files arrive through the picker and share sheet instead
    initBack()
  })

  // Desktop: the nav indicator follows the page (the pager drives it on Android).
  $effect(() => {
    if (!swipePages) setNavPos(navIndex(app.page))
  })

  // Desktop: each page opens at its top, settling in from just below.
  let scroller = $state<HTMLElement>()
  $effect(() => {
    void app.page
    if (scroller) scroller.scrollTop = 0
  })
</script>

<!-- Desktop: the jungle rail on the left. Phone: the same garden turned on
     its side, content above and the tab bar along the bottom. -->
<div class="flex h-full max-sm:flex-col">
  <div class="contents max-sm:hidden"><Sidebar /></div>
  <!-- Under the phone's status bar: ground, so scrolled content never runs beneath the clock. -->
  <div class="h-[var(--sat)] shrink-0 bg-ground sm:hidden"></div>
  <main class="flex min-h-0 min-w-0 flex-1 flex-col max-sm:pr-[var(--sar)] max-sm:pl-[var(--sal)]">
    {#if app.fatalError}
      <div class="flex items-center gap-3 border-b border-line bg-heliconia-soft px-10 py-3 text-sm max-sm:px-4">
        <CircleAlert size={18} class="shrink-0 text-bad" />
        <span>{t('app.fatal', { error: app.fatalError })}</span>
      </div>
    {/if}
    <OfferStrip />

    {#if swipePages}
      <Pager />
    {:else}
      <div bind:this={scroller} class="min-h-0 flex-1 overflow-y-auto">
        {#key app.page}
          <div in:fly={{ y: 6, duration: dur(220), easing: easeOutExpo }}>
            {#if app.page === 'peers'}
              <PeersPage />
            {:else if app.page === 'transfers'}
              <TransfersPage />
            {:else if app.page === 'links'}
              <LinksPage />
            {:else if app.page === 'network'}
              <NetworkPage />
            {:else}
              <SettingsPage />
            {/if}
          </div>
        {/key}
      </div>
    {/if}
  </main>
  <TabBar />
</div>

<ShareSheet />
<Toasts />
