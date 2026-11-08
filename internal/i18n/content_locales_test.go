package i18n

import (
	"context"
	"reflect"
	"testing"
)

func TestContentLocaleTagsPreserveScriptAndRegion(t *testing.T) {
	for _, scenario := range []struct{ raw, want string }{{"fr", "fr"}, {"pt-br", "pt-BR"}, {"zh-hant-tw", "zh-Hant-TW"}, {"es-419", "es-419"}} {
		got, err := CanonicalLocale(scenario.raw)
		if err != nil || got != scenario.want {
			t.Errorf("%q: %q %v", scenario.raw, got, err)
		}
	}
	for _, invalid := range []string{"", "../fr", "en_US", "fr/<script>", "-fr"} {
		if _, err := CanonicalLocale(invalid); err == nil {
			t.Errorf("accepted %q", invalid)
		}
	}
	if _, err := ParseContentLocales([]string{"pt-br", "pt-BR"}); err == nil {
		t.Fatal("accepted duplicate canonical locales")
	}
}

func TestPublicLocalesDoNotChangeAdminNegotiation(t *testing.T) {
	ctx := WithContentLocales(context.Background(), []string{"fr", "pt-BR"})
	if got := ContentLocales(ctx, nil); !reflect.DeepEqual(got, []string{"fr", "pt-BR"}) {
		t.Fatal(got)
	}
	if !reflect.DeepEqual(Supported, []string{"ar", "en"}) {
		t.Fatalf("admin catalogs changed: %v", Supported)
	}
	if got := LocalizePath("pt-BR", "fr", "/post/x", ContentLocales(ctx, nil)); got != "/pt-BR/post/x" {
		t.Fatal(got)
	}
	locale, path := SplitLocalePath("/pt-br/post/x", ContentLocales(ctx, nil))
	if locale != "pt-BR" || path != "/post/x" {
		t.Fatal(locale, path)
	}
}
