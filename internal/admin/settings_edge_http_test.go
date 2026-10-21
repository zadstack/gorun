package admin_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/db"
)

const edgeManagerKey = "edge-manager-key-value-0123456789ABCDEFGHIJ"

func TestEdgeSettingsEncryptKeyAndKeepBlank(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	ctx := context.Background()
	form := url.Values{"origin": {"https://Edge.Example"}, "key": {edgeManagerKey}}
	resp, page := s.post("/admin/settings/edge", form)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save: %d\n%s", resp.StatusCode, page)
	}
	enc := s.app.Deps.Settings.GetString(ctx, "edge.manager_key", "")
	if enc == "" || strings.Contains(enc, edgeManagerKey) {
		t.Fatalf("key must be stored encrypted: %q", enc)
	}
	plain, err := db.Decrypt(s.app.Deps.Config.SecretKey, enc, "edge.manager_key")
	if err != nil || string(plain) != edgeManagerKey {
		t.Fatalf("decrypt: %q %v", plain, err)
	}
	if got := s.app.Deps.Settings.GetString(ctx, "edge.origin", ""); got != "https://edge.example" {
		t.Fatalf("origin: %q", got)
	}
	if got := s.app.Deps.Settings.GetString(ctx, "edge.manager_hint", ""); got != "GHIJ" {
		t.Fatalf("hint: %q", got)
	}
	_, page = s.do("GET", "/admin/settings/edge", nil)
	mustContain(t, page, "https://edge.example", "••••GHIJ", "Edge manager")
	mustNotContain(t, page, edgeManagerKey, enc)
	form.Set("key", "")
	if resp, page = s.post("/admin/settings/edge", form); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("blank key: %d\n%s", resp.StatusCode, page)
	}
	if got := s.app.Deps.Settings.GetString(ctx, "edge.manager_key", ""); got != enc {
		t.Fatalf("blank key replaced the stored key")
	}
	var meta string
	if err := s.app.Deps.DB.QueryRow(`SELECT meta_json FROM audit_log WHERE action = 'settings.edge' ORDER BY id DESC LIMIT 1`).Scan(&meta); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(meta, edgeManagerKey) || strings.Contains(meta, enc) {
		t.Fatalf("audit stored the key: %s", meta)
	}
}

func TestEdgeSettingsRejectPrivateOriginAndFailedCheck(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	ctx := context.Background()
	for _, origin := range []string{"http://edge.example", "https://127.0.0.1", "https://10.0.0.8", "https://localhost/admin"} {
		resp, page := s.post("/admin/settings/edge", url.Values{"origin": {origin}, "key": {edgeManagerKey}})
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("%s: %d", origin, resp.StatusCode)
		}
		mustContain(t, page, "field-error")
		mustNotContain(t, page, edgeManagerKey)
		if got := s.app.Deps.Settings.GetString(ctx, "edge.manager_key", ""); got != "" {
			t.Fatalf("%s stored a key", origin)
		}
	}
	resp, _ := s.post("/admin/settings/edge", url.Values{"origin": {"https://edge-manager-check.invalid"}, "key": {edgeManagerKey}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save unreachable origin: %d", resp.StatusCode)
	}
	resp, page := s.post("/admin/settings/edge/check", url.Values{})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("check: %d\n%s", resp.StatusCode, page)
	}
	mustContain(t, page, "did not accept")
	mustNotContain(t, page, edgeManagerKey)
	if got := s.app.Deps.Settings.GetString(ctx, "edge.checked_at", ""); got != "" {
		t.Fatalf("failed check recorded success: %q", got)
	}
}

func TestEdgeSettingsRequirePermission(t *testing.T) {
	s := boot(t)
	s.login("ed@example.com", "editor", "en")
	if resp, _ := s.do("GET", "/admin/settings/edge", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("editor edge settings: %d", resp.StatusCode)
	}
}
