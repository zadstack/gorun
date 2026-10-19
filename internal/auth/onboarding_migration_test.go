package auth

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/zadstack/gorun/internal/db"
)

func TestOnboardingMigrationOnlyMarksUnconfiguredBootstrap(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	previous := fstest.MapFS{}
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if name == "migrations/0011_onboarding.sql" {
			continue
		}
		b, err := fs.ReadFile(migrations, name)
		if err != nil {
			t.Fatal(err)
		}
		previous[name] = &fstest.MapFile{Data: b}
	}
	ctx := context.Background()
	if err := db.Migrate(ctx, d, "auth", previous); err != nil {
		t.Fatal(err)
	}
	for _, user := range []struct{ id, email, alias string }{{"bootstrap", "admin@localhost.invalid", "admin"}, {"existing", "owner@example.com", ""}} {
		if _, err := d.Exec(`INSERT INTO users (id,email,role,login_name,created_at,updated_at) VALUES (?,?,'admin',NULLIF(?,''),'now','now')`, user.id, user.email, user.alias); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Migrate(ctx, d, "auth", migrations); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id   string
		want bool
	}{{"bootstrap", true}, {"existing", false}} {
		var flag bool
		if err := d.QueryRow(`SELECT onboarding_required FROM users WHERE id = ?`, tc.id).Scan(&flag); err != nil || flag != tc.want {
			t.Fatalf("%s: %v (%v)", tc.id, flag, err)
		}
	}
	if _, err := d.Exec(`UPDATE users SET onboarding_required = 0 WHERE id = 'bootstrap'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, d, "auth", migrations); err != nil {
		t.Fatal(err)
	}
	var flag bool
	if err := d.QueryRow(`SELECT onboarding_required FROM users WHERE id = 'bootstrap'`).Scan(&flag); err != nil || flag {
		t.Fatal("migration rearmed completed onboarding")
	}
}

func TestPendingBootstrapHasNoPermissionsOrDeferredKeyActor(t *testing.T) {
	d := newDB(t)
	ctx := context.Background()
	u, err := BootstrapAdmin(ctx, d, nil, "temporary secure password")
	if err != nil {
		t.Fatal(err)
	}
	if Can(WithUser(ctx, u), "*") || CanGrant(WithUser(ctx, u), []string{"*"}) {
		t.Fatal("pending administrator has grantable permissions")
	}
	keys := testKeys(d)
	_, key, err := keys.Create(ctx, "Pending", u.ID, []string{"*"}, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WithAPIKeyActor(ctx, testDeps(d), key.ID); !errors.Is(err, ErrOnboardingRequired) {
		t.Fatalf("deferred API actor bypassed setup: %v", err)
	}
}
