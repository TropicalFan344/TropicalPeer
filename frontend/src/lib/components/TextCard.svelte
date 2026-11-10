<script lang="ts">
  import { dur } from '../motion'
  // A received text nobody has seen yet, at the top of every page (inside
  // the offer strip). Calmer than an offer: it waits, and nothing expires.
  import { slide } from 'svelte/transition'
  import { Copy } from '@lucide/svelte'
  import Bed from './Bed.svelte'
  import TextBody from './TextBody.svelte'
  import { copyText, markTextRead } from '../state.svelte'
  import type { TextMsg } from '../api'
  import { t, timeOfDay } from '../i18n.svelte'

  let { text }: { text: TextMsg } = $props()

  const time = $derived(timeOfDay(text.at))
</script>

<section
  class="flex items-start gap-5 bg-primary-soft px-10 py-4 text-ink max-sm:flex-wrap max-sm:gap-x-3.5 max-sm:gap-y-3 max-sm:px-4"
  aria-label={t('text.fromLabel', { name: text.peerName })}
  transition:slide={{ duration: dur(220) }}
>
  <Bed name={text.peerName} id={text.peerId} size={44} />
  <div class="min-w-0 flex-1">
    <h2 class="flex items-baseline gap-3 text-[17px] leading-snug font-medium">
      <span class="min-w-0 truncate">{t('text.fromTitle', { name: text.peerName })}</span>
      <span class="shrink-0 text-sm font-normal text-muted tabular-nums">{time}</span>
    </h2>
    <div class="mt-1 max-h-[40vh] overflow-y-auto select-text">
      <TextBody body={text.body} lines={6} />
    </div>
  </div>
  <div class="flex gap-2 self-center max-sm:w-full max-sm:gap-3">
    <button
      class="btn-secondary whitespace-nowrap max-sm:min-h-11 max-sm:flex-1"
      onclick={() => markTextRead(text)}
    >
      {t('text.dismiss')}
    </button>
    <button class="btn-primary whitespace-nowrap max-sm:min-h-11 max-sm:flex-1" onclick={() => copyText(text)}>
      <Copy size={16} />
      {t('text.copy')}
    </button>
  </div>
</section>
