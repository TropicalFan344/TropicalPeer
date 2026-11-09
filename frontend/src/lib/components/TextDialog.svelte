<script lang="ts">
  // Text shared from another app's share menu (Android): it can be edited,
  // then goes to the device chosen here.
  import { Plus } from '@lucide/svelte'
  import Dialog from './Dialog.svelte'
  import Bed from './Bed.svelte'
  import AddPeerDialog from './AddPeerDialog.svelte'
  import { app, sendText } from '../state.svelte'
  import { maxTextBytes, textBytes, type Peer } from '../api'
  import { formatSize } from '../format'
  import { t } from '../i18n.svelte'

  // Set before this opens and cleared to close it.
  let body = $state(app.sharedText ?? '')
  let to = $state<Peer | null>(null)
  let sending = $state(false)
  let error = $state('')
  let pairing = $state(false)

  const bytes = $derived(textBytes(body))
  const tooLong = $derived(bytes > maxTextBytes)
  const empty = $derived(body.trim() === '')

  function close() {
    app.sharedText = null
  }

  async function send(peer: Peer) {
    if (sending || empty || tooLong) return
    to = peer
    sending = true
    error = await sendText(peer, body)
    sending = false
    if (!error) close()
  }
</script>

{#if !pairing}
  <Dialog title={t('text.sharedTitle')} onclose={close}>
    <!-- Not focused: a phone's keyboard would cover the device list. -->
    <textarea
      class="field min-h-32 w-full resize-y leading-relaxed"
      rows="5"
      placeholder={t('text.placeholder')}
      aria-label={t('text.label')}
      aria-invalid={tooLong || !!error}
      bind:value={body}
      oninput={() => (error = '')}
    ></textarea>
    {#if tooLong}
      <p class="mt-2 text-sm text-bad" role="alert">{t('text.tooLong')} ({formatSize(bytes)} / {formatSize(maxTextBytes)})</p>
    {:else if error}
      <p class="mt-2 text-sm text-bad" role="alert">{error}</p>
    {/if}

    {#if app.peers.length}
      <h3 class="mt-5 mb-1 text-sm font-medium text-muted">{t('share.choose')}</h3>
      <ul class="-mx-2 flex flex-col" aria-label={t('share.choose')}>
        {#each app.peers as p (p.id)}
          <li>
            <button
              class="flex min-h-15 w-full items-center gap-3.5 rounded-md px-2 text-left hover:bg-surface-2 active:bg-surface-2 disabled:opacity-60"
              disabled={sending || empty || tooLong}
              onclick={() => send(p)}
            >
              <Bed name={p.name} id={p.id} size={40} />
              <span class="min-w-0 flex-1">
                <span class="display block truncate text-[17px] leading-tight font-medium">{p.name}</span>
                <span class="block text-sm text-muted">{sending && to?.id === p.id ? t('text.sending') : p.id}</span>
              </span>
            </button>
          </li>
        {/each}
      </ul>
    {:else}
      <p class="mt-5 text-muted">{t('text.pairFirst')}</p>
      <button class="btn-primary mt-4 w-full" onclick={() => (pairing = true)}>
        <Plus size={18} /> {t('peers.add')}
      </button>
    {/if}
  </Dialog>
{/if}

{#if pairing}
  <AddPeerDialog onclose={() => (pairing = false)} />
{/if}
