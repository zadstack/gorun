package admin_test

import (
	"context"
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestStorageActivationIsExplicitEncryptedAndPurposeSeparated(t *testing.T) {
	s := boot(t)
	s.login("storage@example.com", "admin", "en")
	ctx := context.Background()
	form := url.Values{"name": {"Storage fixture"}, "kind": {"s3"}, "endpoint": {"https://objects.example.com"}, "bucket": {"fixture-bucket"}, "region": {"auto"}, "access_key": {"fixture-access"}, "secret_key": {"fixture-secret"}}
	if r, _ := s.post("/admin/settings/connections", form); r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
	row := savedConnection(t, s)
	id := row["id"].(string)
	version := row["version"].(string)
	if s.app.Deps.Settings.GetString(ctx, "storage.active.media", "") != "" {
		t.Fatal("profile save activated media")
	}
	for _, purpose := range []string{"media", "backup"} {
		if r, _ := s.post("/admin/settings/storage/activate", url.Values{"id": {id}, "version": {version}, "purpose": {purpose}}); r.StatusCode != 303 {
			t.Fatal(r.StatusCode)
		}
	}
	media := s.app.Deps.Settings.GetString(ctx, "storage.active.media", "")
	backup := s.app.Deps.Settings.GetString(ctx, "storage.active.backup", "")
	if media == "" || backup == "" || media == backup {
		t.Fatal("destinations not purpose isolated")
	}
	_, page := s.do("GET", "/admin/settings/storage", nil)
	mustContain(t, page, "Storage fixture")
	mustNotContain(t, page, "fixture-secret", "fixture-access")
	mustContain(t, page, `name="destination_id" type="hidden" value="`+media+`"`)
	row["test_status"] = "fail"
	rawRow, _ := json.Marshal(row)
	if err := s.app.Deps.Settings.Set(ctx, "connections.profile."+id, json.RawMessage(rawRow)); err != nil {
		t.Fatal(err)
	}
	_, failedPage := s.do("GET", "/admin/settings/storage", nil)
	mustContain(t, failedPage, "Last check failed.")
	vals, err := s.app.Deps.Settings.All(ctx, "storage.destination.")
	if err != nil || len(vals) != 2 {
		t.Fatal("missing snapshots")
	}
	for _, v := range vals {
		if strings.Contains(string(v), "fixture-secret") {
			t.Fatal("plaintext destination secret")
		}
	}
	if r, _ := s.post("/admin/settings/storage/disable", url.Values{"purpose": {"media"}, "destination_id": {media}}); r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
	if s.app.Deps.Settings.GetString(ctx, "storage.active.media", "") != "" || s.app.Deps.Settings.GetString(ctx, "storage.active.backup", "") != backup {
		t.Fatal("disable crossed purpose")
	}
	vals, _ = s.app.Deps.Settings.All(ctx, "storage.destination.")
	if len(vals) != 2 {
		t.Fatal("disable stranded old media")
	}
	if r, _ := s.post("/admin/settings/storage/activate", url.Values{"id": {id}, "version": {"stale"}, "purpose": {"media"}}); r.StatusCode != 409 {
		t.Fatal("stale activation accepted")
	}
	if r, _ := s.do("POST", "/admin/settings/storage/disable", url.Values{"purpose": {"backup"}}); r.StatusCode != 403 {
		t.Fatal("CSRF bypass")
	}
	s.login("storage-editor@example.com", "editor", "en")
	if r, _ := s.do("GET", "/admin/settings/storage", nil); r.StatusCode != 403 {
		t.Fatal("editor read storage")
	}
	if r, _ := s.post("/admin/settings/storage/activate", form); r.StatusCode != 403 {
		t.Fatal("editor activated storage")
	}
}

func TestRemoteBackupUnconfiguredDoesNotCreateFilesAndRequiresPrivilege(t *testing.T) {
	s := boot(t)
	s.login("backup-owner@example.com", "admin", "en")
	if r, _ := s.post("/admin/system/backups/remote", nil); r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
	matches, err := filepath.Glob(filepath.Join(s.app.Deps.Config.DataDir, "backups", "*"))
	if err != nil || len(matches) != 0 {
		t.Fatal("unconfigured action created backup files", err)
	}
	if got := s.app.Deps.Settings.GetString(context.Background(), "storage.site_id", ""); got != "" {
		t.Fatal("unconfigured action mutated storage identity")
	}
	if r, _ := s.do("POST", "/admin/system/backups/remote", url.Values{}); r.StatusCode != 403 {
		t.Fatal("CSRF bypass", r.StatusCode)
	}
	s.login("backup-editor@example.com", "editor", "en")
	if r, _ := s.post("/admin/system/backups/remote", nil); r.StatusCode != 403 {
		t.Fatal("editor created remote backup", r.StatusCode)
	}
}

func TestStorageDisableRejectsReplacedDestination(t *testing.T) {
	s := boot(t)
	s.login("storage-cas@example.com", "admin", "en")
	ctx := context.Background()
	if err := s.app.Deps.Settings.Set(ctx, "storage.active.media", "current-destination"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"old-destination", ""} {
		r, _ := s.post("/admin/settings/storage/disable", url.Values{"purpose": {"media"}, "destination_id": {id}})
		if r.StatusCode != 409 {
			t.Fatalf("stale disable accepted: %d", r.StatusCode)
		}
	}
	if got := s.app.Deps.Settings.GetString(ctx, "storage.active.media", ""); got != "current-destination" {
		t.Fatal("new destination disabled")
	}
	r, _ := s.post("/admin/settings/storage/disable", url.Values{"purpose": {"media"}, "destination_id": {"current-destination"}})
	if r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
}
