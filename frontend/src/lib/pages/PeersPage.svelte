<script lang="ts">
  import { Plus } from '@lucide/svelte'
  import { flip } from 'svelte/animate'
  import { fade, scale } from 'svelte/transition'
  import PeerCard from '../components/PeerCard.svelte'
  import AddPeerDialog from '../components/AddPeerDialog.svelte'
  import { app, toast } from '../state.svelte'
  import { drag } from '../drop.svelte'
  import { api, errorText, isAndroid, isMac, type Peer } from '../api'
  import { sortable } from '../sortable'
  import { dur, easeOutExpo } from '../motion'
  import { t, tSplit } from '../i18n.svelte'

  const [emptyBefore, emptyAfter] = $derived(tSplit('peers.emptyBody', 'code'))

  let adding = $state(false)

  // Rearranging: while a card is held, this is the live order; otherwise the
  // saved one from Go.
  let held = $state<string[] | null>(null)
  const shown = $derived.by(() => {
    if (!held) return app.peers
    const byId = new Map(app.peers.map((p) => [p.id, p]))
    const list = held.map((id) => byId.get(id)).filter((p): p is Peer => !!p)
    return [...list, ...app.peers.filter((p) => !held!.includes(p.id))] // paired mid-drag
  })

  // The held card follows the pointer itself; only the others glide.
  const glide = (node: HTMLElement, rects: { from: DOMRect; to: DOMRect }) =>
    'lifted' in node.dataset ? { duration: 0 } : flip(node, rects, { duration: dur(300), easing: easeOutExpo })

  async function saveOrder(ids: string[]) {
    held = null
    // Show the new order at once; Go confirms it with peers:changed.
    const byId = new Map(app.peers.map((p) => [p.id, p]))
    app.peers = ids.map((id) => byId.get(id)).filter((p): p is Peer => !!p)
    try {
      await api.reorderPeers(ids)
    } catch (e) {
      toast(errorText(e), 'error')
      app.peers = await api.listPeers()
    }
  }
</script>

<div class="page">
  <header class="flex flex-wrap items-end justify-between gap-x-6 gap-y-4">
    <div>
      <h1 class="page-title">{t('peers.title')}</h1>
      <p class="mt-1 text-muted">
        {#if drag.active && app.peers.length}
          {t('peers.dropHint')}
        {:else if isAndroid}
          {t('peers.subtitleTouch')}
        {:else}
          {t(isMac ? 'peers.subtitleMac' : 'peers.subtitle')}
        {/if}
      </p>
    </div>
    <button class="btn-primary whitespace-nowrap" onclick={() => (adding = true)}>
      <Plus size={18} /> {t('peers.add')}
    </button>
  </header>

  {#if app.peers.length === 0}
    <section class="flex flex-col items-start gap-4 border-t border-line pt-8">
      <h2 class="text-xl font-medium">{t('peers.emptyTitle')}</h2>
      <p class="max-w-[60ch] text-muted">
        {emptyBefore}<span class="font-mono whitespace-nowrap text-ink">7-orchid-tiger</span>{emptyAfter}
      </p>
      <button class="btn-primary" onclick={() => (adding = true)}>
        <Plus size={18} /> {t('peers.add')}
      </button>
    </section>
  {:else}
    <section
      class="relative grid grid-cols-[repeat(auto-fill,minmax(19rem,1fr))] gap-3"
      aria-label={t('peers.list')}
      use:sortable={{
        ids: () => shown.map((p) => p.id),
        onmove: (ids) => (held = ids),
        ondrop: saveOrder,
        enabled: () => !drag.active,
      }}
    >
      {#each shown as peer (peer.id)}
        <!-- Cards make way for the one being moved, arrive gently when a
             device is paired, and fade when one is removed. -->
        <div
          class="slot relative"
          data-slot={peer.id}
          animate:glide
          in:scale={{ start: 0.96, duration: dur(240), easing: easeOutExpo }}
          out:fade={{ duration: dur(140) }}
        >
          <PeerCard {peer} />
        </div>
      {/each}
    </section>
  {/if}
</div>

{#if adding}
  <AddPeerDialog onclose={() => (adding = false)} />
{/if}
