<script lang="ts">
  // A settings row's label column: the title, one short line saying what the
  // setting is for, and, behind an ⓘ toggle, the longer explanation for those
  // who want it. A toggle rather than a tooltip, because touch can't hover.
  import type { Snippet } from 'svelte'
  import { slide } from 'svelte/transition'
  import { Info } from '@lucide/svelte'
  import { t } from '../i18n.svelte'

  let {
    title,
    hint,
    detail = '',
    children,
  }: { title: string; hint: string; detail?: string; children?: Snippet } = $props()

  let open = $state(false)
  const id = `detail-${Math.random().toString(36).slice(2, 9)}`
</script>

<div>
  <div class="flex items-center gap-1.5">
    <h2 class="section-title">{title}</h2>
    {#if detail}
      <button
        type="button"
        class="-my-1 rounded-full p-1 transition-colors hover:bg-surface-2 hover:text-ink pointer-coarse:-my-2.5 pointer-coarse:p-2.5
          {open ? 'text-primary' : 'text-muted'}"
        aria-expanded={open}
        aria-controls={id}
        aria-label={t(open ? 'settings.hideAbout' : 'settings.moreAbout', { title })}
        onclick={() => (open = !open)}
      >
        <Info size={15} strokeWidth={1.75} />
      </button>
    {/if}
  </div>
  <p class="mt-1 max-w-[40ch] text-sm text-muted">{hint}</p>
  {#if open}
    <p {id} class="mt-2 max-w-[44ch] text-sm text-ink/80" transition:slide={{ duration: 180 }}>{detail}</p>
  {/if}
  {@render children?.()}
</div>
