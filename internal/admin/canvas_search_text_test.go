package admin

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCanvasSearchNormalization(t *testing.T) {
	for _, test := range []struct{ text, want string }{
		{"  إِطْلَاقُ الـمَكْتَبَة ١٢٣ / ۴۵۶  ", "اطلاق المكتبه 123 456"},
		{"أإآٱ ىئۍیېے ؤ ةۀہھ ک", "اااا يييييي و هههه ك"},
		{"Planning\u200f  NOVEMBER—2026", "planning november 2026"},
		{"نص\u202aعربي\u202c", "نصعربي"},
		{"ــَُِ\u200f", ""},
	} {
		if got := normalizeCanvasSearch(test.text); got != test.want {
			t.Errorf("normalize %q: got %q want %q", test.text, got, test.want)
		}
	}
}

func TestCanvasSearchSnippetPreservesSpellingAndUTF8(t *testing.T) {
	text := strings.Repeat("تفاصيل 👋 ", 40) + "إِطْلَاق مَكْتَبَة ١٢٣ " + strings.Repeat("نهاية ", 40)
	got := canvasSearchSnippet(text, []string{"اطلاق", "مكتبه"})
	if !utf8.ValidString(got) || utf8.RuneCountInString(got) > 182 || !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") || !strings.Contains(got, "إِطْلَاق مَكْتَبَة ١٢٣") {
		t.Fatalf("snippet should retain original matching text: %q", got)
	}
	if got := canvasSearchSnippet("  short\n\ntext  ", []string{"title"}); got != "short text" {
		t.Fatalf("title-only matches use a readable excerpt: %q", got)
	}
	if got := canvasSearchSnippet("", []string{"title"}); got != "" {
		t.Fatalf("empty body: %q", got)
	}
}
