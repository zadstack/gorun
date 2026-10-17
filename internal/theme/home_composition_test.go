package theme

import (
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestHomeCompositionUsesPublishedQueryWithoutInventedCards(t *testing.T) {
	e := newEnv(t, "../../themes")
	posts := []content.Item{
		{ID: "lead", Type: "post", Slug: "first-story", Title: "First real story", Body: "<p>Original first body.</p>", PublishedAt: "2026-10-10T00:00:00Z"},
		{ID: "second", Type: "post", Slug: "second-story", Title: "Second real story", Body: "<p>Original second body.</p>"},
	}
	if err := e.svc.SaveOverride(t.Context(), "index.html", `{{component "home-stories" .}}`, false); err != nil {
		t.Fatal(err)
	}
	set, err := e.svc.set(t.Context(), "default", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"ar", "en"} {
		tc := e.svc.prepare(t.Context(), kernel.KindRequest{Kind: KindHome}, &Context{Items: posts, Locale: locale})
		out, err := e.svc.execute(t.Context(), set, "index.html", tc)
		if err != nil {
			t.Fatal(err)
		}
		body := out.String()
		mustContain(t, body, "First real story", "Second real story", "Original first body.", "gr-home-story-lead")
		if strings.Count(body, `<article class="gr-home-story`) != 2 {
			t.Fatalf("expected only two supplied stories: %s", body)
		}
		mustNotContain(t, body, `href="#"`, `<script`, `https://picsum`, "99.9%")
	}
	tc := e.svc.prepare(t.Context(), kernel.KindRequest{Kind: KindHome}, &Context{})
	out, err := e.svc.execute(t.Context(), set, "index.html", tc)
	if err != nil {
		t.Fatal(err)
	}
	mustNotContain(t, out.String(), "<article", "gr-home-stories")
}
