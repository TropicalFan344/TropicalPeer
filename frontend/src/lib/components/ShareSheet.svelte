<script lang="ts">
  // Files shared from another app's share menu (Android): one question, which
  // device gets them. Choosing one starts the send and shows Transfers.
  import { Plus, Link2 } from '@lucide/svelte'
  import Dialog from './Dialog.svelte'
  import Bed from './Bed.svelte'
  import AddPeerDialog from './AddPeerDialog.svelte'
  import { app, sendTo, shareLink } from '../state.svelte'
  import { formatSize } from '../format'
  import type { Peer, PickedFile } from '../api'
  import { t, what as describe } from '../i18n.svelte'

  const files = $derived(app.shared.filter((f): f is Exclude<PickedFile, string> => typeof f !== 'string'))
  const what = $derived(describe(files))
  const total = $derived(files.every((f) => f.size >= 0) ? files.reduce((n, f) => n + f.size, 0) : -1)

  let pairing = $state(false)

  function close() {
    app.shared = []
  }

  // Or a link, for someone who doesn't have the app.
  async function byLink() {
    const chosen = app.shared
    close()
    app.page = 'links'
    await shareLink(chosen)
  }

  async function send(peer: Peer) {
    const chosen = app.shared
    close()
    app.page = 'transfers'
    await sendTo(peer, chosen)
  }
</script>

{#if app.shared.length && !pairing}
  <Dialog title={t('share.send', { what })} onclose={close}>
    {#if total >= 0}
      <p class="-mt-2 mb-3 text-sm text-muted">{formatSize(total)}</p>
    {/if}
    {#if app.linksAvailable}
      <button
        class="-mx-2 mb-1 flex min-h-15 w-[calc(100%+1rem)] items-center gap-3.5 rounded-md px-2 text-left hover:bg-surface-2 active:bg-surface-2"
        onclick={byLink}
      >
        <span class="flex size-10 shrink-0 items-center justify-center rounded-full bg-primary-soft text-primary"><Link2 size={20} /></span>
        <span class="min-w-0 flex-1">
          <span class="display block text-[17px] leading-tight font-medium">{t('links.shareByLink')}</span>
          <span class="block text-xs text-muted">{t('links.shareByLinkHint')}</span>
        </span>
      </button>
    {/if}
    {#if app.peers.length}
      <ul class="-mx-2 flex flex-col" aria-label={t('share.choose')}>
        {#each app.peers as peer (peer.id)}
          <li>
            <button
              class="flex min-h-15 w-full items-center gap-3.5 rounded-md px-2 text-left hover:bg-surface-2 active:bg-surface-2"
              onclick={() => send(peer)}
            >
              <Bed name={peer.name} id={peer.id} size={40} />
              <span class="min-w-0 flex-1">
                <span class="display block truncate text-[17px] leading-tight font-medium">{peer.name}</span>
                <span class="block font-mono text-[11px] text-muted">{peer.id}</span>
              </span>
            </button>
          </li>
        {/each}
      </ul>
    {:else}
      <p class="text-muted">{t('share.pairFirst')}</p>
      <button class="btn-primary mt-5 w-full" onclick={() => (pairing = true)}>
        <Plus size={18} /> {t('peers.add')}
      </button>
    {/if}
  </Dialog>
{/if}

{#if pairing}
  <AddPeerDialog onclose={() => (pairing = false)} />
{/if}
