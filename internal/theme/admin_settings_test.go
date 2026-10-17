package theme

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

const settingsThemeJSON = `{"name":"Settings Test","version":"1.0.0","locations":["primary"],"settings":[
 {"key":"hero_title","type":"text","label":{"en":"Hero title","ar":"عنوان"}},
 {"key":"columns","type":"number","label":{"en":"Columns"},"min":1,"max":4},
 {"key":"dark","type":"boolean","label":{"en":"Dark band"}},
 {"key":"style","type":"select","label":{"en":"Style"},"options":[{"value":"flat","label":{"en":"Flat"}},{"value":"line","label":{"en":"Line"}}]},
 {"key":"hero_image","type":"image","label":{"en":"Hero image"}},
 {"key":"blurb","type":"textarea","label":{"en":"Blurb"}},
 {"key":"launch","type":"date","label":{"en":"Launch date"}},
 {"key":"notes","type":"repeater","label":{"en":"Unsupported type"}}]}`

// bootRealTheme boots settings + auth + content + media + theme on root.
func bootRealTheme(t *testing.T, root string) *env {
	t.Helper()
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://example.test", Theme: "default"}
	mod := New(WithThemesDir(root), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), auth.New(), content.New(), media.New(), mod})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	return &env{t: t, app: app, svc: mod.svc, root: root, cfg: cfg}
}

// I4(a): Appearance → Theme settings renders the active theme's schema
// with the field partials and stores theme.<slug>.<key> (validated,
// audited, permission themes.manage).
func TestThemeSettingsPage(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", settingsThemeJSON, map[string]string{"index.html": `{{define "content"}}x{{end}}`,
		"layouts/base.html": `<html><body>{{block "content" .}}{{end}}</body></html>`})
	e := bootRealTheme(t, root)
	ctx := context.Background()
	c := e.client()
	e.login(c, "admin@example.com", "admin")

	resp, page := c.get("/admin/appearance/theme-settings")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page: %d\n%s", resp.StatusCode, page)
	}
	mustContain(t, page, `name="settings[hero_title]"`, `name="settings[columns]"`, `type="checkbox"`, `<option value="line"`,
		`data-field-type="image"`, `data-field-type="textarea"`, `data-field-type="date"`, `data-media-open`, "gp-media-picker-body", `name="settings[notes]"`, `href="/admin/appearance/theme-settings"`)
	tok := csrfFrom(t, page)

	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	ms, _ := kernel.Get[media.Service](e.app.Deps, kernel.ServiceMedia)
	m, err := ms.Upload(ctx, "hero.png", &buf, media.UploadMeta{})
	if err != nil {
		t.Fatal(err)
	}
	form := func(cols, style, img string) url.Values {
		return url.Values{"_csrf": {tok}, "settings[hero_title]": {"  مرحبا "}, "settings[columns]": {cols},
			"settings[dark]": {"0", "1"}, "settings[style]": {style}, "settings[hero_image]": {img}, "settings[notes]": {"free text"},
			"settings[blurb]": {"line one\nline two"}, "settings[launch]": {"2026-11-01"}}
	}
	for name, f := range map[string]url.Values{"number above max": form("9", "flat", ""), "unknown option": form("2", "bold", ""),
		"missing media": form("2", "flat", "01MISSINGMEDIA0000000000000")} {
		if resp, _ := c.post("/admin/appearance/theme-settings", f); resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("%s: %d", name, resp.StatusCode)
		}
	}
	if resp, body := c.post("/admin/appearance/theme-settings", form("3", "line", m.ID)); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save: %d\n%s", resp.StatusCode, body)
	}
	get := func(k string) any {
		var v any
		_ = e.app.Deps.Settings.Get(ctx, "theme.default."+k, &v)
		return v
	}
	if get("hero_title") != "مرحبا" || get("columns") != 3.0 || get("dark") != true || get("style") != "line" || get("hero_image") != m.ID || get("notes") != "free text" ||
		get("blurb") != "line one\nline two" || get("launch") != "2026-11-01" {
		t.Fatalf("stored: %v %v %v %v %v %v", get("hero_title"), get("columns"), get("dark"), get("style"), get("hero_image"), get("notes"))
	}
	var n int
	_ = e.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'theme.settings.update' AND target_id = 'default'`).Scan(&n)
	if n != 1 {
		t.Fatalf("audit rows = %d", n)
	}
	_, page = c.get("/admin/appearance/theme-settings")
	mustContain(t, page, `value="مرحبا"`, `value="line" selected`, "checked")

	// No CSRF → refused; editors lack themes.manage.
	if resp, _ := c.post("/admin/appearance/theme-settings", url.Values{"settings[hero_title]": {"x"}}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("without csrf: %d", resp.StatusCode)
	}
	ed := e.client()
	e.login(ed, "editor@example.com", "editor")
	if resp, _ := ed.get("/admin/appearance/theme-settings"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("editor: %d", resp.StatusCode)
	}
}
