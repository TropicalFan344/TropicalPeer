// Translations, from the shared language files in /locales (one JSON file per
// language; see locales/README.md). t('group.key', { name }) returns the
// current language's string with {placeholders} filled, falling back to
// English. Reading the language here is reactive, so templates and $derived
// values re-render when it changes.
import { call } from './backend'

type Tree = { [k: string]: string | Tree }
type Message = string | Record<string, string> // plain, or plural forms by CLDR category

const PLURAL = new Set(['zero', 'one', 'two', 'few', 'many', 'other'])

// English is built in (it's every other language's fallback); the rest load
// when they're used, so the bundle doesn't carry ten languages to show one.
// Each file's own name ("Deutsch") is all Settings needs up front.
const codeOf = (path: string) => path.split('/').pop()!.replace(/\.json$/, '')
const english = import.meta.glob<Tree>('../../../locales/en.json', { eager: true, import: 'default' })
const later = import.meta.glob<Tree>(['../../../locales/*.json', '!../../../locales/en.json'], { query: '?strings', import: 'default' })
const ownNames = import.meta.glob<string>('../../../locales/*.json', { eager: true, import: 'language' })

const catalogs: Record<string, Record<string, Message>> = {}
const names: Record<string, string> = {}
const codes: string[] = []
for (const [path, name] of Object.entries(ownNames)) {
  const code = codeOf(path)
  codes.push(code)
  names[code] = name ?? code
}
for (const [path, tree] of Object.entries(english)) catalogs[codeOf(path)] = flat(tree)

function flat(tree: Tree): Record<string, Message> {
  const out: Record<string, Message> = {}
  flatten('', tree, out)
  return out
}

/** Loads a language's strings (once). English is always there. */
export async function loadLanguage(code: string): Promise<void> {
  if (catalogs[code]) return
  const path = Object.keys(later).find((p) => codeOf(p) === code)
  if (!path) return
  try {
    catalogs[code] = flat(await later[path]!())
  } catch {
    // Unreadable: t() falls back to English.
  }
}

function flatten(prefix: string, tree: Tree, out: Record<string, Message>) {
  for (const [k, v] of Object.entries(tree)) {
    const key = prefix ? `${prefix}.${k}` : k
    if (typeof v === 'string') out[key] = v
    else if ('other' in v && Object.keys(v).every((c) => PLURAL.has(c) && typeof v[c] === 'string')) {
      out[key] = v as Record<string, string>
    } else flatten(key, v, out)
  }
}

/** Available languages, English first, then by their own names. */
export const languages = [...codes]
  .sort((a, b) => (a === 'en' ? -1 : b === 'en' ? 1 : names[a]!.localeCompare(names[b]!)))
  .map((code) => ({ code, name: names[code]! }))

const KEY = 'tropicalpeer.lang'

/** Picks the closest language file for the system's preferences. */
function systemLanguage(): string {
  const prefs = typeof navigator !== 'undefined' ? navigator.languages : []
  for (const raw of prefs) {
    const tag = raw.toLowerCase()
    const exact = codes.find((c) => c.toLowerCase() === tag)
    if (exact) return exact
    const [base, ...rest] = tag.split('-')
    if (base === 'zh') {
      // Traditional for Taiwan, Hong Kong, Macau and explicit Hant; else Simplified.
      const trad = rest.some((p) => p === 'hant' || p === 'tw' || p === 'hk' || p === 'mo')
      const pick = trad ? 'zh-TW' : 'zh-CN'
      if (codes.includes(pick)) return pick
    }
    const sameBase = codes.find((c) => c.toLowerCase().split('-')[0] === base)
    if (sameBase) return sameBase
  }
  return 'en'
}

function stored(): string {
  try {
    const v = localStorage.getItem(KEY)
    if (v && codes.includes(v)) return v
  } catch {
    // Storage unavailable: follow the system.
  }
  return 'system'
}

export const i18n = $state({
  /** The setting: 'system' or a language code. */
  choice: stored(),
  /** The language in use. */
  lang: 'en',
})
i18n.lang = i18n.choice === 'system' ? systemLanguage() : i18n.choice

/** Changes the language (once its strings have loaded) and remembers the choice on this device. */
export async function setLanguage(choice: string) {
  i18n.choice = choice
  const lang = choice === 'system' ? systemLanguage() : choice
  await loadLanguage(lang)
  if (i18n.choice !== choice) return // chosen again meanwhile
  i18n.lang = lang
  try {
    if (choice === 'system') localStorage.removeItem(KEY)
    else localStorage.setItem(KEY, choice)
  } catch {
    // Not persisted; still applied for this session.
  }
  applyLanguage()
}

/** Tells the page and the Go side which language is in use. */
export function applyLanguage() {
  document.documentElement.lang = i18n.lang
  call('SetLanguage', i18n.lang).catch(() => {})
}

const pluralRules = new Map<string, Intl.PluralRules>()
function category(lang: string, n: number) {
  let r = pluralRules.get(lang)
  if (!r) pluralRules.set(lang, (r = new Intl.PluralRules(lang)))
  return r.select(n)
}

export type Params = Record<string, string | number>

/** A time of day, such as 3:05 PM or 15:05, written the current language's way. */
export function timeOfDay(ms: number): string {
  return new Date(ms).toLocaleTimeString(i18n.lang, { hour: 'numeric', minute: '2-digit' })
}

/** The current language's text for key, with {placeholders} filled from params. */
export function t(key: string, params: Params = {}): string {
  let lang = i18n.lang
  let msg = catalogs[lang]?.[key]
  if (msg === undefined) {
    lang = 'en'
    msg = catalogs.en?.[key]
    if (msg === undefined) return key
  }
  let text: string
  if (typeof msg === 'string') text = msg
  else {
    const n = typeof params.count === 'number' ? params.count : NaN
    text = msg[Number.isNaN(n) ? 'other' : category(lang, n)] ?? msg.other ?? ''
  }
  return text.replace(/\{(\w+)\}/g, (whole, name: string) => (name in params ? String(params[name]) : whole))
}

/** Splits a message around one placeholder, for wrapping that part in markup. */
export function tSplit(key: string, name: string, params: Params = {}): [string, string] {
  const marker = '\u0000'
  const [before = '', after = ''] = t(key, { ...params, [name]: marker }).split(marker)
  return [before, after]
}

/** "photo.jpg", or "3 files". */
export function what(files: { name: string }[]): string {
  return files.length === 1 ? files[0]!.name : t('common.files', { count: files.length })
}
