<script lang="ts">
  // The signature move: transfer progress laid as Copacabana wave pavement.
  // The laid length is the share of bytes moved, never decoration. While
  // connecting or waiting the whole band drifts faintly; while bytes flow the
  // laid waves run forward; paused, everything holds still.
  let {
    value = 0,
    waiting = false,
    flowing = false,
    height = 12,
    label,
  }: { value?: number; waiting?: boolean; flowing?: boolean; height?: number; label: string } = $props()

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
    <div class="absolute inset-0 overflow-hidden opacity-35">
      <div class="wave-fill wave-run wave-drift"></div>
    </div>
  {:else}
    <!-- Clipped rather than resized, so the waves keep their shape as it grows. -->
    <div class="wave-laid-length absolute inset-0 overflow-hidden" style="clip-path: inset(0 {100 - pct}% 0 0)">
      <div class="wave-fill wave-run {flowing && pct < 100 ? 'wave-flow' : ''}"></div>
    </div>
    {#if pct > 0 && pct < 100}
      <div class="wave-edge absolute inset-0" style="transform: translateX({pct - 100}%)">
        <div class="wave-laid absolute inset-y-0 right-0"></div>
      </div>
    {/if}
  {/if}
</div>
