package content

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/jobs"
	"github.com/zadstack/gorun/internal/settings"
)

// migrationsBefore returns the content migrations that sort before name.
func migrationsBefore(t *testing.T, name string) fs.FS {
	t.Helper()
	out := fstest.MapFS{}
	paths, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		if filepath.Base(p) >= name {
			continue
		}
		body, err := fs.ReadFile(migrations, p)
		if err != nil {
			t.Fatal(err)
		}
		out[p] = &fstest.MapFile{Data: body}
	}
	return out
}

// 0012 rebuilds content_autosaves with user_id REFERENCES users ON DELETE
// CASCADE: on a database that already holds autosaves it keeps the rows of
// existing users, drops the orphans of users deleted earlier, keeps the
// index, and from then on deleting a user deletes their unsaved drafts.
func TestAutosaveUserForeignKeyMigration(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	for _, m := range []struct {
		name  string
		files fs.FS
	}{{"audit", audit.Migrations()}, {"jobs", jobs.Migrations()}, {"settings", settings.Migrations()}, {"auth", auth.New().Migrations()},
		{"content", migrationsBefore(t, "0012_autosaves_user_fk.sql")}} {
		if err := db.Migrate(ctx, d, m.name, m.files); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := d.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, id := range []string{"u-keep", "u-later"} {
		exec(`INSERT INTO users (id, email, role, created_at, updated_at) VALUES (?, ?, 'editor', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`, id, id+"@example.com")
	}
	exec(`INSERT INTO content (id, type, slug, title, created_at, updated_at) VALUES ('c1', 'post', 'one', 'One', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	for _, user := range []string{"u-keep", "u-later", "u-gone"} {
		exec(`INSERT INTO content_autosaves (content_id, user_id, payload_json, base_edit_token, saved_at) VALUES ('c1', ?, '{"title":["draft"]}', 'tok', '2026-01-02T00:00:00Z')`, user)
	}

	if err := db.Migrate(ctx, d, "content", migrations); err != nil {
		t.Fatal(err)
	}
	users := func() map[string]bool {
		t.Helper()
		rows, err := d.QueryContext(ctx, `SELECT user_id FROM content_autosaves`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		got := map[string]bool{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			got[id] = true
		}
		return got
	}
	if got := users(); len(got) != 2 || !got["u-keep"] || !got["u-later"] {
		t.Fatalf("after migration: %v (want the two existing users, no orphan)", got)
	}
	var fk, idx int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM pragma_foreign_key_list('content_autosaves') WHERE "table" = 'users' AND "from" = 'user_id' AND on_delete = 'CASCADE'`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("user_id foreign key: %d %v", fk, err)
	}
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'content_autosaves_user'`).Scan(&idx); err != nil || idx != 1 {
		t.Fatalf("index: %d %v", idx, err)
	}
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM pragma_foreign_key_list('content_autosaves') WHERE "table" = 'content' AND on_delete = 'CASCADE'`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("content_id foreign key lost: %d %v", fk, err)
	}

	exec(`DELETE FROM users WHERE id = 'u-later'`)
	if got := users(); len(got) != 1 || !got["u-keep"] {
		t.Fatalf("after deleting a user: %v", got)
	}
	if _, err := d.ExecContext(ctx, `INSERT INTO content_autosaves (content_id, user_id, payload_json, base_edit_token, saved_at) VALUES ('c1', 'nobody', '{}', 'tok', '2026-01-02T00:00:00Z')`); err == nil {
		t.Fatal("an autosave for an unknown user was accepted")
	}
	// Running the migrations again changes nothing.
	if err := db.Migrate(ctx, d, "content", migrations); err != nil {
		t.Fatal(err)
	}
	if got := users(); len(got) != 1 {
		t.Fatalf("second run: %v", got)
	}
}
