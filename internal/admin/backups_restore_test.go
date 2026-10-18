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

	discardPath := "/admin/system/backups/" + stamp + "/discard-partial"
	stale := db.FormatTime(time.Now().Add(-11 * time.Minute))
	if _, err := s.app.Deps.DB.Exec(`UPDATE sessions SET reauth_at = ?`, stale); err != nil {
		t.Fatal(err)
	}
	resp, _ := s.post(discardPath, nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/reauth?next="+url.QueryEscape(inspectPath) {
		t.Fatalf("discard reauth gate: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, _ = s.post("/admin/reauth", url.Values{"password": {testPassword}, "next": {inspectPath}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("reauth: %d", resp.StatusCode)
	}
	if resp, _ = s.post(discardPath, nil); resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != inspectPath {
		t.Fatalf("discard redirect: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Fatal("partial still present after discard")
	}
	if s.auditCount("system.backup_discard_partial", stamp) != 1 {
		t.Fatal("missing discard audit")
	}
	_, page = s.do("GET", inspectPath, nil)
	mustContain(t, page, `data-restore-submit`)
	if strings.Contains(page, `data-restore-partial`) || strings.Contains(page, `data-discard-partial`) {
		t.Fatal("partial UI still shown after discard")
	}
	if resp, _ = s.post("/admin/system/backups/"+stamp+"/restore", nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("restore after discard: %d", resp.StatusCode)
	}
	if s.auditCount("system.backup_restore", stamp) != 1 {
		t.Fatal("restore after discard missing audit")
	}
	ed := boot(t)
	ed.login("editor@example.com", "editor", "en")
	if resp, _ = ed.post(discardPath, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("editor discard: %d", resp.StatusCode)
	}
}

// Every inspection code (errors, warnings, limitations) has an EN and AR
// message, so the inspect page never shows a bare code.
func TestBackupInspectionCodesTranslated(t *testing.T) {
	code := regexp.MustCompile(`(?:Errors|Warnings), "([a-z_]+)"`)
	limits := regexp.MustCompile(`backupLimitations = \[\]string\{([^}]*)\}`)
	quoted := regexp.MustCompile(`"([a-z_]+)"`)
	seen := map[string]bool{}
	for _, f := range []string{"../backup/inspect.go", "../backup/manifest.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range code.FindAllStringSubmatch(string(src), -1) {
			seen[m[1]] = true
		}
		if m := limits.FindStringSubmatch(string(src)); m != nil {
			for _, q := range quoted.FindAllStringSubmatch(m[1], -1) {
				seen[q[1]] = true
			}
		}
	}
	if len(seen) < 20 {
		t.Fatalf("found only %d codes: %v", len(seen), seen)
	}
	for _, loc := range []string{"en", "ar"} {
		raw, err := os.ReadFile("catalog/admin." + loc + ".json")
		if err != nil {
			t.Fatal(err)
		}
		for c := range seen {
			if !strings.Contains(string(raw), `"backups.code.`+c+`":`) {
				t.Errorf("admin.%s.json lacks backups.code.%s", loc, c)
			}
		}
	}
	s := boot(t)
	s.login("admin@example.com", "admin", "ar")
	s.post("/admin/system/backups", nil)
	_, page := s.do("GET", "/admin/system/backups", nil)
	m := inspectLink.FindStringSubmatch(page)
	if m == nil {
		t.Fatal("no inspect link")
	}
	_, page = s.do("GET", "/admin/system/backups/"+m[1]+"/inspect", nil)
	mustContain(t, page, `data-code="remote_pair_not_confirmed"`, "لا توجد نسخة بعيدة مؤكدة لهذا الزوج.")
}
