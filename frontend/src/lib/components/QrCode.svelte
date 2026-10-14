<script lang="ts">
  // A QR code from the module rows Go computes (internal/appcore/links.go).
  // It stays dark on pale stone in both themes: scanners expect dark modules
  // on a light ground, and the night garden's inverse reads poorly.
  let { rows, size = 168, label }: { rows: string[]; size?: number; label: string } = $props()

  const quiet = 3 // modules of margin; the pale frame around it adds the rest
  const n = $derived(rows.length + quiet * 2)
  const path = $derived.by(() => {
    let d = ''
    rows.forEach((row, y) => {
      let x = 0
      while (x < row.length) {
        if (row[x] !== '1') {
          x++
          continue
        }
        let w = 1
        while (row[x + w] === '1') w++
        d += `M${x + quiet} ${y + quiet}h${w}v1h-${w}z`
        x += w
      }
    })
    return d
  })
</script>

<svg
  width={size}
  height={size}
  viewBox="0 0 {n} {n}"
  shape-rendering="crispEdges"
  role="img"
  aria-label={label}
  class="block shrink-0"
>
  <rect width={n} height={n} fill="#fbfcf9" />
  <path d={path} fill="#17211c" />
</svg>
