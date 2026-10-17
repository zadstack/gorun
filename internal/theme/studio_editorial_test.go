package theme

import (
	"net/http"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/taxonomy"
)

func TestStudioEditorialCompleteRendering(t *testing.T) {
	e := newEnv(t, "../../themes", func(c *config.Config) { c.Theme = "magazine" })
	post := e.fake.Content.Add(content.Item{Type: "post", Slug: "real-story", Title: `Story <script>alert(1)</script>`, Body: "<p>Published story.</p>", Status: "published", PublishedAt: "2026-10-10T00:00:00Z"})
	page := e.fake.Content.Add(content.Item{Type: "page", Slug: "about", Title: "About this journal", Status: "published", Body: "<p>Editorial principles.</p>"})
	builder := e.fake.Content.Add(content.Item{Type: "page", Slug: "start", Title: "Start", Template: "builder", Status: "published", Body: `<section class="gp-section gp-feed-posts"><div class="gp-inner"><h1>Our journal</h1></div></section>`})
	term := e.fake.Taxonomy.AddTerm(taxonomy.Term{Taxonomy: "category", Slug: "culture", Name: "Culture"})
	list := func() *Context {
		return &Context{Items: []content.Item{post}, Query: map[string]string{"q": `<script>alert(2)</script>`}, Pagination: NewPagination(1, 1, 3)}
	}
	cases := []struct {
		req  kernel.KindRequest
		tc   *Context
		want string
	}{
		{kernel.KindRequest{Kind: KindHome}, list(), "ed-front"},
		{kernel.KindRequest{Kind: KindSingle, Type: "post", Slug: post.Slug}, &Context{Content: &post}, "ed-article"},
		{kernel.KindRequest{Kind: KindPage, Type: "page", Slug: page.Slug}, &Context{Content: &page}, "ed-page"},
		{kernel.KindRequest{Kind: KindPage, Type: "page", Slug: "contact"}, &Context{Content: &page}, "name=\"email\""},
		{kernel.KindRequest{Kind: KindPage, Type: "page", Slug: builder.Slug, Template: "builder"}, &Context{Content: &builder}, "ed-story"},
		{kernel.KindRequest{Kind: KindArchive, Type: "post"}, list(), "ed-story-grid"},
		{kernel.KindRequest{Kind: KindTaxonomy, Taxonomy: "category", Term: "culture"}, func() *Context { v := list(); v.Term = &term; return v }(), "Culture"},
		{kernel.KindRequest{Kind: KindSearch}, list(), "ed-search"},
		{kernel.KindRequest{Kind: Kind404}, &Context{Status: http.StatusNotFound}, "ed-error"},
	}
	for _, tc := range cases {
		t.Run(tc.req.Kind+tc.req.Slug, func(t *testing.T) {
			rec, err := e.render(tc.req, tc.tc)
			if err != nil || (rec.Code != 200 && rec.Code != 404) {
				t.Fatalf("%d %v %s", rec.Code, err, e.logs.String())
			}
			body := rec.Body.String()
			mustContain(t, body, `data-theme="magazine"`, "/themes/magazine/assets/site.css", "ed-masthead", "ed-footer", tc.want, "</html>")
			mustNotContain(t, body, "<script>alert(", "https://cdn.", "fonts.googleapis.com")
			if strings.Contains(body, "ed-story") {
				mustContain(t, body, `href="http://example.test/post/real-story"`)
			}
		})
	}
	set, err := e.svc.set(t.Context(), "magazine", false)
	if err != nil {
		t.Fatal(err)
	}
	for name := range set.files {
		if err := set.errs[name]; err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestStudioEditorialLocaleAndEmptyStates(t *testing.T) {
	e := newEnv(t, "../../themes", func(c *config.Config) { c.Theme = "magazine" })
	for _, locale := range []string{"ar", "en"} {
		tc := &Context{Locale: locale, Query: map[string]string{"q": "unmatched"}}
		rec, err := e.render(kernel.KindRequest{Kind: KindSearch}, tc)
		if err != nil || rec.Code != 200 {
			t.Fatalf("%s: %d %v", locale, rec.Code, err)
		}
		body := rec.Body.String()
		direction := "ltr"
		if locale == "ar" {
			direction = "rtl"
		}
		mustContain(t, body, `lang="`+locale+`"`, `dir="`+direction+`"`, "ed-empty")
		if locale == "en" {
			mustContain(t, body, "Nothing found. Try different words.", "Independent journal")
		}
	}
}

func TestStudioEditorialBuilderContactRetainsSectionPosition(t *testing.T) {
	e := newEnv(t, "../../themes", func(c *config.Config) { c.Theme = "magazine" })
	page := e.fake.Content.Add(content.Item{Type: "page", Slug: "contact", Title: "Contact", Template: "builder", Status: "published", Body: `<section class="gp-section gp-contact"><div class="gp-inner"><h1>Write to us</h1></div></section><section class="gp-section gp-text"><div class="gp-inner"><h2>After the contact form</h2><p>Editorial guidelines</p></div></section>`})
	rec, err := e.render(kernel.KindRequest{Kind: KindPage, Type: "page", Slug: "contact", Template: "builder"}, &Context{Content: &page})
	if err != nil || rec.Code != 200 {
		t.Fatalf("%d %v %s", rec.Code, err, e.logs.String())
	}
	body := rec.Body.String()
	if got := strings.Count(body, `action="/forms/contact"`); got != 1 {
		t.Fatalf("forms=%d, want exactly one", got)
	}
	formAt := strings.Index(body, `action="/forms/contact"`)
	headingAt := strings.Index(body, ">After the contact form</h2>")
	if formAt < 0 || headingAt < 0 || formAt > headingAt {
		t.Fatalf("contact form appeared after following section: form=%d next=%d", formAt, headingAt)
	}
	mustContain(t, body, `class="ed-builder gp-page"`)
	mustNotContain(t, body, `class="ed-contact"`)
}
