package theme

import (
	"github.com/zadstack/gorun/internal/kernel"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/media"
)

func previewRequest(e *env, o IdentityPreviewOptions) *httptest.ResponseRecorder {
	e.t.Helper()
	ctx := adminCtx()
	r := httptest.NewRequest(http.MethodGet, "/admin/appearance/identity/preview", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	if err := e.svc.RenderIdentityPreview(ctx, rec, r, o); err != nil {
		e.t.Fatal(err)
	}
	return rec
}

func TestIdentityPreviewInjectsDraftWithoutPersisting(t *testing.T) {
	e := newEnv(t, "../../themes")
	e.setting("theme.default.identity_accent", "#111111")
	e.setting("site.title", "Live Title")
	rec := previewRequest(e, IdentityPreviewOptions{View: "home", Locale: "en", Snapshot: snap("default", map[string]string{idAccent: "#c2410c"}, map[string]string{"site.title": "Draft Title", "site.tagline": "Draft tagline"})})
	body := rec.Body.String()
	for _, want := range []string{"--accent:#c2410c", "Draft Title", "<style nonce=", `data-gorun-identity`} {
		if !strings.Contains(body, want) {
			t.Errorf("preview missing %q", want)
		}
	}
	for _, bad := range []string{"/site/identity.css", "--accent:#111111", "Live Title"} {
		if strings.Contains(body, bad) {
			t.Errorf("preview must not contain %q", bad)
		}
	}
	if got := e.app.Deps.Settings.GetString(adminCtx(), "theme.default.identity_accent", ""); got != "#111111" {
		t.Fatalf("preview persisted the draft: %q", got)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'self'") {
		t.Errorf("CSP = %q", csp)
	}
}

func TestIdentityPreviewSingleFallsBackToHome(t *testing.T) {
	e := newEnv(t, "../../themes")
	rec := previewRequest(e, IdentityPreviewOptions{View: "single", Locale: "en", Snapshot: snap("default", nil, nil)})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `<html`) {
		t.Fatalf("home fallback: %d", rec.Code)
	}
	e.fake.Content.Add(content.Item{ID: "post1", Type: "post", Title: "Published Preview Post", Slug: "preview-post", Status: "published", Locale: "en", Body: "<p>Body</p>"})
	rec = previewRequest(e, IdentityPreviewOptions{View: "single", Locale: "en", Snapshot: snap("default", nil, nil)})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Published Preview Post") {
		t.Fatalf("single preview missing the post: %d", rec.Code)
	}
}

func TestIdentityPreviewLogoOverride(t *testing.T) {
	e := newEnv(t, "../../themes")
	e.fake.Media.Add(media.Media{ID: "logo1", Filename: "logo.png", Mime: "image/png", URL: "/media/draft-logo.png", Width: 200, Height: 60})
	rec := previewRequest(e, IdentityPreviewOptions{View: "home", Locale: "en", Snapshot: snap("default", nil, map[string]string{"site.title": "S", "site.logo": "logo1"})})
	if !strings.Contains(rec.Body.String(), "draft-logo.png") {
		t.Fatal("draft logo not rendered")
	}
	if got := e.app.Deps.Settings.GetString(adminCtx(), "site.logo", ""); got != "" {
		t.Fatalf("site.logo persisted: %q", got)
	}
}

// The preview must keep its origin so bundled fonts load, allow https
// images like the public policy, and be frameable by the admin page.
func TestIdentityPreviewHeadersAllowFontsImagesAndFraming(t *testing.T) {
	e := newEnv(t, "../../themes")
	rec := previewRequest(e, IdentityPreviewOptions{View: "home", Locale: "en", Snapshot: snap("default", nil, nil)})
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"sandbox allow-same-origin", "img-src 'self' data: blob: https:", "frame-ancestors 'self'", "script-src 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing %q: %s", want, csp)
		}
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Errorf("X-Frame-Options = %q", got)
	}
}

// Templates cannot read the draft or the revision list through {{setting}}.
func TestSettingFuncHidesIdentityDraftAndHistory(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", baseTheme(map[string]string{"templates/index.html": `{{part "header"}}D[{{setting "theme.default.identity_draft"}}]H[{{setting "theme.default.identity_history"}}]A[{{setting "theme.default.identity_accent"}}]{{part "footer"}}`}))
	e := newEnv(t, root)
	e.setting("theme.default.identity_draft", map[string]any{"snapshot": map[string]any{"theme": "default", "site": map[string]string{"site.title": "Secret"}}})
	e.setting("theme.default.identity_history", []map[string]any{{"id": "r1", "author": "u1"}})
	e.setting("theme.default.identity_accent", "#c2410c")
	rec, err := e.render(kernel.KindRequest{Kind: KindHome}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, rec.Body.String(), "D[]H[]A[#c2410c]")
}
