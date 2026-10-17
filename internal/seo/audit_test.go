package seo

import (
	"github.com/zadstack/gorun/internal/content"
	"testing"
)

func TestAuditStoredMetadataDoesNotInventSearchMetrics(t *testing.T) {
	rows := auditItems([]content.Item{
		{ID: "a", Type: "page", Title: "A", Locale: "en", Body: `<img src="a"><img src="b" alt="">`, SEO: map[string]any{"description": "Shared", "canonical": "javascript:bad"}},
		{ID: "b", Type: "page", Title: "B", Locale: "en", SEO: map[string]any{"description": "Shared", "robots": "noindex"}},
		{ID: "c", Type: "page", Title: "C", Locale: "ar", SEO: map[string]any{"description": "Shared"}},
	})
	if rows[0].MissingAlt != 1 {
		t.Fatal("decorative empty alt must not be counted", rows[0])
	}
	if !rows[0].InvalidCanonical || !rows[0].DuplicateDescription || !rows[1].NoIndex {
		t.Fatal("metadata gaps missing", rows)
	}
	if rows[2].DuplicateDescription {
		t.Fatal("different languages must not be grouped")
	}
	if rows[1].MissingDescription {
		t.Fatal("explicit description ignored")
	}
	rows = auditItems([]content.Item{{ID: "x", Title: "Fallback", Excerpt: "Saved excerpt"}})
	if rows[0].MissingDescription {
		t.Fatal("excerpt is available as a description source")
	}
	if rows[0].MissingTitle {
		t.Fatal("item title is a valid fallback")
	}
}

func TestAuditCanonicalRejectsUnsafeOrFragmentedURLs(t *testing.T) {
	for _, value := range []string{`/\evil.test`, `https://example.com/#fragment`, `https://user:secret@example.com`, "//evil.test", "https://example.com/white space"} {
		rows := auditItems([]content.Item{{SEO: map[string]any{"canonical": value}}})
		if !rows[0].InvalidCanonical {
			t.Fatal("accepted", value)
		}
	}
	for _, value := range []string{"/local-page", "https://example.com/page"} {
		rows := auditItems([]content.Item{{SEO: map[string]any{"canonical": value}}})
		if rows[0].InvalidCanonical {
			t.Fatal("rejected", value)
		}
	}
}
