// Plain-language wording for network states, shared by the Network page and
// Settings. Technical terms stay in tooltips (PRODUCT.md principle 5).
import { t } from './i18n.svelte'

/** Turns the backend's port-forwarding failure into a sentence a person can act on. */
export function forwardingProblem(mappingErr: string): string {
  if (mappingErr.includes('carrier-grade')) return t('network.problemCgnat')
  if (mappingErr.includes("doesn't offer")) return t('network.problemNone')
  return t('network.problemRefused')
}

/** Splits "UPnP 203.0.113.7:7778" into method and address. */
export function splitForwarded(forwarded: string): { method: string; addr: string } {
  const [method = '', addr = ''] = forwarded.split(' ')
  return { method, addr }
}
