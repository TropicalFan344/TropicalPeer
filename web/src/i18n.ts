// The page's text, from the "page" section of the app's language files
// (locales/*.json). Only the language in use is loaded.

type Message = string | Record<string, string> // plain, or plural forms by CLDR category
type Section = Record<string, Message>

const loaders = import.meta.glob<Section>('../../locales/*.json', { import: 'page' })
const codes = Object.keys(loaders).map((p) => p.split('/').pop()!.replace(/\.json$/, ''))

let lang = 'en'
let strings: Section = {}
let english: Section = {}

/** Picks the closest language for the browser's preferences (as the app does). */
function pick(): string {
  for (const raw of navigator.languages ?? [navigator.language]) {
    const tag = raw.toLowerCase()
    const exact = codes.find((c) => c.toLowerCase() === tag)
    if (exact) return exact
    const [base, ...rest] = tag.split('-')
    if (base === 'zh') {
      // Traditional for Taiwan, Hong Kong, Macau and explicit Hant; else Simplified.
      return rest.some((p) => p === 'hant' || p === 'tw' || p === 'hk' || p === 'mo') ? 'zh-TW' : 'zh-CN'
    }
    const sameBase = codes.find((c) => c.toLowerCase().split('-')[0] === base)
    if (sameBase) return sameBase
  }
  return 'en'
}

const load = (code: string) => loaders[`../../locales/${code}.json`]?.() ?? Promise.resolve({})

/** Loads the language; call once before t. */
export async function initI18n() {
  lang = pick()
  ;[english, strings] = await Promise.all([load('en'), lang === 'en' ? Promise.resolve({}) : load(lang)])
  if (lang === 'en') strings = english
  document.documentElement.lang = lang
}

const rules = new Map<string, Intl.PluralRules>()

/** The text for key with {placeholders} filled; plural forms pick by params.count. */
export function t(key: string, params: Record<string, string | number> = {}): string {
  let l = lang
  let msg = strings[key]
  if (msg === undefined) {
    l = 'en'
    msg = english[key] ?? key
  }
  let text: string
  if (typeof msg === 'string') text = msg
  else {
    let r = rules.get(l)
    if (!r) rules.set(l, (r = new Intl.PluralRules(l)))
    const n = typeof params.count === 'number' ? params.count : NaN
    text = (Number.isNaN(n) ? msg.other : msg[r.select(n)]) ?? msg.other ?? ''
  }
  return text.replace(/\{(\w+)\}/g, (whole, name: string) => (name in params ? String(params[name]) : whole))
}
