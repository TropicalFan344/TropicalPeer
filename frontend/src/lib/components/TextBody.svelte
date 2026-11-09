<script lang="ts">
  // A text from (or to) another device. Plain text only: pieces are output
  // as text, links as buttons that open the system browser. Never {@html}.
  import { linkPieces } from '../links'
  import { openUrl } from '../state.svelte'
  import { t } from '../i18n.svelte'

  let { body, lines = 0, tone = 'text-primary' }: { body: string; lines?: number; tone?: string } = $props()

  const pieces = $derived(linkPieces(body))
  let open = $state(false)
  let el = $state<HTMLElement>()
  let clipped = $state(false)

  // Only offer "Show all" when the clamp actually hides something.
  $effect(() => {
    void body
    if (!el || !lines || open) return
    const check = () => (clipped = el!.scrollHeight > el!.clientHeight + 1)
    check()
    const ro = new ResizeObserver(check)
    ro.observe(el)
    return () => ro.disconnect()
  })
</script>

<p
  bind:this={el}
  class="break-words whitespace-pre-wrap"
  style={lines && !open ? `display:-webkit-box;-webkit-box-orient:vertical;-webkit-line-clamp:${lines};overflow:hidden` : ''}
>
  {#each pieces as p, i (i)}{#if p.url}<button
        type="button"
        class="inline cursor-pointer text-left break-all underline underline-offset-2 {tone} hover:opacity-80"
        title={p.url}
        onclick={() => openUrl(p.url!)}>{p.text}</button
      >{:else}{p.text}{/if}{/each}
</p>
{#if lines && (clipped || open)}
  <button type="button" class="mt-1 text-sm font-medium underline underline-offset-2 {tone}" onclick={() => (open = !open)}>
    {open ? t('text.showLess') : t('text.showAll')}
  </button>
{/if}
