package wpmigrate

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

func bucketOf(t *testing.T, rep *Report, kind string) (*Finding, string) {
	t.Helper()
	f, b := rep.Find(kind)
	return f, b
}

// TestDiscoveryBucketsFromFixture scans the fictional example.org REST
// responses (authenticated via the synthetic overlay, since raw markup,
// users and menus need an application password).
func TestDiscoveryBucketsFromFixture(t *testing.T) {
	e := newEnv(t)
	e.wp = newFixtureServer(t)
	src, err := e.svc.CreateSource(e.ctx(), SourceInput{URL: e.wp.URL(), Username: fixtureUser, AppPassword: fixturePass})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := e.svc.Discover(e.ctx(), src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Auth.Mode != "app_password" || !rep.Auth.OK || rep.Site.Name != "EXAMPLE PRESS" || rep.Site.Version != "7.1.3" || rep.Site.Locale != "ar" {
		t.Fatalf("site/auth = %+v %+v", rep.Site, rep.Auth)
	}
	if f, b := bucketOf(t, rep, "settings"); f == nil || b != "available" {
		t.Fatalf("settings with auth = %v %s", f, b)
	}
	if f, b := bucketOf(t, rep, "seo_yoast"); f == nil || b != "available" {
		t.Fatalf("yoast = %v %s", f, b)
	}
	if f, b := bucketOf(t, rep, "products"); f == nil || b != "available" || f.Count != 36 || !rep.Plugins["woocommerce"] {
		t.Fatalf("woocommerce = %+v %s", f, b)
	}
	if rep.Plugins["acf"] {
		t.Fatal("ACF must be absent")
	}
	if f, _ := bucketOf(t, rep, "acf"); f != nil {
		t.Fatal("no ACF finding expected")
	}
	if f, _ := bucketOf(t, rep, "acf_hidden"); f != nil {
		t.Fatal("no ACF finding expected")
	}
	tpl, b := bucketOf(t, rep, "custom_templates")
	if tpl == nil || b != "unavailable" || tpl.Count != 8 {
		t.Fatalf("custom templates = %+v %s", tpl, b)
	}
	if tpl.Sampled {
		t.Fatal("all accessible advertised pages were scanned; this is not a first-page sample")
	}
	if f, _ := bucketOf(t, rep, "products"); f.Note["en"] == "" || !strings.Contains(f.Note["en"], "commerce module is not installed") {
		t.Fatalf("products note without commerce = %v", f.Note)
	}
	wantSlugs := []string{"compass-kit", "garden-kit", "lantern-kit", "paper-kit", "platforms", "poster-kit", "pricing", "recipe-kit"}
	if !slices.Equal(tpl.Items, wantSlugs) {
		t.Fatalf("template pages = %v, want %v", tpl.Items, wantSlugs)
	}
	if rep.Templates["template-ep-product.php"] != 6 || rep.Templates["template-pricing.php"] != 1 {
		t.Fatalf("templates = %v", rep.Templates)
	}
	for _, sc := range []string{"ep_plans", "ep_platform_cards", "ep_contact_form"} {
		if rep.Shortcodes[sc] == 0 {
			t.Fatalf("shortcode %s not counted: %v", sc, rep.Shortcodes)
		}
	}
	if f, b := bucketOf(t, rep, "shortcodes"); f == nil || b != "unavailable" {
		t.Fatalf("shortcodes finding = %+v %s", f, b)
	}
	if f, b := bucketOf(t, rep, "raw_content"); f == nil || b != "available" {
		t.Fatalf("raw content with auth = %+v %s", f, b)
	}
	if f, b := bucketOf(t, rep, "menus"); f == nil || b != "available" || f.Count != 1 {
		t.Fatalf("menus with auth = %+v %s", f, b)
	}
	if f, b := bucketOf(t, rep, "plugin_pages"); f == nil || b != "unavailable" || !slices.Contains(f.Items, "cart") {
		t.Fatalf("plugin pages = %+v %s", f, b)
	}
	if rep.Counts["post"] != 11 || rep.Counts["page"] != 27 || rep.Counts["media"] != 155 || rep.Counts["category"] != 8 {
		t.Fatalf("counts = %v", rep.Counts)
	}
	// The fixture site serves every file from its own host: no offload.
	if rep.OffloadedMediaHost != "" {
		t.Fatalf("offloaded host = %q", rep.OffloadedMediaHost)
	}
	for _, f := range append(append(rep.Available, rep.Partial...), rep.Unavailable...) {
		if f.Label["ar"] == "" || f.Label["en"] == "" {
			t.Fatalf("finding %s lacks bilingual label", f.Kind)
		}
	}

	t.Run("media offloader is partial", func(t *testing.T) {
		// Synthetic: the first item of each media page moved to a CDN host.
		move := func(b []byte) []byte {
			var list []map[string]any
			if err := json.Unmarshal(b, &list); err != nil || len(list) == 0 {
				return b
			}
			u, _ := list[0]["source_url"].(string)
			list[0]["source_url"] = strings.Replace(u, e.wp.URL(), "https://cdn.offload.example", 1)
			out, _ := json.Marshal(list)
			return out
		}
		e.wp.override("/wp-json/wp/v2/media?page=2", move)
		e.wp.override("/wp-json/wp/v2/media", move)
		defer e.wp.override("/wp-json/wp/v2/media?page=2", nil)
		defer e.wp.override("/wp-json/wp/v2/media", nil)
		rep, err := e.svc.Discover(e.ctx(), src.ID)
		if err != nil {
			t.Fatal(err)
		}
		f, b := bucketOf(t, rep, "media_offloaded")
		if f == nil || b != "partial" || rep.OffloadedMediaHost != "cdn.offload.example" || f.Count != 2 {
			t.Fatalf("offload = %+v %s host=%q", f, b, rep.OffloadedMediaHost)
		}
	})

	t.Run("anonymous puts auth-dependent items in partial", func(t *testing.T) {
		anon, err := e.svc.CreateSource(e.ctx(), SourceInput{URL: e.wp.URL()})
		if err != nil {
			t.Fatal(err)
		}
		rep, err := e.svc.Discover(e.ctx(), anon.ID)
		if err != nil {
			t.Fatal(err)
		}
		if rep.Auth.Mode != "anonymous" || !rep.Auth.OK {
			t.Fatalf("auth = %+v", rep.Auth)
		}
		for _, kind := range []string{"raw_content_auth", "menus_auth", "users_endpoint_private", "product_meta", "settings_public"} {
			if _, b := bucketOf(t, rep, kind); b != "partial" {
				t.Errorf("%s bucket = %q, want partial", kind, b)
			}
		}
		if len(rep.Shortcodes) != 0 {
			t.Errorf("rendered public content has no shortcodes, got %v", rep.Shortcodes)
		}
		if f, _ := bucketOf(t, rep, "custom_templates"); f == nil || f.Count != 8 {
			t.Errorf("templates are public: %+v", f)
		}
	})
}

// TestDiscoveryAuthFailureIsExplicit: wrong credentials never degrade to an
// anonymous scan.
func TestDiscoveryAuthFailureIsExplicit(t *testing.T) {
	e := newEnv(t)
	e.wp = newFixtureServer(t)
	src, err := e.svc.CreateSource(e.ctx(), SourceInput{URL: e.wp.URL(), Username: fixtureUser, AppPassword: "wrong password value"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := e.svc.CreateMigration(e.ctx(), src.ID, Options{}, e.adm.ID)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := e.svc.Discover(e.ctx(), src.ID)
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("err = %v, want ErrAuthFailed", err)
	}
	if rep == nil || rep.Auth.Mode != "app_password" || rep.Auth.OK || rep.Auth.Error != "auth_failed" {
		t.Fatalf("auth = %+v", rep)
	}
	if len(rep.Available) != 0 || len(rep.Partial) != 0 || len(rep.Unavailable) != 0 || len(rep.Counts) != 0 {
		t.Fatalf("an auth failure must not report buckets: %+v", rep)
	}
	var stored string
	if err := e.app.Deps.DB.QueryRow(`SELECT last_discovery_json FROM wp_sources WHERE id = ?`, src.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var back Report
	if err := json.Unmarshal([]byte(stored), &back); err != nil || back.Auth.Error != "auth_failed" {
		t.Fatalf("stored report = %s", stored)
	}
	if st, _ := e.svc.Migration(e.ctx(), m.ID); st.Status != StatusDraft {
		t.Fatalf("migration must stay draft, got %s", st.Status)
	}
	// The password never appears in what the service returns.
	view, _ := e.svc.Source(e.ctx(), src.ID)
	if b, _ := json.Marshal(view); strings.Contains(string(b), "wrong password") || !view.HasPassword {
		t.Fatalf("source view leaks or lost the password: %s", b)
	}
	var enc string
	_ = e.app.Deps.DB.QueryRow(`SELECT app_password_enc FROM wp_sources WHERE id = ?`, src.ID).Scan(&enc)
	if enc == "" || strings.Contains(enc, "wrong") {
		t.Fatalf("password not encrypted at rest: %q", enc)
	}
}
