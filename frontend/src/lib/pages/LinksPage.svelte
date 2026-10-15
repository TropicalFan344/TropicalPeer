<script lang="ts">
  import { Link2, Upload } from '@lucide/svelte'
  import { flip } from 'svelte/animate'
  import { slide } from 'svelte/transition'
  import LinkCard from '../components/LinkCard.svelte'
  import { app, pickAndShareLink } from '../state.svelte'
  import { isAndroid } from '../api'
  import { drag } from '../drop.svelte'
  import { t } from '../i18n.svelte'
  import { dur, easeOutExpo } from '../motion'
</script>

<div class="page">
  <header class="flex flex-wrap items-end justify-between gap-x-6 gap-y-4">
    <div>
      <h1 class="page-title">{t('links.title')}</h1>
      <p class="mt-1 max-w-[60ch] text-muted">{t('links.subtitle')}</p>
    </div>
    {#if app.linksAvailable}
      <button class="btn-primary whitespace-nowrap" onclick={pickAndShareLink}><Link2 size={16} /> {t('links.new')}</button>
    {/if}
  </header>

  {#if !app.linksAvailable}
    <section class="border-t border-line pt-8">
      <h2 class="text-xl font-medium">{t(app.server.online ? 'links.unsupportedTitle' : 'links.offlineTitle')}</h2>
      <p class="mt-2 max-w-[60ch] text-muted">{t(app.server.online ? 'links.unsupportedBody' : 'links.offlineBody')}</p>
      <button class="btn-secondary mt-4" onclick={() => (app.page = 'network')}>{t('links.seeNetwork')}</button>
    </section>
  {:else if app.links.length === 0}
    <!-- Empty: the whole block is where files go. -->
    <section
      class="flex flex-col items-start gap-2 rounded-lg border border-dashed p-8 transition-colors duration-200
        {drag.active ? 'border-primary bg-primary-soft' : 'border-line'}"
    >
      <Upload size={20} strokeWidth={1.75} class="mb-1 text-muted" />
      <h2 class="text-xl font-medium">{t('links.emptyTitle')}</h2>
      <p class="max-w-[60ch] text-muted">{t(isAndroid ? 'links.emptyBodyTouch' : 'links.emptyBody')}</p>
      <button class="btn-primary mt-3" onclick={pickAndShareLink}><Link2 size={16} /> {t('links.new')}</button>
    </section>
  {:else}
    {#if drag.active}
      <div
        class="rounded-lg border border-dashed border-primary bg-primary-soft px-5 py-4 text-sm font-medium"
        transition:slide={{ duration: dur(180), easing: easeOutExpo }}
      >
        {t('links.dropHint')}
      </div>
    {/if}
    <div class="flex flex-col gap-4">
      {#each app.links as link (link.id)}
        <div animate:flip={{ duration: dur(260), easing: easeOutExpo }} transition:slide={{ duration: dur(220), easing: easeOutExpo }}>
          <LinkCard {link} />
        </div>
      {/each}
    </div>
  {/if}
</div>
