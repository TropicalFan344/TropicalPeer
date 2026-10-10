// Package locales holds TropicalPeer's translations: one JSON file per
// language (en.json, zh-TW.json, …), shared by the Svelte frontend
// (frontend/src/lib/i18n.svelte.ts), the Go app layer (error messages and
// notices) and the Android notifications (through package mobile).
//
// A file is nested groups of strings. A string may hold {placeholders}; a
// plural string is an object keyed by CLDR category ("one", "other", and
// "zero", "two", "few", "many" where a language needs them) and gets its
// number as {count}. en.json is the reference: a key missing elsewhere falls
// back to English. See README.md for adding a language.
package locales

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"
)

//go:embed *.json
var files embed.FS

// Fallback is the reference language, complete by definition.
const Fallback = "en"

// A message is either a plain string or plural forms by category.
type message struct {
	text   string
	plural map[string]string
}

var (
	loadOnce sync.Once
	catalogs map[string]map[string]message // language → flat key → message
	names    map[string]string             // language → its own name
)

func load() {
	catalogs = map[string]map[string]message{}
	names = map[string]string{}
	entries, _ := fs.Glob(files, "*.json")
	for _, e := range entries {
		lang := strings.TrimSuffix(e, ".json")
		data, err := files.ReadFile(e)
		if err != nil {
			continue
		}
		var tree map[string]any
		if err := json.Unmarshal(data, &tree); err != nil {
			panic(fmt.Sprintf("locales/%s: %v", e, err))
		}
		flat := map[string]message{}
		flatten("", tree, flat)
		catalogs[lang] = flat
		names[lang] = flat["language"].text
	}
}

var pluralCategories = map[string]bool{"zero": true, "one": true, "two": true, "few": true, "many": true, "other": true}

func flatten(prefix string, tree map[string]any, out map[string]message) {
	for k, v := range tree {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch v := v.(type) {
		case string:
			out[key] = message{text: v}
		case map[string]any:
			if isPlural(v) {
				m := message{plural: map[string]string{}}
				for cat, s := range v {
					m.plural[cat], _ = s.(string)
				}
				out[key] = m
			} else {
				flatten(key, v, out)
			}
		}
	}
}

func isPlural(m map[string]any) bool {
	if _, ok := m["other"]; !ok {
		return false
	}
	for k, v := range m {
		if _, isStr := v.(string); !isStr || !pluralCategories[k] {
			return false
		}
	}
	return true
}

// Languages lists the available language codes with their own names.
func Languages() map[string]string {
	loadOnce.Do(load)
	out := make(map[string]string, len(names))
	for k, v := range names {
		out[k] = v
	}
	return out
}

// Codes lists the available language codes, sorted.
func Codes() []string {
	loadOnce.Do(load)
	out := make([]string, 0, len(catalogs))
	for k := range catalogs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Supported reports whether lang has a file.
func Supported(lang string) bool {
	loadOnce.Do(load)
	_, ok := catalogs[lang]
	return ok
}

// T returns the message for key in lang with params filled in, falling back
// to English, then to the key itself. A "count" param picks the plural form.
func T(lang, key string, params map[string]any) string {
	loadOnce.Do(load)
	m, ok := catalogs[lang][key]
	if !ok {
		lang = Fallback
		m, ok = catalogs[Fallback][key]
		if !ok {
			return key
		}
	}
	text := m.text
	if m.plural != nil {
		cat := "other"
		if n, isNum := toFloat(params["count"]); isNum {
			cat = pluralCategory(lang, n)
		}
		if s, ok := m.plural[cat]; ok {
			text = s
		} else {
			text = m.plural["other"]
		}
	}
	return fill(text, params)
}

func fill(text string, params map[string]any) string {
	if len(params) == 0 || !strings.Contains(text, "{") {
		return text
	}
	var b strings.Builder
	for {
		i := strings.IndexByte(text, '{')
		if i < 0 {
			break
		}
		j := strings.IndexByte(text[i:], '}')
		if j < 0 {
			break
		}
		name := text[i+1 : i+j]
		b.WriteString(text[:i])
		if v, ok := params[name]; ok {
			fmt.Fprint(&b, v)
		} else {
			b.WriteString(text[i : i+j+1])
		}
		text = text[i+j+1:]
	}
	b.WriteString(text)
	return b.String()
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

// pluralCategory covers the languages shipped here; the frontend uses the
// browser's Intl.PluralRules, which agrees for these.
func pluralCategory(lang string, n float64) string {
	base, _, _ := strings.Cut(lang, "-")
	whole := n == float64(int64(n))
	i := int64(n)
	switch base {
	case "zh", "ja", "ko":
		return "other"
	case "fr", "pt":
		if whole && (i == 0 || i == 1) {
			return "one"
		}
		return "other"
	case "ru":
		if !whole {
			return "other"
		}
		switch {
		case i%10 == 1 && i%100 != 11:
			return "one"
		case i%10 >= 2 && i%10 <= 4 && (i%100 < 12 || i%100 > 14):
			return "few"
		default:
			return "many"
		}
	default: // en, de, es
		if whole && i == 1 {
			return "one"
		}
		return "other"
	}
}
