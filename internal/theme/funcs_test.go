package theme

import (
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/seo"
	"github.com/zadstack/gorun/internal/taxonomy"
)

const funcsTemplate = `
MENU:{{with menu "primary"}}{{range .Items}}[{{.Label}}={{.URL}}{{range .Children}}({{.Label}}={{.URL}}){{end}}]{{end}}{{end}}
NOMENU:{{with menu "nowhere"}}X{{else}}none{{end}}
{{$p := query (dict "type" "post" "per_page" 2 "order" "-published_at")}}QUERY:{{range $p.Items}}<{{.Title}}>{{end}} total={{$p.Total}}
{{$pg := paginate $p}}PAGES:{{$pg.Page}}/{{$pg.Pages}} next={{$pg.HasNext}}
DRAFTS:{{$d := query (dict "type" "post" "status" "draft")}}{{range $d.Items}}{{.Title}},{{end}}
TAX:{{$n := query (dict "taxonomy" (dict "category" "news"))}}{{range $n.Items}}<{{.Title}}>{{end}}
FIELD:{{field . "price"}} {{field .Content "color"}} {{with field . "missing"}}X{{else}}nil{{end}}
TERMS:{{range terms . "category"}}{{.Name}}@{{url .}};{{end}}
IMG:{{img (media .Content.FeaturedMediaID) "medium"}}
IMGID:{{img "m1" "thumbnail" "rounded"}}
NOIMG:[{{img "nope" "medium"}}]
URL:{{url .Content}} {{url "/about"}} {{url "https://x.test/y"}}
CONTENT:{{with content "c003"}}{{.Title}}{{end}} HIDDEN:{{with content "c004"}}{{.Title}}{{else}}none{{end}}
DATE:{{date .Content.PublishedAt}} SHORT:{{date .Content.PublishedAt "short"}}
MONEY:{{money 123450 "USD"}} {{money 5 "JPY"}}
EXCERPT:{{excerpt "<p>one <b>two</b> three four</p>" 3}}
SAFE:{{safe .Content.Body}} ESC:{{.Content.Body}}
SETTING:[{{setting "site.title"}}][{{setting "mail.password"}}][{{setting "site.smtp_password"}}]
ASSET:{{asset "style.css"}}
SEO:{{seo .}}
SITE:{{site.Title}} {{locale}} {{dir}}
CSRF:[{{csrf}}]
`

func TestFuncsMenuQueryFieldImg(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", baseTheme(map[string]string{
		"templates/single.html": funcsTemplate,
		"assets/style.css":      "body{}",
	}))
	e := newEnv(t, root)
	e.setting(SettingTitle, "My Site")
	e.setting("mail.password", "hunter2")
	e.setting("site.smtp_password", "hunter3")

	fc := e.fake.Content
	about := fc.Add(content.Item{Type: "page", Slug: "about", Title: "About"})
	team := fc.Add(content.Item{Type: "page", Slug: "team", Title: "Team", ParentID: about.ID})
	post := fc.Add(content.Item{Type: "post", Slug: "hello", Title: "Hello", PublishedAt: "2026-10-10T09:00:00Z",
		Body: `<p>Body &amp; <em>more</em></p>`, Fields: map[string]any{"price": 42, "color": "red"},
		Terms: map[string][]string{"category": {"news", "ghost"}}, FeaturedMediaID: "m1",
		SEO: map[string]any{"description": "Custom description"}})
	fc.Add(content.Item{Type: "post", Slug: "draft", Title: "Secret", Status: "draft", PublishedAt: "2026-10-11T00:00:00Z"})
	fc.Add(content.Item{Type: "post", Slug: "older", Title: "Older", PublishedAt: "2026-09-01T00:00:00Z"})
	fc.Add(content.Item{Type: "post", Slug: "oldest", Title: "Oldest", PublishedAt: "2026-08-01T00:00:00Z"})
	e.fake.Taxonomy.AddTerm(taxonomy.Term{ID: "t1", Taxonomy: "category", Slug: "news", Name: "News"})
	e.fake.Media.Add(media.Media{ID: "m1", URL: "/media/a.jpg", Width: 2000, Height: 1000, Alt: `A "quoted" <alt>`,
		Variants: map[string]media.Variant{
			"thumbnail": {URL: "/media/a-300.jpg", Width: 300, Height: 150},
			"medium":    {URL: "/media/a-768.jpg", Width: 768, Height: 384},
			"evil":      {URL: "javascript:alert(1)", Width: 999, Height: 1},
		}})
	e.fake.Menus.Save(t.Context(), menus.Menu{Slug: "main", Location: "primary", Items: []menus.Item{
		{Label: "Home", Kind: "custom", URL: "/"},
		{Label: "Team", Kind: "content", RefID: team.ID, Children: []menus.Item{{Label: "News", Kind: "term", RefID: "t1"}}},
		{Label: "Blog", Kind: "archive", RefID: "post"},
	}})

	rec, err := e.render(kernel.KindRequest{Kind: KindSingle, Type: "post", Slug: "hello"}, &Context{Content: &post})
	if err != nil || rec.Code != 200 {
		t.Fatalf("render: %d %v\n%s\n%s", rec.Code, err, rec.Body.String(), e.logs.String())
	}
	body := rec.Body.String()
	mustContain(t, body,
		"MENU:[Home=/][Team=/about/team(News=/category/news)][Blog=/blog/]",
		"NOMENU:none",
		"QUERY:&lt;Hello>&lt;Older> total=3",
		"PAGES:1/2 next=true",
		"DRAFTS:Hello,Older,Oldest,", // status forced to published for visitors
		"TAX:&lt;Hello>",
		"FIELD:42 red nil",
		"TERMS:News@http://example.test/category/news;",
		`srcset="/media/a-300.jpg 300w, /media/a-768.jpg 768w, /media/a.jpg 2000w"`,
		`src="/media/a-768.jpg"`, `width="768" height="384"`, `alt="A &#34;quoted&#34; &lt;alt&gt;"`, `loading="lazy"`,
		`IMGID:<img src="/media/a-300.jpg"`, `class="rounded"`,
		"NOIMG:[]",
		"URL:http://example.test/post/hello http://example.test/about https://x.test/y",
		"CONTENT:Hello HIDDEN:none",
		"DATE:10 أكتوبر 2026 SHORT:2026-10-10",
		"MONEY:1,234.50 USD 5 JPY",
		"EXCERPT:one two three…",
		"SAFE:<p>Body &amp; <em>more</em></p> ESC:&lt;p&gt;",
		"SETTING:[My Site][][]",
		"ASSET:/themes/default/assets/style.css?v=",
		"SEO:<title>Hello</title>", `content="Custom description"`, `href="http://example.test/post/hello"`,
		"SITE:My Site ar rtl",
	)
	mustNotContain(t, body, "javascript:", "hunter", "Secret")
	mustContain(t, body, "CSRF:[]") // no CSRF middleware in a direct render

	// Preview-capable users see drafts through query; content.view is required.
	rec, _ = e.render(kernel.KindRequest{Kind: KindSingle, Type: "post", Slug: "hello"}, &Context{Content: &post}, adminCtx())
	mustContain(t, rec.Body.String(), "DRAFTS:Secret,", "HIDDEN:Secret")
}

func TestMoneyAndDate(t *testing.T) {
	cases := []struct {
		amount   any
		cur, loc string
		want     string
	}{
		{123450, "USD", "en", "$1,234.50"},
		{123450, "USD", "ar", "1,234.50 USD"},
		{-99, "EUR", "en", "-€0.99"},
		{1000, "KWD", "en", "1.000 KWD"},
		{100000000, "EGP", "en", "1,000,000.00 EGP"},
		{"250", "SAR", "ar", "2.50 SAR"},
	}
	for _, c := range cases {
		if got, err := Money(c.amount, c.cur, c.loc); err != nil || got != c.want {
			t.Errorf("Money(%v,%s,%s) = %q %v, want %q", c.amount, c.cur, c.loc, got, err, c.want)
		}
	}
	if _, err := Money("x", "USD", "en"); err == nil {
		t.Error("non-number money must fail")
	}
	if got := FormatDate("2026-01-05T23:30:00Z", "en", "Africa/Cairo"); got != "January 6, 2026" {
		t.Errorf("tz date = %q", got)
	}
	if got := FormatDate("2026-03-01T00:00:00Z", "ar", "", "datetime"); got != "1 مارس 2026 00:00" {
		t.Errorf("ar datetime = %q", got)
	}
	if got := FormatDate("not a date", "ar", ""); got != "not a date" {
		t.Errorf("passthrough = %q", got)
	}
	q, err := BuildQuery(map[string]any{"type": "post", "status": "draft", "per_page": 500, "order": "title asc, -published_at", "taxonomy": "tag:a,b", "fields": map[string]any{"price": map[string]any{"op": "gt", "value": 5}, "color": "red"}}, false)
	if err != nil || q.Status != "published" || q.PerPage != 100 || len(q.Order) != 2 || q.Order[1].Dir != "desc" || len(q.Taxonomy["tag"]) != 2 || q.Fields["price"].Op != "gt" || q.Fields["color"].Op != "eq" {
		t.Fatalf("query: %+v %v", q, err)
	}
	if _, err := BuildQuery(map[string]any{"bogus": 1}, true); err == nil {
		t.Fatal("unknown key must fail")
	}
}

func TestMenuResolveURLFilter(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", baseTheme(nil))
	e := newEnv(t, root)
	page := e.fake.Content.Add(content.Item{Type: "page", Slug: "about", Title: "About"})
	e.fake.Taxonomy.AddTerm(taxonomy.Term{ID: "t9", Taxonomy: "tag", Slug: "go", Name: "Go"})
	it := &menus.Item{Kind: "content", RefID: page.ID, Children: []menus.Item{{Kind: "term", RefID: "t9"}, {Kind: "custom", URL: "https://x.test"}}}
	v, err := e.app.Deps.Hooks.Apply(t.Context(), HookMenuResolveURL, it)
	if err != nil || v.(*menus.Item) != it {
		t.Fatalf("filter: %v", err)
	}
	if it.URL != "/about" || it.Children[0].URL != "/tag/go" || it.Children[1].URL != "https://x.test" {
		t.Fatalf("urls: %+v", it)
	}
	e.setting(SettingFront, page.ID)
	it = &menus.Item{Kind: "content", RefID: page.ID}
	e.app.Deps.Hooks.Apply(t.Context(), HookMenuResolveURL, it)
	if it.URL != "/" {
		t.Fatalf("front page url: %q", it.URL)
	}
}

func TestSEOAppliesSiteTitleTemplateOnce(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", baseTheme(map[string]string{"templates/page.html": `{{seo .}}`, "templates/archive.html": `{{seo .}}`}))
	e := newEnv(t, root)
	e.setting(SettingTitle, "Example Co")
	if err := seo.New().Register(e.app.Deps); err != nil {
		t.Fatal(err)
	}
	page := e.fake.Content.Add(content.Item{Type: "page", Slug: "pricing", Title: "الباقات والأسعار", Status: "published"})
	render := func(tc *Context, req kernel.KindRequest, want string) {
		t.Helper()
		rec, err := e.render(req, tc)
		if err != nil || rec.Code != 200 {
			t.Fatalf("render: %d %v", rec.Code, err)
		}
		mustContain(t, rec.Body.String(), "<title>"+want+"</title>")
	}
	req := kernel.KindRequest{Kind: KindPage, Type: "page", Slug: "pricing"}
	render(&Context{Content: &page}, req, "الباقات والأسعار – Example Co")
	e.setting(seo.SettingTitleTemplate, "{site} | {title}")
	render(&Context{Content: &page}, req, "Example Co | الباقات والأسعار")
	render(&Context{Extra: map[string]any{"title": "Articles"}}, kernel.KindRequest{Kind: KindArchive, Type: "post"}, "Example Co | Articles")
	page.SEO = map[string]any{"title": "Exact SEO title"}
	render(&Context{Content: &page}, req, "Exact SEO title")
}
