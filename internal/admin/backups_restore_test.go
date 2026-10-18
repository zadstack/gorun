package admin_test

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

var inspectLink = regexp.MustCompile(`href="/admin/system/backups/(\d{8}T\d{6}Z)/inspect"`)

func TestAdminBackupInspectAndRestore(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	if resp, _ := s.post("/admin/system/backups", nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	_, page := s.do("GET", "/admin/system/backups", nil)
	m := inspectLink.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no inspect link:\n%s", page)
	}
	stamp := m[1]
	resp, page := s.do("GET", "/admin/system/backups/"+stamp+"/inspect", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("inspect: %d", resp.StatusCode)
	}
	mustContain(t, page, `data-inspect-readiness="review_required"`, `data-restore-submit`, `data-restore-dest`, filepath.Join("restores", stamp))

	inspectPath := "/admin/system/backups/" + stamp + "/inspect"
	restorePath := "/admin/system/backups/" + stamp + "/restore"
	// Restore requires recent auth; after reauth the operator returns to inspect.
	stale := db.FormatTime(time.Now().Add(-11 * time.Minute))
	if _, err := s.app.Deps.DB.Exec(`UPDATE sessions SET reauth_at = ?`, stale); err != nil {
		t.Fatal(err)
	}
	resp, _ = s.post(restorePath, nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/reauth?next="+url.QueryEscape(inspectPath) {
		t.Fatalf("reauth gate: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, _ = s.post("/admin/reauth", url.Values{"password": {testPassword}, "next": {inspectPath}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("reauth: %d", resp.StatusCode)
	}
	resp, _ = s.post(restorePath, nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != inspectPath {
		t.Fatalf("restore redirect: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if s.auditCount("system.backup_restore", stamp) != 1 {
		t.Fatal("missing restore audit")
	}
	dest := filepath.Join(s.app.Deps.Config.DataDir, "restores", stamp)
	if _, err := os.Stat(filepath.Join(dest, "gorun.db")); err != nil {
		t.Fatal("restored database missing", err)
	}
	resp, page = s.do("GET", "/admin/system/backups/"+stamp+"/inspect", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("inspect after restore: %d", resp.StatusCode)
	}
	mustContain(t, page, `data-restore-done`)
	if resp, _ = s.post(restorePath, nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("second restore: %d", resp.StatusCode)
	}
	if s.auditCount("system.backup_restore", stamp) != 1 {
		t.Fatal("second restore must not rewrite")
	}

	ed := boot(t)
	ed.login("editor@example.com", "editor", "en")
	if resp, _ = ed.do("GET", "/admin/system/backups/"+stamp+"/inspect", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("editor inspect: %d", resp.StatusCode)
	}
	if resp, _ = ed.post("/admin/system/backups/"+stamp+"/restore", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("editor restore: %d", resp.StatusCode)
	}
	if resp, _ = s.do("GET", "/admin/system/backups/not-a-stamp/inspect", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bad stamp: %d", resp.StatusCode)
	}
}

func TestAdminBackupRestoreRefusesLeftoverPartial(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	if resp, _ := s.post("/admin/system/backups", nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	_, page := s.do("GET", "/admin/system/backups", nil)
	m := inspectLink.FindStringSubmatch(page)
	if m == nil {
		t.Fatal("no inspect link")
	}
	stamp := m[1]
	restores := filepath.Join(s.app.Deps.Config.DataDir, "restores")
	partial := filepath.Join(restores, "."+stamp+".partial")
	if err := os.MkdirAll(partial, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(partial, "gorun.db"), []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	inspectPath := "/admin/system/backups/" + stamp + "/inspect"
	_, page = s.do("GET", inspectPath, nil)
	mustContain(t, page, `data-restore-partial`, partial)
	if strings.Contains(page, `data-restore-done`) || strings.Contains(page, `data-restore-submit`) {
		t.Fatal("interrupted restore shown as done or restorable")
	}
	if resp, _ := s.post("/admin/system/backups/"+stamp+"/restore", nil); resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != inspectPath {
		t.Fatalf("restore over partial: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if _, err := os.Stat(filepath.Join(restores, stamp)); !os.IsNotExist(err) {
		t.Fatal("restore ran next to a leftover partial")
	}
	if s.auditCount("system.backup_restore", stamp) != 0 {
		t.Fatal("refused restore was audited as done")
	}
	_, page = s.do("GET", "/admin/system/backups", nil)
	if strings.Contains(page, `data-restored-path`) {
		t.Fatal("partial restore listed as restored")
	}
}
