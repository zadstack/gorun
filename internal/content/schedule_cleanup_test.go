package content

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

// The daily cleanup deletes preview links that expired or were revoked more
// than 30 days ago (recent ones stay listed for audit), autosaves not
// touched for 30 days, and autosaves whose item was saved since their base
// version once they are a week old; it keeps everything else and logs what
// it removed.
func TestCleanupStaleRows(t *testing.T) {
	s := newTestService(t)
	var logs bytes.Buffer
	s.log = slog.New(slog.NewTextHandler(&logs, nil))
	ctx := context.Background()
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) string { return db.FormatTime(now.Add(-d)) }
	day := 24 * time.Hour
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	item := mustCreate(t, s, Input{Type: "post", Title: "Item"})
	other := mustCreate(t, s, Input{Type: "post", Title: "Other"})

	links := []struct {
		id, expires string
		revoked     any
		keep        bool
	}{
		{"live", db.FormatTime(now.Add(day)), nil, true},
		{"expired-recently", ago(10 * day), nil, true},
		{"expired-long-ago", ago(31 * day), nil, false},
		{"revoked-recently", db.FormatTime(now.Add(day)), ago(29 * day), true},
		{"revoked-long-ago", db.FormatTime(now.Add(day)), ago(31 * day), false},
	}
	for _, l := range links {
		exec(`INSERT INTO content_preview_links (id, content_id, token_hash, created_at, expires_at, revoked_at) VALUES (?, ?, ?, ?, ?, ?)`,
			l.id, item.ID, "hash-"+l.id, ago(60*day), l.expires, l.revoked)
	}

	autosaves := []struct {
		user, content, savedAt, base string
		keep                         bool
	}{
		{"fresh", item.ID, ago(day), item.UpdatedAt, true},
		{"old", item.ID, ago(31 * day), item.UpdatedAt, false},
		{"stale-recent", item.ID, ago(3 * day), "2020-01-01T00:00:00Z", true},
		{"stale-week", other.ID, ago(8 * day), "2020-01-01T00:00:00Z", false},
		{"current-week", other.ID, ago(8 * day), other.UpdatedAt, true},
	}
	for _, a := range autosaves {
		exec(`INSERT OR IGNORE INTO users (id, email, role, created_at, updated_at) VALUES (?, ?, 'editor', ?, ?)`, a.user, a.user+"@example.com", ago(0), ago(0))
		exec(`INSERT INTO content_autosaves (content_id, user_id, payload_json, base_edit_token, base_updated_at, saved_at) VALUES (?, ?, '{}', 'tok', ?, ?)`,
			a.content, a.user, a.base, a.savedAt)
	}

	if err := s.cleanupStale(ctx, now); err != nil {
		t.Fatal(err)
	}
	for _, l := range links {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM content_preview_links WHERE id = ?`, l.id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if (n == 1) != l.keep {
			t.Errorf("preview link %s: present=%v, want %v", l.id, n == 1, l.keep)
		}
	}
	for _, a := range autosaves {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM content_autosaves WHERE user_id = ?`, a.user).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if (n == 1) != a.keep {
			t.Errorf("autosave %s: present=%v, want %v", a.user, n == 1, a.keep)
		}
	}
	for _, want := range []string{"content: cleanup", "preview_links=2", "autosaves=2"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, logs.String())
		}
	}

	// A second run has nothing left to delete and stays quiet.
	logs.Reset()
	if err := s.cleanupStale(ctx, now); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "content: cleanup") {
		t.Errorf("an empty run logged: %s", logs.String())
	}
}
