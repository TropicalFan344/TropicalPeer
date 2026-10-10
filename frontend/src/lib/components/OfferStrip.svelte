<script lang="ts">
  import { onDestroy } from 'svelte'
  import { slide } from 'svelte/transition'
  import { Folder } from '@lucide/svelte'
  import Bed from './Bed.svelte'
  import { app, respondOffer } from '../state.svelte'
  import { formatSize } from '../format'
  import { isAndroid } from '../api'
  import { t, what } from '../i18n.svelte'

  let now = $state(Date.now())
  const timer = setInterval(() => (now = Date.now()), 1000)
  onDestroy(() => clearInterval(timer))

  function remaining(expiresAt: number) {
    const s = Math.max(0, Math.round((expiresAt - now) / 1000))
    return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
  }

  // Phone toasts sit at the top of the screen, just under any offers, so they
  // never cover Accept and Decline (see Toasts.svelte).
  let stripHeight = $state(0)
  $effect(() => {
    const root = document.documentElement.style
    root.setProperty('--offer-h', `${app.offers.length ? stripHeight : 0}px`)
    return () => root.setProperty('--offer-h', '0px')
  })

  function shortDir(dir: string) {
    const parts = dir.split(/[\\/]/).filter(Boolean)
    return parts.length > 2 ? `…\\${parts.slice(-2).join('\\')}` : dir
  }
</script>

{#if app.offers.length}
  <!-- Sticky: an offer must be seen wherever the page is scrolled to, since it expires. -->
  <div class="sticky top-0 z-20 flex flex-col gap-px bg-offer-ink/25" aria-live="polite" bind:offsetHeight={stripHeight}>
    {#each app.offers as offer (offer.id)}
      {@const shown = offer.files.slice(0, 3)}
      <!-- Phone: the sentence on top, answers as two full-width buttons under it, in thumb reach. -->
      <section
        class="flex items-center gap-5 bg-offer px-10 py-4 text-offer-ink max-sm:flex-wrap max-sm:gap-x-3.5 max-sm:gap-y-3 max-sm:px-4"
        aria-label={t('offer.label', { name: offer.peerName })}
        transition:slide={{ duration: 220 }}
      >
        <Bed name={offer.peerName} id={offer.peerId} size={44} edge="var(--offer-muted)" />
        <div class="min-w-0 flex-1">
          <h2 class="text-[17px] leading-snug font-medium">
            {t('offer.wants', { name: offer.peerName, what: what(offer.files) })}
          </h2>
          <div class="mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-sm text-offer-muted">
            <span class="font-medium text-offer-ink">{formatSize(offer.total)}</span>
            {#if offer.files.length > 1}
              <span class="truncate">
                {shown.map((f) => f.name).join(', ')}{offer.files.length > 3 ? `, ${t('offer.more', { count: offer.files.length - 3 })}` : ''}
              </span>
            {/if}
            {#if !isAndroid}
              <!-- On Android received files always go to Downloads/TropicalPeer. -->
              <span class="flex items-center gap-1" title={offer.saveDir}>
                <Folder size={13} /> {shortDir(offer.saveDir)}
              </span>
            {/if}
            <span class="text-offer-muted tabular-nums sm:hidden">{t('offer.declinesIn', { time: remaining(offer.expiresAt) })}</span>
          </div>
        </div>
        <span class="text-sm whitespace-nowrap text-offer-muted tabular-nums max-sm:hidden">
          {t('offer.declinesIn', { time: remaining(offer.expiresAt) })}
        </span>
        <div class="flex gap-2 max-sm:w-full max-sm:gap-3">
          <button
            class="rounded-md border border-offer-ink/60 px-4 py-2 font-medium whitespace-nowrap text-offer-ink transition-colors
              hover:bg-offer-ink/10 active:bg-offer-ink/15 max-sm:min-h-11 max-sm:flex-1"
            onclick={() => respondOffer(offer, false)}
          >
            {t('offer.decline')}
          </button>
          <button
            class="rounded-md bg-offer-ink px-4 py-2 font-medium whitespace-nowrap text-offer transition-opacity hover:opacity-90
              active:opacity-85 max-sm:min-h-11 max-sm:flex-1"
            onclick={() => respondOffer(offer, true)}
          >
            {t('offer.accept')}
          </button>
        </div>
      </section>
    {/each}
  </div>
{/if}
