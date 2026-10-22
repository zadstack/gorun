package db

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDestructiveStatementsFixtures(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want []string
	}{
		{"create table", "CREATE TABLE t(x TEXT);", nil},
		{"add column", "ALTER TABLE t ADD COLUMN y TEXT;", nil},
		{"create index", "CREATE INDEX IF NOT EXISTS t_x ON t(x);", nil},
		{"drop table", "DROP TABLE t;", []string{RuleDropTable}},
		{"drop table if exists lowercase", "drop table if exists t;", []string{RuleDropTable}},
		{"drop table split across lines", "DROP\n   TABLE\tt;", []string{RuleDropTable}},
		{"drop column", "ALTER TABLE t DROP COLUMN x;", []string{RuleDropColumn}},
		{"drop column without keyword", "alter table t drop x;", []string{RuleDropColumn}},
		{"rename table", "ALTER TABLE t RENAME TO u;", []string{RuleRename}},
		{"rename column", "ALTER TABLE t RENAME COLUMN a TO b;", []string{RuleRename}},
		{"rename bare column", "ALTER TABLE \"t\" RENAME a TO b;", []string{RuleRename}},
		{"drop index", "DROP INDEX IF EXISTS t_x;", []string{RuleDropIndex}},
		{"rebuild", "CREATE TABLE t_new(x);\nINSERT INTO t_new SELECT x FROM t;\nDROP TABLE t;\nALTER TABLE t_new RENAME TO t;",
			[]string{RuleDropTable, RuleRename}},
		{"line comment mentions drop", "-- we never DROP TABLE here\nCREATE TABLE t(x);", nil},
		{"block comment mentions rename", "/* ALTER TABLE t RENAME TO u */ CREATE TABLE t(x);", nil},
		{"apostrophe in comment does not open a string", "-- don't worry\nDROP TABLE t; -- it's fine", []string{RuleDropTable}},
		{"string literal mentions drop", "INSERT INTO notes VALUES ('DROP TABLE users');", nil},
		{"double dash inside string", "INSERT INTO t VALUES ('--'); DROP TABLE t;", []string{RuleDropTable}},
		{"escaped quote in string", "INSERT INTO t VALUES ('it''s DROP TABLE x');", nil},
		{"trigger using NEW and _new names", "CREATE TRIGGER sites_new_origin_unique BEFORE INSERT ON sites WHEN NEW.url!='' BEGIN SELECT RAISE(ABORT,'x'); END;", nil},
		{"column named drop_at", "CREATE TABLE t(dropped_at TEXT, renamed INTEGER);", nil},
		{"unterminated block comment", "CREATE TABLE t(x); /* DROP TABLE t", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DestructiveStatements(c.sql)
			if !slices.Equal(got, c.want) {
				t.Fatalf("DestructiveStatements(%q) = %v, want %v", c.sql, got, c.want)
			}
		})
	}
}

func TestStripSQLRemovesCommentsAndStringContents(t *testing.T) {
	got := strings.Join(strings.Fields(StripSQL("a -- c\nb /* x\ny */ 'q''s' d")), " ")
	if got != "a b '' d" {
		t.Fatalf("StripSQL = %q, want %q", got, "a b '' d")
	}
}

// migrationAllowlistFile lists migration files allowed to contain
// destructive statements, one "path # justification" per line.
const migrationAllowlistFile = "migration_allowlist.txt"

// TestMigrationsAreAdditive walks every migrations/NNNN_*.sql file in the
// repository and fails when one contains a destructive statement and is not
// in the allowlist. Allowlist entries must carry a justification and must
// still match a destructive file (no stale entries).
func TestMigrationsAreAdditive(t *testing.T) {
	root := repoRoot(t)
	allow := readAllowlist(t)
	used := map[string]bool{}
	var checked int
	err := filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			switch e.Name() {
			case ".git", ".claude", "e2e", "node_modules":
				// .claude holds other sessions' worktrees: their branches are
				// linted when they are checked out, not from this tree.
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Base(filepath.Dir(p)) != "migrations" || !migrationName.MatchString(e.Name()) {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		checked++
		rules := DestructiveStatements(string(body))
		if len(rules) == 0 {
			return nil
		}
		if _, ok := allow[rel]; ok {
			used[rel] = true
			return nil
		}
		t.Errorf("%s contains destructive statements %v: migrations must be additive (see docs/ARCHITECTURE.md, \"Migrations must be additive\"), or add it to internal/db/%s with a justification", rel, rules, migrationAllowlistFile)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 50 {
		t.Fatalf("only %d migration files found under %s; the walk is broken", checked, root)
	}
	for p := range allow {
		if !used[p] {
			t.Errorf("stale allowlist entry %s: the file is missing or no longer destructive; remove it from %s", p, migrationAllowlistFile)
		}
	}
}

func readAllowlist(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open(migrationAllowlistFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		path, why, ok := strings.Cut(line, "#")
		path, why = strings.TrimSpace(path), strings.TrimSpace(why)
		if !ok || path == "" || why == "" {
			t.Fatalf("%s:%d: want \"path # justification\", got %q", migrationAllowlistFile, n, line)
		}
		if _, dup := out[path]; dup {
			t.Fatalf("%s:%d: duplicate entry %s", migrationAllowlistFile, n, path)
		}
		out[path] = why
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		} else if !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
