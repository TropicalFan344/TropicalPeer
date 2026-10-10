# Languages

Every piece of text TropicalPeer shows lives here, one JSON file per language.
The same files feed the desktop and phone UI (`frontend/src/lib/i18n.svelte.ts`),
the Go app layer's messages and errors (`locales.go`), and Android's
notifications (through `mobile.Text`).

| File | Language |
|---|---|
| `en.json` | English (the reference; every other file mirrors its keys) |
| `zh-TW.json` | 繁體中文 |
| `zh-CN.json` | 简体中文 |
| `ja.json` | 日本語 |
| `ko.json` | 한국어 |
| `es.json` | Español |
| `fr.json` | Français |
| `de.json` | Deutsch |
| `pt-BR.json` | Português (Brasil) |
| `ru.json` | Русский |

## Format

- Strings are grouped by screen (`peers`, `settings`, `network`, …). Code
  refers to them by dotted key: `t('settings.deviceName')`.
- `"language"` is the language's own name, shown in Settings → Language.
- `{name}` placeholders are filled in by the app. Keep every placeholder
  from the English string; move it wherever the sentence needs it.
- Counted strings are objects keyed by plural category. English has
  `one`/`other`; Chinese, Japanese and Korean only need `other`; Russian
  needs `one`, `few`, `many` and `other`. The number is `{count}`.

```json
"files": { "one": "{count} file", "other": "{count} files" }
```

## Changing a translation

Edit the string and rebuild. Missing keys fall back to English, so a
half-finished file never shows blanks.

## Adding a language

1. Copy `en.json` to `<code>.json`, using a BCP 47 code such as `it` or
   `pt-PT`.
2. Set `"language"` to the language's own name and translate the strings.
3. Run `go test ./locales`. It fails on missing or extra keys, changed
   placeholders, or missing plural forms, and says exactly where. A new
   plural rule (Arabic, Polish, …) also needs a case in `pluralCategory`
   in `locales.go` and in `requiredPlurals` in `locales_test.go`.
4. Rebuild the apps. The new file appears in Settings → Language, and
   "Match system" picks it for people whose system uses that language.

Keep the product's voice: plain, friendly and specific. Errors say what
happened and what to do next. Leave `TropicalPeer`, `UPnP`, `NAT-PMP`,
`UDP` and the example code `7-orchid-tiger` as they are.
