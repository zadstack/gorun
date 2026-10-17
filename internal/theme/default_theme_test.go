package theme

import (
	"net/http"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/taxonomy"
)

// TestDefaultThemeRendersAllKinds renders every template of themes/default
// with fixture context; none may fail.
func TestDefaultThemeRendersAllKinds(t *testing.T) {
	e := newEnv(t, "../../themes")
	post := e.fake.Content.Add(content.Item{Type: "post", Slug: "hello", Title: "Hello", Body: "<p>Body</p>", PublishedAt: "2026-10-10T00:00:00Z",
		Terms: map[string][]string{"category": {"news"}}})
	page := e.fake.Content.Add(content.Item{Type: "page", Slug: "contact", Title: "Contact"})
	term := e.fake.Taxonomy.AddTerm(taxonomy.Term{Taxonomy: "category", Slug: "news", Name: "News", Description: "All news"})
	list := func() *Context {
		return &Context{Items: []content.Item{post}, Pagination: NewPagination(1, 1, 3), Query: map[string]string{"q": "hel"}}
	}
	cases := []struct {
		req  kernel.KindRequest
		tc   *Context
		want string
	}{
		{kernel.KindRequest{Kind: KindHome}, list(), "front-page.html"},
		{kernel.KindRequest{Kind: KindSingle, Type: "post", Slug: "hello"}, &Context{Content: &post}, "single.html"},
		{kernel.KindRequest{Kind: KindPage, Type: "page", Slug: "about"}, &Context{Content: &page}, "page.html"},
		{kernel.KindRequest{Kind: KindPage, Type: "page", Slug: "contact"}, &Context{Content: &page, Extra: map[string]any{"form_flash": "ok"}}, "page-contact.html"},
		{kernel.KindRequest{Kind: KindArchive, Type: "post"}, list(), "archive.html"},
		{kernel.KindRequest{Kind: KindTaxonomy, Taxonomy: "category", Term: "news"}, func() *Context { c := list(); c.Term = &term; return c }(), "taxonomy.html"},
		{kernel.KindRequest{Kind: KindSearch}, list(), "search.html"},
		{kernel.KindRequest{Kind: Kind404}, &Context{Status: http.StatusNotFound}, "404.html"},
		{kernel.KindRequest{Kind: "form-result"}, &Context{Extra: map[string]any{"form": map[string]any{"OK": false, "Errors": map[string]string{"email": "core.validation.email"}, "Values": map[string]string{}, "Back": "/contact"}}}, "index.html"},
		{kernel.KindRequest{Kind: "unknown"}, list(), "index.html"},
	}
	for _, c := range cases {
		if got, err := e.svc.Resolve(c.req); err != nil || got != c.want {
			t.Fatalf("resolve %+v = %q %v, want %q", c.req, got, err, c.want)
		}
		rec, err := e.render(c.req, c.tc)
		if err != nil || (rec.Code != 200 && rec.Code != 404) {
			t.Fatalf("%s: %d %v\n%s", c.want, rec.Code, err, e.logs.String())
		}
		mustContain(t, rec.Body.String(), `dir="rtl"`, `data-theme="default"`, "</html>")
	}
	// form-result.html renders with the site's FormState via the site
	// module; here it is executed directly with a map of the same shape.
	set, err := e.svc.set(t.Context(), "default", false)
	if err != nil {
		t.Fatal(err)
	}
	for name := range set.files {
		if err := set.errs[name]; err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	buf, err := e.svc.execute(t.Context(), set, "form-result.html", e.svc.prepare(t.Context(), kernel.KindRequest{Kind: "form-result"}, cases[8].tc))
	if err != nil {
		t.Fatalf("form-result: %v", err)
	}
	mustContain(t, buf.String(), `role="alert"`, `name="email"`)

	// A broken override falls back to the theme's own 500.html.
	if err := e.svc.SaveOverride(t.Context(), "single.html", `{{part "header" .}}{{ index .Items 9 }}`, false); err != nil {
		t.Fatal(err)
	}
	rec, _ := e.render(kernel.KindRequest{Kind: KindSingle, Type: "post", Slug: "hello"}, &Context{Content: &post})
	if rec.Code != 500 {
		t.Fatalf("500: %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), `data-kind="500"`, "حدث خطأ ما", Marker500)
}
