package admin_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

// Downloading a backup needs a sign-in or password confirmation from the
// last ten minutes; an older session is sent to /admin/reauth and back.
func TestBackupDownloadRequiresRecentAuthentication(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	if resp, _ := s.post("/admin/system/backups", nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	_, page := s.do("GET", "/admin/system/backups", nil)
	m := backupLink.FindStringSubmatch(page)
	if m == nil {
		t.Fatal("no backup listed")
	}
	path := "/admin/system/backups/" + m[1] + ".db"
	if resp, _ := s.do("GET", path, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("fresh sign-in may download: %d", resp.StatusCode)
	}

	stale := db.FormatTime(time.Now().Add(-11 * time.Minute))
	if _, err := s.app.Deps.DB.Exec(`UPDATE sessions SET reauth_at = ?`, stale); err != nil {
		t.Fatal(err)
	}
	resp, body := s.do("GET", path, nil)
	want := "/admin/reauth?next=" + url.QueryEscape(path)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != want || strings.HasPrefix(body, "SQLite") {
		t.Fatalf("stale session: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, _ := s.do("HEAD", path, nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("stale session HEAD: %d", resp.StatusCode)
	}
	resp, page = s.do("GET", want, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reauth form: %d", resp.StatusCode)
	}
	mustContain(t, page, `action="/admin/reauth"`, `name="next" value="`+path+`"`, `autocomplete="current-password"`)

	resp, _ = s.post("/admin/reauth", url.Values{"password": {"wrong password"}, "next": {path}})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", resp.StatusCode)
	}
	if resp, _ := s.do("GET", path, nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("a wrong password must not unlock downloads: %d", resp.StatusCode)
	}
	// An off-site next is replaced by /admin.
	resp, _ = s.post("/admin/reauth", url.Values{"password": {testPassword}, "next": {"https://evil.example/"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin" {
		t.Fatalf("reauth: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if s.auditCount("user.reauth") != 1 {
		t.Fatal("re-authentication not audited")
	}
	resp, body = s.do("GET", path, nil)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(body, "SQLite format 3") {
		t.Fatalf("download after reauth: %d", resp.StatusCode)
	}
}
