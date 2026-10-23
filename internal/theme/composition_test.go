package theme

import (
	"context"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"github.com/zadstack/gorun/internal/taxonomy"
)

// compositionEnv boots real content and taxonomy with a theme whose index
// template is tpl.
func compositionEnv(t *testing.T, tpl string) (*env, content.Service) {
	t.Helper()
	root := t.TempDir()
	writeTheme(t, root, "default", "", map[string]string{"templates/index.html": tpl})
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://example.test", Theme: "default"}
	mod := New(WithThemesDir(root), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), auth.New(), content.New(), taxonomy.New(), media.New(), mod})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	cs, _ := kernel.Get[content.Service](app.Deps, kernel.ServiceContent)
	return &env{t: t, app: app, svc: mod.svc, root: root, cfg: cfg}, cs
}

func mustItem(t *testing.T, cs content.Service, in content.Input) *content.Item {
	t.Helper()
	it, err := cs.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("create %q: %v", in.Title, err)
	}
	return it
}

func renderIn(t *testing.T, e *env, locale string, tc *Context) string {
	t.Helper()
	if tc == nil {
		tc = &Context{}
	}
	tc.Locale = locale
	rec, err := e.render(kernel.KindRequest{Kind: "index"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(rec.Body.String())
}

// destination resolves a purpose page in the current locale, then through the
// translation group, then as an explicit fallback; drafts, unknown purposes
// and missing pages are unavailable; a slug match serves pages without the
// field.
func TestDestinationResolvesPurposePagesPerLocale(t *testing.T) {
	e, cs := compositionEnv(t, `{{range $p := .Extra.purposes}}{{with destination $p}}{{$p}}={{.Available}},{{.Fallback}},{{.Locale}},{{.URL}};{{end}}{{end}}`)
	purposes := &Context{Extra: map[string]any{"purposes": []string{"contact", "about", "service", "pricing"}}}
	got := func(locale string) string {
		tc := *purposes
		return renderIn(t, e, locale, &tc)
	}
	if out := got("ar"); out != "contact=false,false,,;about=false,false,,;service=false,false,,;pricing=false,false,,;" {
		t.Fatalf("empty site: %s", out)
	}
	ar := mustItem(t, cs, content.Input{Type: "page", Title: "تواصل", Slug: "tawasul", Locale: "ar", Status: "published", Fields: map[string]any{"purpose": "contact"}})
	mustItem(t, cs, content.Input{Type: "page", Title: "Draft contact", Slug: "contact-draft", Locale: "en", Status: "draft", Fields: map[string]any{"purpose": "contact"}})
	mustItem(t, cs, content.Input{Type: "page", Title: "About", Slug: "about", Locale: "en", Status: "published"})

	out := got("ar")
	if !strings.Contains(out, "contact=true,false,ar,") || !strings.Contains(out, "/tawasul;") {
		t.Fatalf("Arabic contact page: %s", out)
	}
	if !strings.Contains(out, "about=true,true,en,") {
		t.Fatalf("about exists only in English, so Arabic must get an explicit fallback: %s", out)
	}
	if !strings.Contains(out, "pricing=false") {
		t.Fatalf("unknown purposes are never resolved: %s", out)
	}
	// English: no English contact page (the draft does not count), so the
	// Arabic one is an explicit fallback.
	if out := got("en"); !strings.Contains(out, "contact=true,true,ar,") || !strings.Contains(out, "about=true,false,en,") {
		t.Fatalf("English before translation: %s", out)
	}
	en := mustItem(t, cs, content.Input{Type: "page", Title: "Contact", Slug: "get-in-touch", Locale: "en", Status: "published"})
	if err := cs.LinkTranslation(context.Background(), ar.ID, en.ID); err != nil {
		t.Fatal(err)
	}
	if out := got("en"); !strings.Contains(out, "contact=true,false,en,") || !strings.Contains(out, "/get-in-touch;") {
		t.Fatalf("the English translation must serve English: %s", out)
	}
}

// Only a known purpose reaches the template hierarchy.
func TestPagePurposeAcceptsOnlyKnownValues(t *testing.T) {
	for v, want := range map[any]string{"contact": "contact", "about": "about", "../../etc": "", "pricing": "", 3: "", nil: ""} {
		if got := PagePurpose(&content.Item{Fields: map[string]any{"purpose": v}}); got != want {
			t.Errorf("purpose %v = %q, want %q", v, got, want)
		}
	}
	cands := candidates(kernel.KindRequest{Kind: KindPage, Slug: "x", Template: "builder", Purpose: "contact"}, nil)
	if strings.Join(cands, ",") != "page-x.html,page-builder.html,page-contact.html,page.html,single.html,index.html" {
		t.Fatalf("candidates = %v", cands)
	}
	if cands := candidates(kernel.KindRequest{Kind: KindPage, Slug: "x", Purpose: "../evil"}, nil); strings.Contains(strings.Join(cands, ","), "evil") {
		t.Fatalf("an unknown purpose reached the hierarchy: %v", cands)
	}
}

// related shares a term, stays in type and locale, skips drafts and the item
// itself; adjacent orders by publication then id; termsOf counts published
// items of one type in the page locale.
func TestRelatedAdjacentAndTermsOf(t *testing.T) {
	e, cs := compositionEnv(t, `{{with .Content}}R:{{range related . 5}}{{.Slug}} {{end}}|A:{{with adjacent .}}{{with .Prev}}{{.Slug}}{{end}}<{{with .Next}}{{.Slug}}{{end}}{{end}}{{end}}|T:{{range termsOf "category" "post"}}{{.Term.Slug}}={{.Count}} {{end}}`)
	ctx := context.Background()
	tax, _ := kernel.Get[taxonomy.Service](e.app.Deps, kernel.ServiceTaxonomy)
	for _, s := range []string{"news", "guides", "empty"} {
		if _, err := tax.SaveTerm(ctx, taxonomy.Term{Taxonomy: "category", Slug: s, Name: s}); err != nil {
			t.Fatal(err)
		}
	}
	post := func(slug, locale, status string, cats ...string) *content.Item {
		return mustItem(t, cs, content.Input{Type: "post", Title: slug, Slug: slug, Locale: locale, Status: content.Status(status), Terms: map[string][]string{"category": cats}})
	}
	first := post("first", "ar", "published", "news")
	post("second", "ar", "published", "guides")
	current := post("current", "ar", "published", "news")
	post("hidden", "ar", "draft", "news")
	post("english", "en", "published", "news")
	post("newest", "ar", "published", "news", "guides")
	mustItem(t, cs, content.Input{Type: "page", Title: "Page", Slug: "page-news", Locale: "ar", Status: "published"})

	out := renderIn(t, e, "ar", &Context{Content: current})
	if !strings.Contains(out, "R:newest first |") {
		t.Fatalf("related must be the published Arabic posts sharing a term, newest first: %s", out)
	}
	if !strings.Contains(out, "|A:second&lt;newest|") {
		t.Fatalf("adjacent must be the published neighbours by date then id: %s", out)
	}
	if !strings.Contains(out, "news=3") || !strings.Contains(out, "guides=2") || strings.Contains(out, "empty=") {
		t.Fatalf("termsOf must count published Arabic posts and skip empty terms: %s", out)
	}
	if out := renderIn(t, e, "ar", &Context{Content: first}); !strings.Contains(out, "|A:&lt;second|") {
		t.Fatalf("the oldest post has no previous neighbour: %s", out)
	}
}

// The default article prints its table of contents beside the same anchored
// body it lists (every link target exists in the printed body), hides it
// below three headings, and shows related and neighbour sections only when
// they have data.
func TestDefaultArticleAnatomyRendersOnlyWithData(t *testing.T) {
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://example.test", Theme: "default"}
	mod := New(WithThemesDir("../../themes"), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), auth.New(), content.New(), taxonomy.New(), media.New(), mod})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	e := &env{t: t, app: app, svc: mod.svc, cfg: cfg}
	cs, _ := kernel.Get[content.Service](app.Deps, kernel.ServiceContent)
	render := func(it *content.Item) string {
		rec, err := e.render(kernel.KindRequest{Kind: KindSingle, Type: "post", Slug: it.Slug}, &Context{Content: it, Locale: "ar"})
		if err != nil {
			t.Fatal(err)
		}
		return rec.Body.String()
	}
	long := mustItem(t, cs, content.Input{Type: "post", Title: "Long", Slug: "long", Locale: "ar", Status: "published",
		Body: `<h2>Intro</h2><p>a</p><h2>Intro</h2><p>b</p><h3>Detail</h3><p>c</p>`})
	out := render(long)
	if !strings.Contains(out, `class="gp-article-toc"`) {
		t.Fatalf("three headings must show the table of contents:\n%s", out)
	}
	for _, m := range regexpAll(`href="#([^"]+)"`, out) {
		if !strings.Contains(out, `id="`+m+`"`) {
			t.Errorf("toc link #%s has no target in the printed body", m)
		}
	}
	if strings.Contains(out, "gp-article-related") || strings.Contains(out, "gp-article-adjacent") {
		t.Fatalf("a lone article has no related or neighbour section:\n%s", out)
	}
	short := mustItem(t, cs, content.Input{Type: "post", Title: "Short", Slug: "short", Locale: "ar", Status: "published", Body: `<h2>Only</h2><p>x</p>`})
	out = render(short)
	if strings.Contains(out, "gp-article-toc") {
		t.Fatal("one heading must not show a table of contents")
	}
	if !strings.Contains(out, `class="gp-article-adjacent"`) || !strings.Contains(out, `rel="prev"`) {
		t.Fatalf("the newer article links to the older one:\n%s", out)
	}
}

func regexpAll(pattern, s string) []string {
	var out []string
	for _, m := range regexp.MustCompile(pattern).FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// series orders a guide's published parts in the item's locale by
// series_order (unnumbered parts last, oldest first), skips drafts and
// other-locale parts, and gives series neighbours; an item in no series, or
// the only published part, has none.
func TestSeriesOrdersPublishedPartsInLocale(t *testing.T) {
	e, cs := compositionEnv(t, `{{with .Content}}{{with series .}}{{.Term.Slug}} {{.Index}}/{{.Count}}:{{range .Parts}}{{.Slug}} {{end}}|{{with .Prev}}{{.Slug}}{{end}}<{{with .Next}}{{.Slug}}{{end}}{{else}}none{{end}}{{end}}`)
	ctx := context.Background()
	tax, _ := kernel.Get[taxonomy.Service](e.app.Deps, kernel.ServiceTaxonomy)
	for _, s := range []string{"setup", "solo"} {
		if _, err := tax.SaveTerm(ctx, taxonomy.Term{Taxonomy: SeriesTaxonomy, Slug: s, Name: s}); err != nil {
			t.Fatal(err)
		}
	}
	part := func(slug, series, locale, status string, order any) *content.Item {
		fields := map[string]any{}
		if order != nil {
			fields[SeriesOrderField] = order
		}
		return mustItem(t, cs, content.Input{Type: "post", Title: slug, Slug: slug, Locale: locale, Status: content.Status(status),
			Terms: map[string][]string{SeriesTaxonomy: {series}}, Fields: fields})
	}
	third := part("third", "setup", "ar", "published", 3)
	first := part("first", "setup", "ar", "published", 1)
	part("unnumbered", "setup", "ar", "published", nil)
	part("draft-two", "setup", "ar", "draft", 2)
	part("english-two", "setup", "en", "published", 2)
	alone := part("alone", "solo", "ar", "published", 1)
	plain := mustItem(t, cs, content.Input{Type: "post", Title: "plain", Slug: "plain", Locale: "ar", Status: "published"})

	if got := renderIn(t, e, "ar", &Context{Content: third}); got != "setup 2/3:first third unnumbered |first&lt;unnumbered" {
		t.Fatalf("third: %q", got)
	}
	if got := renderIn(t, e, "ar", &Context{Content: first}); got != "setup 1/3:first third unnumbered |&lt;third" {
		t.Fatalf("first: %q", got)
	}
	for name, it := range map[string]*content.Item{"only part": alone, "no series": plain} {
		if got := renderIn(t, e, "ar", &Context{Content: it}); got != "none" {
			t.Fatalf("%s: %q", name, got)
		}
	}
}

// Sharing only a series does not make posts related: the series has its own
// navigation.
func TestRelatedIgnoresSeriesMembership(t *testing.T) {
	e, cs := compositionEnv(t, `{{with .Content}}R:{{range related . 5}}{{.Slug}} {{end}}{{end}}`)
	ctx := context.Background()
	tax, _ := kernel.Get[taxonomy.Service](e.app.Deps, kernel.ServiceTaxonomy)
	if _, err := tax.SaveTerm(ctx, taxonomy.Term{Taxonomy: SeriesTaxonomy, Slug: "s", Name: "s"}); err != nil {
		t.Fatal(err)
	}
	one := mustItem(t, cs, content.Input{Type: "post", Title: "one", Slug: "one", Locale: "ar", Status: "published", Terms: map[string][]string{SeriesTaxonomy: {"s"}}})
	mustItem(t, cs, content.Input{Type: "post", Title: "two", Slug: "two", Locale: "ar", Status: "published", Terms: map[string][]string{SeriesTaxonomy: {"s"}}})
	if got := renderIn(t, e, "ar", &Context{Content: one}); got != "R:" {
		t.Fatalf("series-only siblings must not be related: %q", got)
	}
}
