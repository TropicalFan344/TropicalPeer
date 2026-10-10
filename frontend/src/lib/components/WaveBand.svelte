<script lang="ts">
  // The signature move: transfer progress laid as Copacabana wave pavement.
  // The filled length is the share of bytes moved, never decoration; while
  // connecting or waiting the whole band drifts instead.
  let {
    value = 0,
    waiting = false,
    height = 12,
    label,
  }: { value?: number; waiting?: boolean; height?: number; label: string } = $props()

  const pct = $derived(Math.max(0, Math.min(1, value)) * 100)
</script>

<div
  class="wave-track relative w-full overflow-hidden rounded-full"
  style="height:{height}px"
  role="progressbar"
  aria-label={label}
  aria-valuemin="0"
  aria-valuemax="100"
  aria-valuenow={waiting ? undefined : Math.round(pct)}
>
  {#if waiting}
    <div class="wave-fill wave-drift absolute inset-0 opacity-35"></div>
  {:else}
    <div class="absolute inset-y-0 left-0 transition-[width] duration-300 ease-out" style="width:{pct}%">
      <div class="wave-fill absolute inset-0"></div>
      {#if pct > 0 && pct < 100}<div class="wave-laid absolute inset-y-0 right-0"></div>{/if}
    </div>
  {/if}
</div>
