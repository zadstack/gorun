package content

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/hooks"
	"github.com/zadstack/gorun/internal/jobs"
	"github.com/zadstack/gorun/internal/settings"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	ctx := context.Background()
	if err := db.Migrate(ctx, d, "audit", audit.Migrations()); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, d, "jobs", jobs.Migrations()); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, d, "settings", settings.Migrations()); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, d, "content", migrations); err != nil {
		t.Fatal(err)
	}
	return d
}

// newTestService returns a service over a fresh database with no taxonomy
// backend (fakeTerms can be attached with svc.termsFn).
func newTestService(t *testing.T) *service {
	t.Helper()
	d := newTestDB(t)
	return newService(d, hooks.New(), audit.New(d, discardLog()), settings.New(d), discardLog())
}

func ptr[T any](v T) *T { return &v }

func mustType(t *testing.T, s *service, typ Type) {
	t.Helper()
	if err := s.SaveType(context.Background(), typ); err != nil {
		t.Fatalf("save type %s: %v", typ.Slug, err)
	}
}

func mustCreate(t *testing.T, s *service, in Input) *Item {
	t.Helper()
	it, err := s.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return it
}
