package locales

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

var placeholder = regexp.MustCompile(`\{(\w+)\}`)

func placeholders(m message) string {
	set := map[string]bool{}
	texts := []string{m.text}
	for _, s := range m.plural {
		texts = append(texts, s)
	}
	for _, t := range texts {
		for _, p := range placeholder.FindAllStringSubmatch(t, -1) {
			set[p[1]] = true
		}
	}
	// {count} is implied by every plural form even where a language omits it.
	if m.plural != nil {
		set["count"] = true
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// requiredPlurals are the CLDR categories each shipped language needs.
var requiredPlurals = map[string][]string{
	"zh": {"other"}, "ja": {"other"}, "ko": {"other"},
	"ru": {"one", "few", "many", "other"},
}

// Every language must have exactly English's keys, the same placeholders
// (a translated sentence that drops {name} would show nothing there), and
// the plural forms its grammar needs.
func TestLanguagesComplete(t *testing.T) {
	loadOnce.Do(load)
	ref := catalogs[Fallback]
	if len(ref) < 100 {
		t.Fatalf("en.json has only %d keys", len(ref))
	}
	if len(catalogs) < 2 {
		t.Fatalf("only %d language files", len(catalogs))
	}
	for lang, cat := range catalogs {
		if lang == Fallback {
			continue
		}
		base, _, _ := strings.Cut(lang, "-")
		for key, want := range ref {
			got, ok := cat[key]
			if !ok {
				t.Errorf("%s: missing %q", lang, key)
				continue
			}
			if (want.plural == nil) != (got.plural == nil) {
				t.Errorf("%s: %q should be %s", lang, key, map[bool]string{true: "a plain string", false: "plural forms"}[want.plural == nil])
				continue
			}
			if placeholders(want) != placeholders(got) {
				t.Errorf("%s: %q has placeholders {%s}, English has {%s}", lang, key, placeholders(got), placeholders(want))
			}
			if got.plural != nil {
				need := requiredPlurals[base]
				if need == nil {
					need = []string{"one", "other"}
				}
				for _, c := range need {
					if _, ok := got.plural[c]; !ok {
						t.Errorf("%s: %q lacks the %q plural form", lang, key, c)
					}
				}
			}
			if got.plural == nil && strings.TrimSpace(got.text) == "" {
				t.Errorf("%s: %q is empty", lang, key)
			}
		}
		for key := range cat {
			if _, ok := ref[key]; !ok {
				t.Errorf("%s: %q isn't in en.json", lang, key)
			}
		}
	}
}

func TestT(t *testing.T) {
	if got := T("en", "peer.removeTitle", map[string]any{"name": "Moon PC"}); got != "Remove Moon PC?" {
		t.Errorf("fill: %q", got)
	}
	if got := T("en", "common.files", map[string]any{"count": 1}); got != "1 file" {
		t.Errorf("plural one: %q", got)
	}
	if got := T("en", "common.files", map[string]any{"count": 3}); got != "3 files" {
		t.Errorf("plural other: %q", got)
	}
	if got := T("xx", "nav.peers", nil); got != "Peers" {
		t.Errorf("unknown language should fall back to English: %q", got)
	}
	if got := T("en", "no.such.key", nil); got != "no.such.key" {
		t.Errorf("unknown key: %q", got)
	}
	if got := T("en", "pair.expiresIn", nil); got != "Code expires in {time}" {
		t.Errorf("missing param stays visible: %q", got)
	}
	cases := map[float64]string{1: "one", 2: "few", 5: "many", 11: "many", 21: "one", 22: "few"}
	for n, want := range cases {
		if got := pluralCategory("ru", n); got != want {
			t.Errorf("ru %v: %s, want %s", n, got, want)
		}
	}
}
