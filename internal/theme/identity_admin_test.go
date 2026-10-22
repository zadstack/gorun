package theme

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/brand"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/media"
)

const identityPath = "/admin/appearance/identity"

func identityClient(t *testing.T, e *env) (*client, string) {
	t.Helper()
	c := e.client()
	e.login(c, "admin@example.com", "admin")
	resp, page := c.get(identityPath)
	if resp.StatusCode != 200 {
		t.Fatalf("identity page: %d", resp.StatusCode)
	}
	return c, csrfFrom(t, page)
}

func identityForm(csrf, action string, kv ...string) url.Values {
	v := url.Values{"_csrf": {csrf}, "action": {action}, "site.title": {"Studio"}}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return v
}

func TestIdentityPageRendersFieldsAndHistory(t *testing.T) {
	e := newEnv(t, "../../themes")
	c, _ := identityClient(t, e)
	_, page := c.do("GET", identityPath, nil, "Accept-Language", "en")
	mustContain(t, page, `name="site.logo"`, `name="site.logo_light"`, `name="site.logo_dark"`, `name="site.icon"`,
		`name="identity_accent"`, `name="identity_latin_font"`, `name="identity_spacing"`, `name="custom_css"`,
		`data-identity-preview-frame`, `name="action" value="apply"`, `name="action" value="draft"`, "Latin font", "Spacing", `data-identity-form`)
	_, ar := c.do("GET", identityPath, nil, "Accept-Language", "ar")
	mustContain(t, ar, "الخط اللاتيني", "المسافات")
	resp, _ := c.get(identityPath + "/preview?view=home")
	if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("preview: %d %q", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
}

func TestIdentityApplyRedirectsSavedAndWritesKeys(t *testing.T) {
	e := newEnv(t, "../../themes")
	c, csrf := identityClient(t, e)
	resp, _ := c.post(identityPath, identityForm(csrf, "apply", "identity_accent", "#c2410c", "identity_radius", "round", "site.tagline", "Hello"))
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != identityPath+"?saved=1" {
		t.Fatalf("apply: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	ctx := adminCtx()
	if got := e.app.Deps.Settings.GetString(ctx, "theme.default.identity_accent", ""); got != "#c2410c" {
		t.Fatalf("accent = %q", got)
	}
	if got := e.app.Deps.Settings.GetString(ctx, "site.tagline", ""); got != "Hello" {
		t.Fatalf("tagline = %q", got)
	}
	_, page := c.get(identityPath + "?saved=1")
	mustContain(t, page, `role="status"`, `name="action" value="revert"`)
}

func TestIdentityDraftThenPreviewShowsDraft(t *testing.T) {
	e := newEnv(t, "../../themes")
	e.setting("theme.default.identity_accent", "#111111")
	c, csrf := identityClient(t, e)
	resp, _ := c.post(identityPath, identityForm(csrf, "draft", "identity_accent", "#c2410c", "site.title", "Draft Name"))
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != identityPath+"?draft=1" {
		t.Fatalf("draft: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if got := e.app.Deps.Settings.GetString(adminCtx(), "theme.default.identity_accent", ""); got != "#111111" {
		t.Fatalf("draft went live: %q", got)
	}
	_, preview := c.get(identityPath + "/preview?view=home")
	mustContain(t, preview, "--accent:#c2410c", "Draft Name")
	_, page := c.get(identityPath)
	mustContain(t, page, `value="#c2410c"`, `name="action" value="discard"`)
	resp, _ = c.post(identityPath, identityForm(csrf, "discard"))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("discard: %d", resp.StatusCode)
	}
	if _, ok := e.svc.IdentityDraft(adminCtx(), "default"); ok {
		t.Fatal("draft survived discard")
	}
}

func TestIdentityRevertFromPage(t *testing.T) {
	e := newEnv(t, "../../themes")
	c, csrf := identityClient(t, e)
	c.post(identityPath, identityForm(csrf, "apply", "identity_accent", "#111111"))
	c.post(identityPath, identityForm(csrf, "apply", "identity_accent", "#222222"))
	hist, _ := e.svc.IdentityHistory(adminCtx(), "default")
	first := hist[1] // newest first: [#222222, #111111, baseline]
	resp, _ := c.post(identityPath, url.Values{"_csrf": {csrf}, "action": {"revert"}, "revision": {first.ID}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != identityPath+"?reverted=1" {
		t.Fatalf("revert: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if got := e.app.Deps.Settings.GetString(adminCtx(), "theme.default.identity_accent", ""); got != "#111111" {
		t.Fatalf("accent after revert = %q", got)
	}
	if resp, _ := c.post(identityPath, url.Values{"_csrf": {csrf}, "action": {"revert"}, "revision": {"nope"}}); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown revision: %d", resp.StatusCode)
	}
}

func TestIdentityPageForThemeWithoutIdentitySupport(t *testing.T) {
	root := adminThemes(t) // "child" declares no identity support
	e := newEnv(t, root, func(c *config.Config) { c.Theme = "child" })
	c, csrf := identityClient(t, e)
	_, page := c.do("GET", identityPath, nil, "Accept-Language", "en")
	mustContain(t, page, "does not accept identity settings", `name="site.logo"`, `name="site.title"`)
	if !regexp.MustCompile(`<input[^>]*name="identity_accent"[^>]*\bdisabled\b`).MatchString(page) {
		t.Fatal("identity fields must be disabled for a theme without identity support")
	}
	if resp, _ := c.get(identityPath + "/preview?view=single"); resp.StatusCode != 200 {
		t.Fatalf("preview: %d", resp.StatusCode)
	}
	resp, _ := c.post(identityPath, identityForm(csrf, "apply", "site.title", "Renamed", "identity_accent", "#c2410c"))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("apply: %d", resp.StatusCode)
	}
	if got := e.app.Deps.Settings.GetString(adminCtx(), "site.title", ""); got != "Renamed" {
		t.Fatalf("title = %q", got)
	}
	if got := e.app.Deps.Settings.GetString(adminCtx(), "theme.child.identity_accent", ""); got != "" {
		t.Fatalf("identity value stored for unsupported theme: %q", got)
	}
}

func TestIdentityContrastRejected(t *testing.T) {
	e := newEnv(t, "../../themes")
	c, csrf := identityClient(t, e)
	resp, page := c.post(identityPath, identityForm(csrf, "apply", "identity_text", "#ffffff", "identity_background", "#ffffff"))
	if resp.StatusCode != 422 {
		t.Fatalf("contrast: %d", resp.StatusCode)
	}
	mustContain(t, page, `data-design-error`, "contrast")
	if got := e.app.Deps.Settings.GetString(adminCtx(), "theme.default.identity_text", ""); got != "" {
		t.Fatalf("invalid text stored: %q", got)
	}
	// A good pair shows the ratio.
	resp, _ = c.post(identityPath, identityForm(csrf, "draft", "identity_text", "#111111", "identity_background", "#ffffff"))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("draft: %d", resp.StatusCode)
	}
	_, page = c.get(identityPath)
	mustContain(t, page, `data-identity-contrast`, "18.")
}

func TestIdentityImportFromBuilder(t *testing.T) {
	e := newEnv(t, "../../themes")
	e.setting("builder.identity", brand.Identity{Accent: "#167252", Background: "#f8faf9", Text: "#172b29", Font: "zad", Width: "wide"})
	c, csrf := identityClient(t, e)
	_, page := c.get(identityPath)
	mustContain(t, page, `name="action" value="import"`)
	resp, _ := c.post(identityPath, url.Values{"_csrf": {csrf}, "action": {"import"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != identityPath+"?imported=1" {
		t.Fatalf("import: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	d, ok := e.svc.IdentityDraft(adminCtx(), "default")
	if !ok || d.Snapshot.Get(idAccent) != "#167252" || d.Snapshot.Get(idBody) != "zad" || d.Snapshot.Get(idText) != "#172b29" {
		t.Fatalf("draft = %+v ok=%v", d, ok)
	}
	var b brand.Identity
	if err := e.app.Deps.Settings.Get(adminCtx(), "builder.identity", &b); err != nil || b.Width != "wide" {
		t.Fatalf("builder identity changed: %+v %v", b, err)
	}
	if got := e.app.Deps.Settings.GetString(adminCtx(), "theme.default.identity_accent", ""); got != "" {
		t.Fatal("import must not apply live")
	}
}

func TestIdentityLogoValidationOnPage(t *testing.T) {
	e := newEnv(t, "../../themes")
	e.fake.Media.Add(media.Media{ID: "m1", Filename: "logo.png", Mime: "image/png", URL: "/media/logo.png"})
	c, csrf := identityClient(t, e)
	if resp, page := c.post(identityPath, identityForm(csrf, "apply", "site.logo_dark", "missing")); resp.StatusCode != 422 || !strings.Contains(page, "data-design-error") {
		t.Fatalf("missing logo accepted: %d", resp.StatusCode)
	}
	if resp, _ := c.post(identityPath, identityForm(csrf, "apply", "site.logo_dark", "m1")); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("existing logo rejected: %d", resp.StatusCode)
	}
	if got := e.app.Deps.Settings.GetString(adminCtx(), "site.logo_dark", ""); got != "m1" {
		t.Fatalf("logo_dark = %q", got)
	}
}

// A settings-only user lands on Identity with no Build tab (the site design
// workspace contract that moved here from the builder).
func TestIdentityPageHidesBuildTabForSettingsOnlyUser(t *testing.T) {
	e := newEnv(t, "../../themes")
	const role = "design-settings-only"
	if _, err := e.app.Deps.DB.Exec(`INSERT INTO roles(slug,name_json,system) VALUES(?, ?,0)`, role, `{"en":"Design settings"}`); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"settings.manage", "media.upload"} {
		if _, err := e.app.Deps.DB.Exec(`INSERT INTO role_permissions(role,permission) VALUES(?,?)`, role, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := auth.ReloadRoles(context.Background(), e.app.Deps.DB); err != nil {
		t.Fatal(err)
	}
	c := e.client()
	e.login(c, "limited@example.com", role)
	resp, body := c.get(identityPath)
	if resp.StatusCode != 200 {
		t.Fatalf("settings-only identity page: %d", resp.StatusCode)
	}
	start := strings.Index(body, `<nav class="site-design-tabs"`)
	end := strings.Index(body[start:], "</nav>")
	if start < 0 || end < 0 {
		t.Fatal("workspace tabs missing")
	}
	tabs := body[start : start+end]
	if strings.Contains(tabs, `href="/admin/site-builder"`) || !strings.Contains(tabs, `href="/admin/appearance/identity"`) {
		t.Fatal("settings-only user received Build tab")
	}
}
