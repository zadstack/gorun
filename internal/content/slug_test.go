package content

import (
	"context"
	"testing"
)

func TestSlugArabicPreserved(t *testing.T) {
	for in, want := range map[string]string{
		"مرحبا بالعالم":     "مرحبا-بالعالم",
		"Hello, World!":     "hello-world",
		"  Go_Run -- CMS  ": "go-run-cms",
		"مَرْحَبًا ٢٠٢٦":    "مرحبا-٢٠٢٦",
		"‏مرحبا‏ عالم":      "مرحبا-عالم",
		"Café Déjà":         "café-déjà",
	} {
		if got := Slugify(in, false); got != want {
			t.Errorf("Slugify(%q) = %q want %q", in, got, want)
		}
	}
	if got := Slugify("Café مرحبا Déjà", true); got != "cafe-deja" {
		t.Errorf("ascii fold: %q", got)
	}
	s := newTestService(t)
	it := mustCreate(t, s, Input{Type: "post", Title: "مرحبا بالعالم", Locale: "ar"})
	if it.Slug != "مرحبا-بالعالم" {
		t.Fatalf("item slug %q", it.Slug)
	}
	got, err := s.GetBySlug(context.Background(), "post", "مرحبا-بالعالم", "ar")
	if err != nil || got.ID != it.ID || got.Title != "مرحبا بالعالم" {
		t.Fatalf("round trip: %+v %v", got, err)
	}
}

func TestSlugUniqueSuffix(t *testing.T) {
	s := newTestService(t)
	a := mustCreate(t, s, Input{Type: "post", Title: "Hello", Locale: "en"})
	b := mustCreate(t, s, Input{Type: "post", Title: "Hello", Locale: "en"})
	c := mustCreate(t, s, Input{Type: "post", Slug: "hello", Title: "x", Locale: "en"})
	other := mustCreate(t, s, Input{Type: "post", Title: "Hello", Locale: "ar"})
	page := mustCreate(t, s, Input{Type: "page", Title: "Hello", Locale: "en"})
	if a.Slug != "hello" || b.Slug != "hello-2" || c.Slug != "hello-3" || other.Slug != "hello" || page.Slug != "hello" {
		t.Fatalf("slugs: %s %s %s %s %s", a.Slug, b.Slug, c.Slug, other.Slug, page.Slug)
	}
	in := InputFromItem(b)
	in.Title = "Changed"
	up, err := s.Update(context.Background(), b.ID, in)
	if err != nil || up.Slug != "hello-2" {
		t.Fatalf("update keeps its own slug: %+v %v", up, err)
	}
}
