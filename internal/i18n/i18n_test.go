package i18n

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"unicode"
)

func demoCatalog() fstest.MapFS {
	return fstest.MapFS{
		"catalog/demo.en.json": {Data: []byte(`{"hello":"Hello %s","only_en":"English only"}`)},
		"catalog/demo.ar.json": {Data: []byte(`{"hello":"مرحبا %s"}`)},
	}
}

func TestFallbackEnThenKey(t *testing.T) {
	b, err := Load(demoCatalog())
	if err != nil {
		t.Fatal(err)
	}
	if got := b.T("ar", "demo.hello", "x"); got != "مرحبا x" {
		t.Fatal(got)
	}
	if got := b.T("ar", "demo.only_en"); got != "English only" {
		t.Fatalf("want en fallback, got %q", got)
	}
	if got := b.T("ar", "demo.missing"); got != "demo.missing" {
		t.Fatalf("want key fallback, got %q", got)
	}
	if got := b.T("fr", "demo.hello", "y"); got != "Hello y" {
		t.Fatalf("unknown locale should fall back to en, got %q", got)
	}
}

var requiredCoreKeys = []string{
	"app.name", "nav.dashboard", "action.save", "action.cancel", "action.delete",
	"status.draft", "status.published", "error.not_found", "error.forbidden",
	"nav.group.content", "nav.group.structure", "nav.group.appearance",
	"nav.group.commerce", "nav.group.agents", "nav.group.system", "search",
}

func TestCoreCatalogLocalesHaveTheSameKeys(t *testing.T) {
	b, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"ar", "en"}, {"en", "ar"}} {
		for key := range b.messages[pair[0]] {
			if !strings.HasPrefix(key, "core.") {
				continue
			}
			if _, ok := b.lookup(pair[1], key); !ok {
				t.Errorf("%s is in the %s core catalog but missing in %s", key, pair[0], pair[1])
			}
		}
	}
}

func TestCoreCatalogKeysArePrefixedAndComplete(t *testing.T) {
	b, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range requiredCoreKeys {
		key := "core." + k
		for _, loc := range []string{"ar", "en"} {
			if _, ok := b.lookup(loc, key); !ok {
				t.Errorf("%s missing in %s catalog", key, loc)
			}
		}
	}
	if got := b.T("ar", "core.action.save"); !hasArabic(got) {
		t.Fatalf("core.action.save in ar should be Arabic, got %q", got)
	}
	if got := b.T("en", "core.action.save"); got != "Save" {
		t.Fatal(got)
	}
}

func hasArabic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Arabic, r) {
			return true
		}
	}
	return false
}

func TestLoadRejectsBadNameAndDuplicates(t *testing.T) {
	if _, err := Load(fstest.MapFS{"catalog/nolocale.json": {Data: []byte(`{}`)}}); err == nil {
		t.Fatal("want error for missing locale in file name")
	}
	if _, err := Load(fstest.MapFS{"catalog/core.en.json": {Data: []byte(`{"app.name":"x"}`)}}); err == nil {
		t.Fatal("want error for duplicate key core.app.name")
	}
	if _, err := Load(fstest.MapFS{"catalog/demo.en.json": {Data: []byte(`not json`)}}); err == nil {
		t.Fatal("want error for invalid json")
	}
}

func TestDir(t *testing.T) {
	b, _ := Load()
	if b.Dir("ar") != "rtl" || b.Dir("ar-EG") != "rtl" || b.Dir("en") != "ltr" {
		t.Fatal(b.Dir("ar"), b.Dir("ar-EG"), b.Dir("en"))
	}
}

func TestLocaleFromRequest(t *testing.T) {
	cases := []struct{ accept, pref, want string }{
		{"fr-FR, en;q=0.8, ar;q=0.9", "", "ar"},
		{"en-US,en;q=0.9", "", "en"},
		{"ar-EG", "", "ar"},
		{"en;q=0, ar;q=0.1", "", "ar"},
		{"fr, de", "", DefaultLocale},
		{"", "", DefaultLocale},
		{"ar", "en", "en"},    // user preference wins
		{"ar", "fr", "ar"},    // unsupported preference ignored
		{"en", "AR-eg", "ar"}, // preference normalised
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		if c.accept != "" {
			r.Header.Set("Accept-Language", c.accept)
		}
		if got := LocaleFromRequest(r, c.pref); got != c.want {
			t.Errorf("accept=%q pref=%q: got %q want %q", c.accept, c.pref, got, c.want)
		}
	}
}

func TestLocaleContext(t *testing.T) {
	if Locale(context.Background()) != DefaultLocale {
		t.Fatal("default")
	}
	if Locale(WithLocale(context.Background(), "en")) != "en" {
		t.Fatal("roundtrip")
	}
}

func TestLoadRejectsKeysRepeatingModulePrefix(t *testing.T) {
	_, err := Load(fstest.MapFS{"catalog/demo.en.json": {Data: []byte(`{"demo.hello":"x"}`)}})
	if err == nil || !strings.Contains(err.Error(), "demo.en.json") || !strings.Contains(err.Error(), "demo.hello") {
		t.Fatalf("want error naming file and key, got %v", err)
	}
}
