<script lang="ts">
  import TransferRow from '../components/TransferRow.svelte'
  import { api, isActive, isAndroid, type Transfer } from '../api'
  import { app } from '../state.svelte'
  import { t } from '../i18n.svelte'
  import { flip } from 'svelte/animate'
  import { slide } from 'svelte/transition'
  import { dur, easeOutExpo } from '../motion'

  // Paused transfers aren't finished: they stay with the running ones.
  const current = (t: Transfer) => isActive(t) || t.status === 'paused'
  const active = $derived(app.transfers.filter(current))
  const finished = $derived(app.transfers.filter((t) => !current(t)))
</script>

<div class="page">
  <header class="flex flex-wrap items-end justify-between gap-x-6 gap-y-4">
    <div>
      <h1 class="page-title">{t('transfers.title')}</h1>
      <p class="mt-1 text-muted">{t('transfers.subtitle')}</p>
    </div>
    {#if finished.length}
      <button class="btn-secondary" onclick={() => api.clearFinishedTransfers()}>{t('transfers.clear')}</button>
    {/if}
  </header>

  {#if app.transfers.length === 0}
    <section class="border-t border-line pt-8">
      <h2 class="text-xl font-medium">{t('transfers.emptyTitle')}</h2>
      <p class="mt-2 max-w-[60ch] text-muted">{t(isAndroid ? 'transfers.emptyBodyTouch' : 'transfers.emptyBody')}</p>
    </section>
  {:else}
    {#if active.length}
      <section class="flex flex-col gap-3">
        <h2 class="section-title">{t('transfers.inProgress')}</h2>
        <ul class="divide-y divide-line overflow-hidden rounded-lg border border-line bg-surface">
          {#each active as t (t.id)}
            <li animate:flip={{ duration: dur(260), easing: easeOutExpo }} transition:slide={{ duration: dur(220), easing: easeOutExpo }}>
              <TransferRow transfer={t} />
            </li>
          {/each}
        </ul>
      </section>
    {/if}
    {#if finished.length}
      <section class="flex flex-col gap-3">
        <h2 class="section-title">{t('transfers.finished')}</h2>
        <ul class="divide-y divide-line overflow-hidden rounded-lg border border-line bg-surface">
          {#each finished as t (t.id)}
            <li animate:flip={{ duration: dur(260), easing: easeOutExpo }} transition:slide={{ duration: dur(220), easing: easeOutExpo }}>
              <TransferRow transfer={t} />
            </li>
          {/each}
        </ul>
      </section>
    {/if}
  {/if}
</div>
