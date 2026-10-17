package ext_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/ext"
	"github.com/zadstack/gorun/internal/kernel"
)

func sampleManifest(endpoint string) map[string]any {
	m := manifest("sample", endpoint, hook("content.published", "event", 0))
	m["content_types"] = []any{map[string]any{
		"slug": "greeting", "name": map[string]string{"en": "Greetings", "ar": "التحيات"},
		"public": true, "supports": []string{"title", "body"},
		"fields": []any{map[string]any{"key": "mood", "label": map[string]string{"en": "Mood"}, "type": "text"}},
	}}
	m["settings"] = []any{map[string]any{"key": "greeting_word", "label": map[string]string{"en": "Word"}, "type": "text", "default": "hello"}}
	return m
}

func TestInstallRegistersContentTypesAndSettings(t *testing.T) {
	e := boot(t)
	res := e.install(sampleManifest("http://127.0.0.1:1/hook"))
	if res.Secret == "" || !strings.HasPrefix(res.APIKey, "gp_") {
		t.Fatalf("install must reveal secret and key once: %+v", res)
	}
	if res.Extension.Status != ext.StatusInstalled || res.Extension.Version != "1.0.0" {
		t.Fatalf("extension = %+v", res.Extension)
	}
	typ, err := e.content.GetType(context.Background(), "greeting")
	if err != nil {
		t.Fatalf("declared type not registered: %v", err)
	}
	if typ.System || len(typ.Fields) != 1 || typ.Fields[0].Key != "mood" {
		t.Fatalf("type = %+v", typ)
	}
	st := e.app.Deps.Settings
	if v := st.GetString(context.Background(), "ext.sample.settings.greeting_word", ""); v != "hello" {
		t.Fatalf("setting default = %q", v)
	}
	var types []string
	if err := st.Get(context.Background(), "ext.sample.types", &types); err != nil || !slices.Equal(types, []string{"greeting"}) {
		t.Fatalf("types bookkeeping = %v, %v", types, err)
	}
	if _, err := os.Stat(filepath.Join(e.app.Deps.Config.DataDir, "extensions", "sample", "manifest.json")); err != nil {
		t.Fatalf("package not stored under DataDir/extensions: %v", err)
	}
	var secretEnc string
	if err := e.app.Deps.DB.QueryRow(`SELECT secret_enc FROM extensions WHERE slug = 'sample'`).Scan(&secretEnc); err != nil {
		t.Fatal(err)
	}
	if secretEnc == "" || strings.Contains(secretEnc, res.Secret) {
		t.Fatal("secret must be stored encrypted")
	}
	var audits int
	e.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'ext.install' AND target_id = 'sample'`).Scan(&audits)
	if audits != 1 {
		t.Fatalf("ext.install audits = %d", audits)
	}
	// Installing the same slug again is refused.
	_, err = e.svc.InstallDir(e.ctx, writeManifest(t, sampleManifest("http://127.0.0.1:1/hook")))
	var ve *ext.ValidationError
	if !errors.As(err, &ve) || ve.Fields["slug"] == "" {
		t.Fatalf("reinstall: %v", err)
	}
	// A manifest colliding with a system type is refused.
	m := manifest("clash", "http://127.0.0.1:1/hook")
	m["content_types"] = []any{map[string]any{"slug": "post", "name": map[string]string{"en": "Posts"}}}
	if _, err := e.svc.InstallDir(e.ctx, writeManifest(t, m)); !errors.As(err, &ve) || ve.Fields["content_types.0.slug"] == "" {
		t.Fatalf("system type collision: %v", err)
	}
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestInstallZipIsZipSlipSafe(t *testing.T) {
	e := boot(t)
	raw := `{"slug":"zipped","name":{"en":"Zipped"},"version":"0.1.0","permissions":[],"hooks":[],"endpoint":{"url":"","auth":""}}`
	good := zipOf(t, map[string]string{"zipped/manifest.json": raw, "zipped/assets/logo.svg": "<svg/>", "zipped/server.exe": "MZ"})
	res, err := e.svc.InstallZip(e.ctx, bytes.NewReader(good), int64(len(good)))
	if err != nil {
		t.Fatalf("zip install: %v", err)
	}
	if res.APIKey != "" {
		t.Fatal("an extension without permissions gets no API key")
	}
	dir := filepath.Join(e.app.Deps.Config.DataDir, "extensions", "zipped")
	if _, err := os.Stat(filepath.Join(dir, "assets", "logo.svg")); err != nil {
		t.Fatalf("assets not extracted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "server.exe")); !os.IsNotExist(err) {
		t.Fatal("only manifest.json and assets/ may be extracted")
	}
	evil := zipOf(t, map[string]string{"manifest.json": strings.Replace(raw, "zipped", "evil", 1), "assets/../../../escape.txt": "x"})
	if _, err := e.svc.InstallZip(e.ctx, bytes.NewReader(evil), int64(len(evil))); err == nil {
		t.Fatal("zip-slip entry accepted")
	}
	if _, err := os.Stat(filepath.Join(e.app.Deps.Config.DataDir, "escape.txt")); !os.IsNotExist(err) {
		t.Fatal("zip-slip wrote outside the extension dir")
	}
	if _, err := e.svc.InstallZip(e.ctx, bytes.NewReader([]byte("not a zip")), 9); err == nil {
		t.Fatal("garbage zip accepted")
	}
}

func TestUninstallPurgeOnlyWhenAsked(t *testing.T) {
	e := boot(t)
	res := e.install(sampleManifest("http://127.0.0.1:1/hook"))
	it, err := e.content.Create(e.ctx, content.Input{Type: "greeting", Title: "Salam", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Uninstall(e.ctx, "sample", false); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := e.svc.Status(context.Background(), "sample"); !errors.Is(err, ext.ErrNotFound) {
		t.Fatalf("extension row must be gone: %v", err)
	}
	if _, err := e.content.Get(context.Background(), it.ID); err != nil {
		t.Fatalf("content must be kept without purge: %v", err)
	}
	if _, err := e.content.GetType(context.Background(), "greeting"); err != nil {
		t.Fatalf("type must be kept without purge: %v", err)
	}
	if v := e.app.Deps.Settings.GetString(context.Background(), "ext.sample.settings.greeting_word", ""); v != "hello" {
		t.Fatalf("settings must be kept without purge: %q", v)
	}
	keys, _ := kernel.Get[auth.KeyService](e.app.Deps, kernel.ServiceAPIKeys)
	list, _ := keys.List(context.Background())
	for _, k := range list {
		if k.ID == res.Extension.APIKeyID && k.RevokedAt == "" {
			t.Fatal("API key must be revoked at uninstall")
		}
	}

	// Reinstalling may redeclare its own (kept) type; purge then removes all.
	e.install(sampleManifest("http://127.0.0.1:1/hook"))
	if err := e.svc.Uninstall(e.ctx, "sample", true); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if _, err := e.content.Get(context.Background(), it.ID); !errors.Is(err, content.ErrNotFound) {
		t.Fatalf("content must be purged: %v", err)
	}
	if _, err := e.content.GetType(context.Background(), "greeting"); !errors.Is(err, content.ErrNotFound) {
		t.Fatalf("type must be purged: %v", err)
	}
	all, _ := e.app.Deps.Settings.All(context.Background(), "ext.sample.")
	if len(all) != 0 {
		t.Fatalf("settings must be purged: %v", all)
	}
	if _, err := os.Stat(filepath.Join(e.app.Deps.Config.DataDir, "extensions", "sample")); !os.IsNotExist(err) {
		t.Fatal("purge removes the package directory")
	}
	var audits int
	e.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'ext.uninstall'`).Scan(&audits)
	if audits != 2 {
		t.Fatalf("ext.uninstall audits = %d", audits)
	}
}

func TestExtensionAPIKeyScopedToManifestPermissions(t *testing.T) {
	e := boot(t)
	res := e.install(manifest("reader", "http://127.0.0.1:1/hook"))
	keys, _ := kernel.Get[auth.KeyService](e.app.Deps, kernel.ServiceAPIKeys)
	list, _ := keys.List(context.Background())
	var found *auth.APIKey
	for i := range list {
		if list[i].ID == res.Extension.APIKeyID {
			found = &list[i]
		}
	}
	if found == nil || found.Name != "ext:reader" || !slices.Equal(found.Scopes, []string{"content.view"}) {
		t.Fatalf("key = %+v", found)
	}
	c := apitest.New(t, e.app, apitest.Options{})
	c.Token = res.APIKey
	if code, body := c.Get("/v1/content?type=post"); code != http.StatusOK {
		t.Fatalf("content.view call: %d %v", code, body)
	}
	if code, _ := c.Post("/v1/content", map[string]any{"type": "post", "title": "x"}); code != http.StatusForbidden {
		t.Fatalf("content.create must be forbidden: %d", code)
	}
	if code, _ := c.Get("/v1/extensions"); code != http.StatusForbidden {
		t.Fatalf("extensions.manage must be forbidden: %d", code)
	}
	if err := e.svc.Uninstall(e.ctx, "reader", false); err != nil {
		t.Fatal(err)
	}
	if code, _ := c.Get("/v1/content?type=post"); code != http.StatusUnauthorized {
		t.Fatalf("revoked key: %d", code)
	}
}
