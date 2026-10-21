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
	checked := map[string]string{"edge.manager_id": "manager-1", "edge.manager_label": "Checked manager", "edge.manager_site": "demo", "edge.checked_at": "2026-10-21T12:00:00Z"}
	for key, value := range checked {
		if err := s.app.Deps.Settings.Set(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	form.Set("key", "")
	if resp, page = s.post("/admin/settings/edge", form); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("blank key: %d\n%s", resp.StatusCode, page)
	}
	if got := s.app.Deps.Settings.GetString(ctx, "edge.manager_key", ""); got != enc {
		t.Fatalf("blank key replaced the stored key")
	}
	for key, want := range checked {
		if got := s.app.Deps.Settings.GetString(ctx, key, ""); got != want {
			t.Fatalf("unchanged connection discarded %s", key)
		}
	}
	form.Set("origin", "https://new.example")
	if resp, page = s.post("/admin/settings/edge", form); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("change origin: %d\n%s", resp.StatusCode, page)
	}
	if got := s.app.Deps.Settings.GetString(ctx, "edge.manager_key", ""); got != enc {
		t.Fatal("origin-only change replaced the key")
	}
	for key := range checked {
		if got := s.app.Deps.Settings.GetString(ctx, key, ""); got != "" {
			t.Fatalf("changed connection retained old %s", key)
		}
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

func TestEdgeSettingsSaveRollsBackPartialWrite(t *testing.T) {
	s := boot(t)
	s.login("edge-atomic@example.com", "admin", "en")
	ctx := context.Background()
	// edge.origin sorts last, so this aborts after the ciphertext insert in the
	// same transaction. A non-atomic save would leave that ciphertext behind.
	if _, err := s.app.Deps.DB.Exec(`CREATE TRIGGER edge_origin_blocked BEFORE INSERT ON settings WHEN NEW.key = 'edge.origin' BEGIN SELECT RAISE(ABORT, 'edge origin blocked'); END`); err != nil {
		t.Fatal(err)
	}
	resp, page := s.post("/admin/settings/edge", url.Values{"origin": {"https://Edge.Example"}, "key": {edgeManagerKey}})
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("save: %d\n%s", resp.StatusCode, page)
	}
	for _, key := range []string{"edge.origin", "edge.manager_key", "edge.manager_hint", "edge.checked_at"} {
		if got := s.app.Deps.Settings.GetString(ctx, key, ""); got != "" {
			t.Fatalf("%s committed after failed save: %q", key, got)
		}
	}
	mustNotContain(t, page, edgeManagerKey)
	var n int
	if err := s.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'settings.edge'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("failed save was audited: %d", n)
	}
}

func TestEdgeSettingsRequirePermission(t *testing.T) {
	s := boot(t)
	s.login("ed@example.com", "editor", "en")
	if resp, _ := s.do("GET", "/admin/settings/edge", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("editor edge settings: %d", resp.StatusCode)
	}
}
