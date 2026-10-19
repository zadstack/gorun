package admin_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/db"
)

func TestUpdatesDisabledUntilOperatorConfiguration(t *testing.T) {
	s := boot(t)
	s.login("updates@example.invalid", "admin", "en")
	resp, page := s.do("GET", "/admin/system/updates", nil)
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	mustContain(t, page, `data-system-updates`, `disabled`, `Deployment is not connected`)
	resp, _ = s.post("/admin/system/updates", url.Values{"confirm": {"yes"}, "url": {"https://untrusted.example"}, "token": {"fake"}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatal(resp.StatusCode)
	}
	list, err := backup.List(s.app.Deps.Config.DataDir)
	if err != nil || len(list) != 0 {
		t.Fatalf("unexpected backup %v %v", list, err)
	}
	s.login("updates-editor@example.invalid", "editor", "en")
	resp, _ = s.do("GET", "/admin/system/updates", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatal(resp.StatusCode)
	}
	resp, _ = s.post("/admin/system/updates", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatal(resp.StatusCode)
	}
}

func TestUpdatesReauthenticationAndCooldown(t *testing.T) {
	s := boot(t)
	s.login("updates-admin@example.invalid", "admin", "en")
	s.app.Deps.Config.UpdateURL = "https://coolify.example"
	s.app.Deps.Config.UpdateToken = "never-show-this-token"
	s.app.Deps.Config.UpdateResource = "site_123"
	_, page := s.do("GET", "/admin/system/updates", nil)
	mustContain(t, page, `name="confirm"`, `action="/admin/system/updates"`)
	if strings.Contains(page, "never-show-this-token") || strings.Contains(page, "coolify.example") {
		t.Fatal("operator credentials leaked")
	}
	if err := s.app.Deps.Settings.Set(context.Background(), "system.update.attempt_at", time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	resp, _ := s.post("/admin/system/updates", url.Values{"confirm": {"yes"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatal(resp.StatusCode)
	}
	list, _ := backup.List(s.app.Deps.Config.DataDir)
	if len(list) != 0 {
		t.Fatal("cooldown must precede backup and dispatch")
	}
	s.app.Deps.DB.Exec(`UPDATE sessions SET reauth_at = ?`, db.FormatTime(time.Now().Add(-11*time.Minute)))
	resp, _ = s.post("/admin/system/updates", url.Values{"confirm": {"yes"}})
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/admin/reauth?") {
		t.Fatal("stale session allowed deployment")
	}
}

func TestUpdatesBackupFailureStopsDispatch(t *testing.T) {
	s := boot(t)
	s.login("updates-backup@example.invalid", "admin", "en")
	s.app.Deps.Config.UpdateURL = "https://127.0.0.1:1"
	s.app.Deps.Config.UpdateToken = "not-a-real-token"
	s.app.Deps.Config.UpdateResource = "site_123"
	s.app.Deps.Config.DataDir += "/missing-data"
	resp, _ := s.post("/admin/system/updates", url.Values{"confirm": {"yes"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatal(resp.StatusCode)
	}
	if s.auditCount("system.update.backup_failed", "update") != 1 {
		t.Fatal("backup failure not reported")
	}
	if s.auditCount("system.update.requested", "update") != 0 {
		t.Fatal("deployment attempted without a verified backup")
	}
}
