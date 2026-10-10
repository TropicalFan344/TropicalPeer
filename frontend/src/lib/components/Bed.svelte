<script lang="ts">
  // A peer's garden bed: one flat colour and one organic outline, both picked
  // from the device ID so a device looks the same on every screen. The
  // outlines are hand-drawn, after the curving beds of Burle Marx's gardens.
  // edge: an outline colour, for beds laid on a coloured field (the offer
  // strip), where a heliconia bed would otherwise vanish into heliconia.
  let { name, id, size = 48, edge = '' }: { name: string; id: string; size?: number; edge?: string } = $props()

  const beds = [
    'M9 15C15 4 31 1 42 6C52 10 55 23 51 32C48 40 39 38 34 45C28 52 14 54 7 46C1 39 3 25 9 15Z',
    'M18 4C27 1 33 9 41 8C50 7 55 16 52 25C50 31 54 38 50 45C45 53 33 52 26 50C17 47 6 50 3 40C0 31 7 26 7 18C7 11 11 6 18 4Z',
    'M28 3C37 3 41 11 48 14C55 18 54 29 49 35C44 41 46 49 38 52C29 55 22 47 15 46C7 44 2 37 4 28C6 20 12 19 15 12C18 6 22 3 28 3Z',
    'M7 22C8 10 20 3 31 5C42 7 46 15 52 21C56 27 54 37 47 43C40 49 31 47 24 50C15 53 6 46 5 37C4 31 6 27 7 22Z',
  ]
  // Jungle, moss, ochre, heliconia, iresine, sea, silver-leaf: dark enough
  // for white initials (large text, 3:1+) in both themes.
  const colours = [1, 2, 3, 4, 5, 6, 7].map((i) => `var(--bed-${i})`)

  const hash = $derived.by(() => {
    let h = 0
    for (const c of id) h = (h * 31 + c.charCodeAt(0)) >>> 0
    return h
  })
  const initials = $derived(
    name
      .split(/[\s\-_]+/)
      .filter(Boolean)
      .slice(0, 2)
      .map((w) => w[0]!.toUpperCase())
      .join('') || '?',
  )
</script>

<svg width={size} height={size} viewBox="0 0 56 56" class="shrink-0 overflow-visible" aria-hidden="true">
  <path
    d={beds[hash % beds.length]}
    style="fill:{colours[(hash >>> 3) % colours.length]}{edge ? `;stroke:${edge};stroke-width:2.5;paint-order:stroke` : ''}"
  />
  <text
    x="28"
    y="29"
    text-anchor="middle"
    dominant-baseline="central"
    fill="#fff"
    font-family="Jost Variable, sans-serif"
    font-weight="560"
    font-size={initials.length > 1 ? 19 : 22}
  >
    {initials}
  </text>
</svg>
