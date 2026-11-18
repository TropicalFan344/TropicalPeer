<script lang="ts">
  // What someone on a link wants to send, every name in full: the name and
  // size are all there is to judge a stranger's files by. Programs say so in
  // words, since a file's type is easy to miss at the end of a long name.
  import { TriangleAlert } from '@lucide/svelte'
  import { formatSize, runsCode } from '../format'
  import { t } from '../i18n.svelte'

  let { files, muted = 'text-muted' }: { files: { name: string; size: number }[]; muted?: string } = $props()
</script>

<ul class="max-h-44 overflow-y-auto text-sm">
  {#each files as f, i (i)}
    <li class="flex items-baseline gap-3 py-0.5">
      <span class="min-w-0 flex-1 [overflow-wrap:anywhere]">
        {f.name}
        {#if runsCode(f.name)}
          <span class="ml-1 inline-flex items-center gap-1 font-medium whitespace-nowrap">
            <TriangleAlert size={13} strokeWidth={2} aria-hidden="true" /> {t('offer.program')}
          </span>
        {/if}
      </span>
      <span class="shrink-0 tabular-nums {muted}">{formatSize(f.size)}</span>
    </li>
  {/each}
</ul>
