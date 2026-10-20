package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/media"
)

func TestCoreRemoteBackupHTTPFailedUploadRetainsInspectableLocalPair(t *testing.T) {
	s := boot(t)
	s.login("backup-failure@example.invalid", "admin", "en")
	ctx := context.Background()
	dest, err := media.PrepareDestination("Synthetic unreachable backup", "backup", media.S3Config{
		Endpoint: "https://objects.example.invalid", Region: "auto", Bucket: "isolated-fixture",
		AccessKey: "synthetic-backup-access", SecretKey: "synthetic-backup-secret",
	}, s.app.Deps.Config.SecretKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := media.SaveDestination(ctx, s.app.Deps.DB, s.app.Deps.Settings, dest); err != nil {
		t.Fatal(err)
	}
	var dnsCalls atomic.Int64
	original := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		dnsCalls.Add(1)
		return nil, errors.New("synthetic resolver denies all network")
	}}
	t.Cleanup(func() { net.DefaultResolver = original })
	if response, _ := s.do("POST", "/admin/system/backups/remote", nil); response.StatusCode != http.StatusForbidden {
		t.Fatal("missing CSRF created remote backup", response.StatusCode)
	}
	if entries, err := backup.List(s.app.Deps.Config.DataDir); err != nil || len(entries) != 0 {
		t.Fatal("rejected request created local files", err)
	}
	response, body := s.post("/admin/system/backups/remote", nil)
	if response.StatusCode != http.StatusSeeOther || strings.Contains(body, "synthetic-backup-secret") {
		t.Fatal("failure response unavailable or leaked secret", response.StatusCode)
	}
	if dnsCalls.Load() == 0 {
		t.Fatal("configured upload did not reach denied synthetic resolver")
	}
	var state struct {
		Stamp, At, Status, Destination string
		Receipt                        backup.UploadReceipt
	}
	if err := s.app.Deps.Settings.Get(ctx, "storage.backup.last", &state); err != nil {
		t.Fatal(err)
	}
	if state.Status != "failed" || state.Stamp == "" || state.At == "" || state.Destination != dest.Name || state.Receipt.State != "failed" || state.Receipt.ReadBackVerified || len(state.Receipt.Files) != 2 {
		t.Fatalf("unverified upload claimed success: %+v", state)
	}
	if state.Receipt.Files[0].State != "failed" || state.Receipt.Files[0].Code != "provider_upload_failed" || state.Receipt.Files[1].State != "not_attempted" {
		t.Fatalf("pair-level upload failure lost file evidence: %+v", state.Receipt)
	}
	if s.auditCount("system.backup_remote", state.Stamp) != 1 {
		t.Fatal("missing remote failure audit")
	}
	entries, err := backup.List(s.app.Deps.Config.DataDir)
	if err != nil || len(entries) != 1 || entries[0].Stamp != state.Stamp || entries[0].LocalState != "paired_not_inspected" || !entries[0].HasManifest || entries[0].Remote.State != "failed" {
		t.Fatal("failed upload lost or misclassified local pair", err, entries)
	}
	manifestPath := filepath.Join(backup.Dir(s.app.Deps.Config.DataDir), state.Stamp+".manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest backup.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if entries[0].Remote.DatabaseSHA256 != manifest.Database.SHA256 || entries[0].Remote.ArchiveSHA256 != manifest.Archive.SHA256 || manifest.Database.Size <= 0 || manifest.Archive.Size <= 0 {
		t.Fatal("failed receipt lacks local manifest checksum binding")
	}
	report, err := backup.Inspect(manifestPath)
	if err != nil || report.Readiness != "local_ok" {
		t.Fatal("local pair preflight should stay local_ok when only remote is unverified", err, report)
	}
	joined := strings.Join(report.CutoverChecklist, " ")
	if !strings.Contains(joined, "remote_pair_not_confirmed") || !strings.Contains(joined, "remote_readback_not_verified") {
		t.Fatal("failed remote upload omitted from cutover checklist", report.CutoverChecklist)
	}
	response, page := s.do("GET", "/admin/system/backups", nil)
	if response.StatusCode != 200 || !strings.Contains(page, `data-local-state="paired_not_inspected"`) || !strings.Contains(page, `data-remote-state="failed"`) || !strings.Contains(page, dest.Name) || strings.Contains(page, "synthetic-backup-secret") || strings.Contains(page, dest.Secret) {
		t.Fatal("native UI lost completeness distinction or leaked secret", response.StatusCode)
	}
	var saved media.Destination
	if err := s.app.Deps.Settings.Get(ctx, media.DestinationPrefix+dest.ID, &saved); err != nil || saved != dest || s.app.Deps.Settings.GetString(ctx, "storage.active.backup", "") != dest.ID || s.app.Deps.Settings.GetString(ctx, "storage.active.media", "") != "" {
		t.Fatal("failed upload changed destination or crossed storage purpose", err)
	}
}
