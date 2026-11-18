export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = bytes / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v >= 100 || Number.isInteger(v) ? Math.round(v) : v.toFixed(1)} ${units[i]}`
}

/** "2 hr. ago", in lang; '' under a minute. */
export function formatAgo(ms: number, lang: string, now = Date.now()): string {
  const mins = Math.floor((now - ms) / 60_000)
  if (mins < 1) return ''
  const [n, unit]: [number, Intl.RelativeTimeFormatUnit] =
    mins < 60 ? [mins, 'minute'] : mins < 47.5 * 60 ? [Math.round(mins / 60), 'hour'] : [Math.round(mins / 1440), 'day']
  return new Intl.RelativeTimeFormat(lang, { style: 'short', numeric: 'auto' }).format(-n, unit)
}

/** Whether opening a file of this name can run something on the device. */
export const runsCode = (name: string) =>
  /\.(exe|msi|msix|appx|bat|cmd|com|scr|pif|cpl|ps1|psm1|vbs|vbe|js|jse|wsf|wsh|hta|jar|lnk|reg|dll|sh|command|app|pkg|dmg|apk|xapk|deb|rpm)$/i.test(name.trim())

export const MB = 1024 * 1024
export const GB = 1024 * MB
