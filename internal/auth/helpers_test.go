package auth

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := db.Migrate(context.Background(), d, "audit", audit.Migrations()); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(context.Background(), d, "auth", migrations); err != nil {
		t.Fatal(err)
	}
	if err := ReloadRoles(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	return d
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func testDeps(d *sql.DB) *kernel.Deps {
	return &kernel.Deps{DB: d, Config: config.Config{BaseURL: "http://localhost"}, Log: discardLog(), Audit: audit.New(d, discardLog())}
}

// testService is the user service with a real audit writer.
func testService(d *sql.DB) *service { return newService(d, audit.New(d, discardLog())) }

// testPerms declares the base permissions, as Register does.
func testPerms() *kernel.PermissionRegistry {
	p := kernel.NewPermissionRegistry()
	for _, bp := range basePermissions {
		p.Declare(bp.name, map[string]string{"en": bp.en})
	}
	return p
}

func testKeys(d *sql.DB) KeyService { return NewKeyService(d, audit.New(d, discardLog()), testPerms()) }

// auditActions lists recorded audit actions, oldest first.
func auditActions(t *testing.T, d *sql.DB) []string {
	t.Helper()
	rows, err := d.Query(`SELECT action FROM audit_log ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		rows.Scan(&a)
		out = append(out, a)
	}
	return out
}

func mustUser(t *testing.T, s *service, email, role string) *User {
	t.Helper()
	u, err := s.Create(context.Background(), email, "Test", "correct horse battery", role)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func withIP(ip string) context.Context { return audit.WithIP(context.Background(), ip) }

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *clock               { return &clock{t: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)} }
