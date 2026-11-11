package admin_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
)

var notifyKeyRe = regexp.MustCompile(`data-notify-item="([^"]+)"`)

func notifyKeys(page string) []string {
	var out []string
	for _, m := range notifyKeyRe.FindAllStringSubmatch(page, -1) {
		out = append(out, m[1])
	}
	return out
}

func hasKeyPrefix(keys []string, prefix string) bool {
	for _, k := range keys {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

func TestNotificationsFollowPermissions(t *testing.T) {
	s := boot(t, content.New())
	now := db.FormatTime(time.Now())
	author := s.createUser("writer@example.com", "author", "en")
	editor := s.createUser("editor@example.com", "editor", "en")
	dbx := s.app.Deps.DB
	// A pending post by the author, an old dead job and a recent one.
	if _, err := dbx.Exec(`INSERT INTO content (id, type, slug, title, author_id, status, created_at, updated_at) VALUES ('c1', 'post', 'review-me', 'Review me', ?, 'pending', ?, ?)`, author.ID, now, now); err != nil {
		t.Fatal(err)
	}
	old := db.FormatTime(time.Now().Add(-30 * 24 * time.Hour))
	for _, j := range []struct{ id, at string }{{"j-recent", now}, {"j-old", old}} {
		if _, err := dbx.Exec(`INSERT INTO jobs (id, kind, status, run_at, created_at, updated_at) VALUES (?, 'mail.send', 'dead', ?, ?, ?)`, j.id, j.at, j.at, j.at); err != nil {
			t.Fatal(err)
		}
	}

	// The editor may publish others' content but not see system jobs.
	s.signIn(editor.Email)
	_, page := s.do("GET", "/admin/notifications", nil)
	keys := notifyKeys(page)
	if !hasKeyPrefix(keys, "content.review:c1") || hasKeyPrefix(keys, "job.dead:") {
		t.Fatalf("editor notifications: %v", keys)
	}
	_, count := s.do("GET", "/admin/x/notifications/count", nil, "HX-Request", "true")
	if !strings.Contains(count, `data-notify-unread="1"`) {
		t.Fatalf("editor count: %s", count)
	}

	// The author wrote it: nothing to review, and no system items.
	s.signIn(author.Email)
	_, page = s.do("GET", "/admin/notifications", nil)
	if keys := notifyKeys(page); len(keys) != 0 {
		t.Fatalf("author notifications: %v", keys)
	}
	_, count = s.do("GET", "/admin/x/notifications/count", nil, "HX-Request", "true")
	if strings.Contains(count, "notify-count") {
		t.Fatalf("author count: %s", count)
	}

	// The administrator sees both, but not the job older than the window.
	s.login("root@example.com", "admin", "en")
	_, page = s.do("GET", "/admin/notifications", nil)
	keys = notifyKeys(page)
	if !hasKeyPrefix(keys, "content.review:c1") || !hasKeyPrefix(keys, "job.dead:j-recent") || hasKeyPrefix(keys, "job.dead:j-old") {
		t.Fatalf("admin notifications: %v", keys)
	}

	// Marking read is per user; unknown keys are ignored.
	resp, panel := s.post("/admin/notifications/read", url.Values{"key": {"job.dead:j-recent"}}, "HX-Request", "true")
	if resp.StatusCode != 200 || resp.Header.Get("HX-Trigger") != "notify-refresh" || !strings.Contains(panel, "data-notify-target") {
		t.Fatalf("mark read: %d %q", resp.StatusCode, resp.Header.Get("HX-Trigger"))
	}
	s.post("/admin/notifications/read", url.Values{"key": {"made-up:key"}})
	var n int
	dbx.QueryRow(`SELECT count(*) FROM notifications_read WHERE key = 'made-up:key'`).Scan(&n)
	if n != 0 {
		t.Fatal("an unknown key was stored")
	}
	_, count = s.do("GET", "/admin/x/notifications/count", nil, "HX-Request", "true")
	if !strings.Contains(count, `data-notify-unread="1"`) {
		t.Fatalf("admin count after one read: %s", count)
	}
	// Opening an item marks it read and goes where to act.
	resp, _ = s.do("GET", "/admin/notifications/go?key=content.review:c1", nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/content/post/c1" {
		t.Fatalf("open: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	_, count = s.do("GET", "/admin/x/notifications/count", nil, "HX-Request", "true")
	if strings.Contains(count, "notify-count") {
		t.Fatalf("admin count after reading all: %s", count)
	}
	// The editor's read state is separate.
	s.signIn(editor.Email)
	_, count = s.do("GET", "/admin/x/notifications/count", nil, "HX-Request", "true")
	if !strings.Contains(count, `data-notify-unread="1"`) {
		t.Fatalf("editor read state shared: %s", count)
	}
	resp, _ = s.post("/admin/notifications/read-all", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("read all: %d", resp.StatusCode)
	}
	_, count = s.do("GET", "/admin/x/notifications/count", nil, "HX-Request", "true")
	if strings.Contains(count, "notify-count") {
		t.Fatalf("editor count after read all: %s", count)
	}
	// An unknown key cannot be used as an open redirect.
	resp, _ = s.do("GET", "/admin/notifications/go?key=x", nil)
	if resp.Header.Get("Location") != "/admin/notifications" {
		t.Fatalf("unknown key redirect: %s", resp.Header.Get("Location"))
	}
}

func TestUserTwoFactorResetByAdmin(t *testing.T) {
	s := boot(t)
	s.login("boss@example.com", "admin", "en")
	ed := s.createUser("lost-phone@example.com", "editor", "en")
	now := db.Now()
	if _, err := s.app.Deps.DB.Exec(`INSERT INTO user_totp (user_id, secret_enc, enabled_at, created_at, updated_at) VALUES (?, 'x', ?, ?, ?)`, ed.ID, now, now, now); err != nil {
		t.Fatal(err)
	}
	_, page := s.do("GET", "/admin/users/"+ed.ID, nil)
	mustContain(t, page, `action="/admin/users/`+ed.ID+`/two-factor/reset"`)
	resp, _ := s.post("/admin/users/"+ed.ID+"/two-factor/reset", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("reset: %d", resp.StatusCode)
	}
	var n int
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM user_totp WHERE user_id = ?`, ed.ID).Scan(&n)
	if n != 0 || s.auditCount("user.2fa_reset", ed.ID) != 1 {
		t.Fatalf("reset left %d factors, audit %d", n, s.auditCount("user.2fa_reset", ed.ID))
	}
	// Editors without users.manage cannot reset anyone.
	s.login("plain@example.com", "editor", "en")
	resp, _ = s.post("/admin/users/"+ed.ID+"/two-factor/reset", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("editor reset: %d", resp.StatusCode)
	}
}
