package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/theme"
)

// bundledThemes are the catalog themes shipped under themes/.
var bundledThemes = []string{"default", "business", "magazine", "portfolio", "docs"}

const marker500 = "<!-- gorun:500 -->"

// siteFixture is the content, media, menu and identity a real site carries
// across a theme switch.
type siteFixture struct {
	logoID, logoURL, arPost, enPost string
	menuLabels                      []string
	settingsBefore                  map[string]string
}

func seedSite(t *testing.T, app *kernel.App) siteFixture {
	t.Helper()
	ctx := context.Background()
	var fx siteFixture
	st := app.Deps.Settings
	for k, v := range map[string]any{
		"site.title": "Atlas Demo", "site.tagline": "Maps for people", "site.locale": "ar",
		theme.SettingKey("default", "cta_label"): "Talk to us", theme.SettingKey("default", "cta_url"): "/contact",
		theme.SettingKey("business", "footer_contact"):  "hello@atlas.test",
		theme.SettingKey("business", "custom_css"):      "body{--atlas-probe:1}",
		theme.SettingKey("magazine", "identity_accent"): "#aa3366",
	} {
		if err := st.Set(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	ms, ok := kernel.Get[media.Service](app.Deps, kernel.ServiceMedia)
	if !ok {
		t.Fatal("media service missing")
	}
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = 0x7f
	}
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	logo, err := ms.Upload(ctx, "atlas-logo.png", &buf, media.UploadMeta{Alt: "Atlas logo", Title: "Atlas logo"})
	if err != nil {
		t.Fatal(err)
	}
	fx.logoID, fx.logoURL = logo.ID, logo.URL
	if err := st.Set(ctx, "site.logo", logo.ID); err != nil {
		t.Fatal(err)
	}
	mn, ok := kernel.Get[menus.Service](app.Deps, kernel.ServiceMenus)
	if !ok {
		t.Fatal("menus service missing")
	}
	menu, err := mn.Save(ctx, menus.Menu{Slug: "main", Name: "Main", Location: "primary"})
	if err != nil {
		t.Fatal(err)
	}
	fx.menuLabels = []string{"المقالات", "Atlas journal"}
	if err := mn.SaveItems(ctx, menu.ID, []menus.Item{{Label: fx.menuLabels[0], Kind: "custom", URL: "/blog"}, {Label: fx.menuLabels[1], Kind: "custom", URL: "/en/blog"}}); err != nil {
		t.Fatal(err)
	}
	cs, ok := kernel.Get[content.Service](app.Deps, kernel.ServiceContent)
	if !ok {
		t.Fatal("content service missing")
	}
	ar, err := cs.Create(ctx, content.Input{Type: "post", Title: "مقال للتبديل بين القوالب", Slug: "switch-ar", Locale: "ar", Status: "published",
		Body: "<p>فقرة.</p><h2>عنوان فرعي</h2><p>كلمةطويلةجدًابلامسافاتلفحصالالتفاف</p>", FeaturedMediaID: logo.ID})
	if err != nil {
		t.Fatal(err)
	}
	en, err := cs.Create(ctx, content.Input{Type: "post", Title: "A post that survives theme switches", Slug: "switch-en", Locale: "en", Status: "published",
		Body: "<p>Paragraph.</p><h2>Heading</h2><p>Averyveryverylongwordwithoutspacestocheckwrapping</p>", FeaturedMediaID: logo.ID})
	if err != nil {
		t.Fatal(err)
	}
	fx.arPost, fx.enPost = "/post/"+ar.Slug, "/en/post/"+en.Slug
	fx.settingsBefore = settingsSnapshot(t, app.Deps.DB)
	return fx
}

func settingsSnapshot(t *testing.T, d *sql.DB) map[string]string {
	t.Helper()
	rows, err := d.Query(`SELECT key, value_json FROM settings`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatal(err)
		}
		out[k] = v
	}
	return out
}

func getHTML(t *testing.T, h http.Handler, path string) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w.Code, w.Body.String()
}

var htmlTag = regexp.MustCompile(`<html[^>]*>`)

// checkThemePage asserts the page rendered by slug in locale: no 500 marker,
// the html element carries the locale and direction, the active theme, the
// site title, the logo image and the primary menu.
func checkThemePage(t *testing.T, body, slug, locale, logoURL string, menuLabels []string) {
	t.Helper()
	if strings.Contains(body, marker500) {
		t.Fatalf("%s/%s: rendered the 500 marker", slug, locale)
	}
	tag := htmlTag.FindString(body)
	dir := "rtl"
	if locale == "en" {
		dir = "ltr"
	}
	for _, want := range []string{`lang="` + locale + `"`, `dir="` + dir + `"`, `data-theme="` + slug + `"`} {
		if !strings.Contains(tag, want) {
			t.Errorf("%s/%s: html tag %q lacks %s", slug, locale, tag, want)
		}
	}
	if !strings.Contains(body, "Atlas Demo") {
		t.Errorf("%s/%s: site title missing", slug, locale)
	}
	if !strings.Contains(body, logoURL) || !strings.Contains(body, `alt="Atlas logo"`) {
		t.Errorf("%s/%s: logo image %s (alt \"Atlas logo\") not rendered", slug, locale, logoURL)
	}
	for _, label := range menuLabels {
		if !strings.Contains(body, label) {
			t.Errorf("%s/%s: menu label %q missing", slug, locale, label)
		}
	}
}

// Switching between the bundled themes keeps content, media, menus, site
// identity, custom CSS and per-theme settings, and every theme renders both
// locales, inner pages and the 404 page with that site.
func TestSwitchingBundledThemesPreservesSiteAndRendersBothLocales(t *testing.T) {
	app, _ := bootFull(t)
	ctx := context.Background()
	fx := seedSite(t, app)
	ts, ok := kernel.Get[theme.Service](app.Deps, kernel.ServiceTheme)
	if !ok {
		t.Fatal("theme service missing")
	}
	for _, slug := range append(slices.Clone(bundledThemes[1:]), "default") {
		if err := ts.Activate(ctx, slug); err != nil {
			t.Fatalf("activate %s: %v", slug, err)
		}
		for _, page := range []struct{ path, locale string }{{"/", "ar"}, {"/en/", "en"}, {fx.arPost, "ar"}, {fx.enPost, "en"}, {"/blog", "ar"}, {"/en/blog", "en"}, {"/search?q=switch", "ar"}} {
			status, body := getHTML(t, app.Handler, page.path)
			if status != 200 {
				t.Fatalf("%s %s: %d", slug, page.path, status)
			}
			checkThemePage(t, body, slug, page.locale, fx.logoURL, fx.menuLabels)
		}
		status, body := getHTML(t, app.Handler, "/no-such-page")
		if status != 404 || strings.Contains(body, marker500) || !strings.Contains(body, `data-theme="`+slug+`"`) {
			t.Fatalf("%s 404 page: %d themed=%v", slug, status, strings.Contains(body, `data-theme="`+slug+`"`))
		}
		if slug == "business" {
			if _, body := getHTML(t, app.Handler, "/"); !strings.Contains(body, "hello@atlas.test") || !strings.Contains(body, "/site/identity.css") {
				t.Fatalf("business: per-theme footer contact or custom CSS link missing")
			}
			if status, css := getHTML(t, app.Handler, "/site/identity.css"); status != 200 || !strings.Contains(css, "--atlas-probe") {
				t.Fatalf("business custom CSS: %d %q", status, css)
			}
		}
		if slug == "default" {
			if _, body := getHTML(t, app.Handler, "/"); !strings.Contains(body, "Talk to us") || !strings.Contains(body, "/contact") {
				t.Fatalf("default: header CTA setting lost after switching")
			}
		}
	}
	after := settingsSnapshot(t, app.Deps.DB)
	for k, v := range fx.settingsBefore {
		if k == theme.SettingTheme {
			continue
		}
		if after[k] != v {
			t.Errorf("setting %s changed by theme switches: %s → %s", k, v, after[k])
		}
	}
	var n int
	if err := app.Deps.DB.QueryRow(`SELECT count(*) FROM content WHERE status = 'published'`).Scan(&n); err != nil || n < 2 {
		t.Fatalf("content after switches: %d %v", n, err)
	}
	ms, _ := kernel.Get[media.Service](app.Deps, kernel.ServiceMedia)
	if m, err := ms.Get(ctx, fx.logoID); err != nil || m == nil {
		t.Fatalf("logo media after switches: %v", err)
	}
}

// Every bundled theme renders without the optional commerce, saas and
// membership modules: the store funcs fall back to empty results and the
// store routes are simply absent.
func TestBundledThemesRenderWithoutOptionalModules(t *testing.T) {
	kernel.LogOutput = logSink
	cfg, err := config.Load([]string{"--data", t.TempDir(), "--base-url", "http://gorun.test"})
	if err != nil {
		t.Fatal(err)
	}
	mods := slices.DeleteFunc(Modules(cfg), func(m kernel.Module) bool {
		return slices.Contains([]string{"commerce", "saas", "membership"}, m.Name())
	})
	app, err := kernel.BootWithOptions(context.Background(), cfg, mods, kernel.BootOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	fx := seedSite(t, app)
	ts, _ := kernel.Get[theme.Service](app.Deps, kernel.ServiceTheme)
	for _, slug := range bundledThemes {
		if err := ts.Activate(context.Background(), slug); err != nil {
			t.Fatal(err)
		}
		for _, page := range []struct{ path, locale string }{{"/", "ar"}, {"/en/", "en"}, {fx.arPost, "ar"}, {fx.enPost, "en"}} {
			status, body := getHTML(t, app.Handler, page.path)
			if status != 200 {
				t.Fatalf("%s %s without commerce: %d", slug, page.path, status)
			}
			checkThemePage(t, body, slug, page.locale, fx.logoURL, fx.menuLabels)
		}
		for _, path := range []string{"/shop", "/cart", "/account"} {
			if status, body := getHTML(t, app.Handler, path); status != 404 || strings.Contains(body, marker500) {
				t.Fatalf("%s %s without commerce: %d", slug, path, status)
			}
		}
	}
	var info struct {
		Data struct {
			Theme string `json:"theme"`
		} `json:"data"`
	}
	_, body := getHTML(t, app.Handler, "/api/v1/site/info")
	_ = json.Unmarshal([]byte(body), &info)
}
