<script lang="ts">
  // A text in the Transfers list, sent or received this session.
  import { ArrowUpRight, ArrowDownLeft, Copy, Route } from '@lucide/svelte'
  import Bed from './Bed.svelte'
  import TextBody from './TextBody.svelte'
  import { copyText } from '../state.svelte'
  import type { TextMsg } from '../api'
  import { t, timeOfDay } from '../i18n.svelte'

  let { text }: { text: TextMsg } = $props()

  const sending = $derived(text.direction === 'send')
  const time = $derived(timeOfDay(text.at))
</script>

<div class="flex items-start gap-4 px-5 py-4 max-sm:flex-wrap max-sm:gap-x-3.5 max-sm:gap-y-2 max-sm:px-4">
  <Bed name={text.peerName} id={text.peerId} size={40} />
  <div class="min-w-0 flex-1">
    <div class="flex items-center gap-2">
      {#if sending}
        <ArrowUpRight size={16} class="shrink-0 text-primary" aria-label={t('transfer.sent')} />
      {:else}
        <ArrowDownLeft size={16} class="shrink-0 text-sea" aria-label={t('transfer.received')} />
      {/if}
      <span class="truncate font-medium">{t(sending ? 'text.rowSent' : 'text.rowReceived', { name: text.peerName })}</span>
      <span class="shrink-0 text-sm text-muted tabular-nums">{time}</span>
      {#if text.relayed}
        <span class="flex shrink-0 items-center gap-1 text-sm text-muted" title={t('transfer.relayedTip')}>
          <Route size={13} strokeWidth={1.75} /> {t('transfer.relayed')}
        </span>
      {/if}
    </div>
    <div class="mt-1 text-ink select-text">
      <TextBody body={text.body} lines={3} />
    </div>
  </div>
  <div class="flex shrink-0 items-center gap-1 self-center max-sm:w-full max-sm:pl-11">
    <button class="btn-quiet" onclick={() => copyText(text)}><Copy size={16} /> {t('text.copy')}</button>
  </div>
</div>
