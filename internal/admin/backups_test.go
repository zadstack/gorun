package admin_test

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

var backupLink = regexp.MustCompile(`href="/admin/system/backups/(\d{8}T\d{6}Z)\.db"`)

// System → Backups makes a backup in the data directory, lists it and downloads its files;
// only backup files by name are served, and only to holders of system.backup.
func TestAdminBackups(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	resp, page := s.do("GET", "/admin/system/backups", nil)
	if resp.StatusCode != http.StatusOK || backupLink.MatchString(page) {
		t.Fatalf("empty list: %d", resp.StatusCode)
	}
	mustContain(t, page, `action="/admin/system/backups"`)
	if resp, _ = s.post("/admin/system/backups", nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	_, page = s.do("GET", "/admin/system/backups", nil)
	m := backupLink.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no backup listed:\n%s", page)
	}
	mustContain(t, page, `href="/admin/system/backups/`+m[1]+`.tar.gz"`, `data-local-state="paired_not_inspected"`, `data-remote-state="unverified"`, `href="/admin/system/backups/`+m[1]+`.manifest.json"`, `href="/admin/system/backups/`+m[1]+`/inspect"`)
	resp, body := s.do("GET", "/admin/system/backups/"+m[1]+".db", nil)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(body, "SQLite format 3") ||
		!strings.Contains(resp.Header.Get("Content-Disposition"), m[1]+".db") {
		t.Fatalf("download: %d %q", resp.StatusCode, resp.Header.Get("Content-Disposition"))
	}
	for _, bad := range []string{"/admin/system/backups/gorun.db", "/admin/system/backups/..%2Fgorun.db", "/admin/system/backups/20260101T000000Z.db"} {
		if resp, _ = s.do("GET", bad, nil); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s: %d", bad, resp.StatusCode)
		}
	}

	ed := boot(t)
	ed.login("editor@example.com", "editor", "en")
	if resp, _ = ed.do("GET", "/admin/system/backups", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("editor: %d", resp.StatusCode)
	}
}
