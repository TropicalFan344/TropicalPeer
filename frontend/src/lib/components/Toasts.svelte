<script lang="ts">
  import { fly } from 'svelte/transition'
  import { CircleCheck, CircleAlert, Info, X } from '@lucide/svelte'
  import { toasts, dismissToast } from '../state.svelte'
  import { t as tr } from '../i18n.svelte'

  const icons = { success: CircleCheck, error: CircleAlert, info: Info }
  const tones = { success: 'text-ok', error: 'text-bad', info: 'text-sea' }

  // Phone: notifications drop in from the top, where Android puts its own.
  const phone = typeof matchMedia !== 'undefined' && matchMedia('(max-width: 639.98px)').matches
</script>

<!-- Desktop: bottom-right. Phone: full width at the top, under the status bar
     and below any offer strip (--offer-h), newest first. -->
<div
  class="pointer-events-none fixed right-6 bottom-6 z-50 flex w-[22rem] flex-col gap-2
    max-sm:inset-x-3 max-sm:top-[calc(var(--sat)+var(--offer-h,0px)+12px)] max-sm:bottom-auto max-sm:w-auto max-sm:flex-col-reverse"
  aria-live="polite"
>
  {#each toasts as t (t.id)}
    {@const Icon = icons[t.kind]}
    <div
      class="pointer-events-auto flex items-start gap-3 rounded-lg border border-line bg-surface p-3.5 shadow-float"
      transition:fly={{ y: phone ? -12 : 12, duration: 220, easing: (x) => 1 - Math.pow(2, -10 * x) }}
    >
      <Icon size={18} class="mt-px shrink-0 {tones[t.kind]}" />
      <div class="min-w-0 flex-1 text-sm">
        {t.text}
        {#if t.action}
          <button
            class="mt-1.5 block font-medium text-primary hover:underline max-sm:-my-1.5 max-sm:py-1.5"
            onclick={() => {
              t.action?.run()
              dismissToast(t.id)
            }}
          >
            {t.action.label}
          </button>
        {/if}
      </div>
      <button class="rounded p-0.5 text-muted hover:text-ink max-sm:-m-2 max-sm:p-2" aria-label={tr('common.dismiss')} onclick={() => dismissToast(t.id)}>
        <X size={16} />
      </button>
    </div>
  {/each}
</div>
