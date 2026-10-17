package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRotateKeepsSettingsAndRetiresOldKey(t *testing.T) {
	s, u := keyConsoleFixture(t)
	ctx := context.Background()
	plain, k, err := s.CreateKind(ctx, KeyKindAPI, "ci", u.ID, []string{"content.view"}, 30, "")
	if err != nil {
		t.Fatal(err)
	}
	next, nk, err := s.Rotate(ctx, k.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if next == plain || nk.RotatedFrom != k.ID || nk.Name != "ci" || nk.RatePerMin != 30 || nk.Scopes[0] != "content.view" || nk.Kind != KeyKindAPI {
		t.Fatalf("rotated key: %+v", nk)
	}
	if _, _, err := s.authenticate(ctx, plain); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("grace 0 must revoke the old key at once")
	}
	if _, _, err := s.authenticate(ctx, next); err != nil {
		t.Fatalf("new key must authenticate: %v", err)
	}
	if _, _, err := s.Rotate(ctx, k.ID, 0); !errors.Is(err, ErrKeyRevoked) {
		t.Fatalf("rotating a revoked key: %v", err)
	}
	g, gk, _ := s.CreateKind(ctx, KeyKindAPI, "grace", u.ID, []string{"content.view"}, 0, "")
	if _, _, err := s.Rotate(ctx, gk.ID, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.authenticate(ctx, g); err != nil {
		t.Fatalf("the old key must keep working during grace: %v", err)
	}
	old, _ := s.Get(ctx, gk.ID)
	if exp, err := time.Parse(time.RFC3339, old.ExpiresAt); err != nil || time.Until(exp) > time.Hour+time.Minute {
		t.Fatalf("old key must expire after the grace: %q", old.ExpiresAt)
	}
	if _, _, err := s.Rotate(ctx, gk.ID, MaxRotateGrace+time.Hour); err == nil {
		t.Fatal("grace beyond the maximum must be rejected")
	}
}

func TestTouchCountsUsesAndFlushesPerMinute(t *testing.T) {
	s, u := keyConsoleFixture(t)
	ctx := context.Background()
	_, k, _ := s.CreateKind(ctx, KeyKindAPI, "n", u.ID, []string{"*"}, 0, "")
	base := time.Now()
	s.now = func() time.Time { return base }
	s.touch(ctx, k) // first use flushes
	k, _ = s.Get(ctx, k.ID)
	for range 3 {
		s.touch(ctx, k) // within the minute: counted, not written
	}
	if got, _ := s.Get(ctx, k.ID); got.UseCount != 1 {
		t.Fatalf("use_count before the next flush = %d", got.UseCount)
	}
	s.now = func() time.Time { return base.Add(2 * time.Minute) }
	s.touch(ctx, k)
	if got, _ := s.Get(ctx, k.ID); got.UseCount != 5 {
		t.Fatalf("use_count after flush = %d, want 5", got.UseCount)
	}
}

func TestCanGrant(t *testing.T) {
	newDB(t) // loads the seeded role permissions
	admin := &User{ID: "u", Role: "admin", Status: StatusActive}
	session := withPrincipal(context.Background(), &principal{kind: "user", user: admin})
	if !CanGrant(session, []string{"*"}) || !CanGrant(session, []string{"content.view"}) {
		t.Fatal("an admin session may grant anything its role allows")
	}
	scoped := withPrincipal(context.Background(), &principal{kind: "api_key", user: admin, scopes: []string{"api_keys.manage", "content.view"}})
	if CanGrant(scoped, []string{"*"}) || CanGrant(scoped, []string{"users.manage"}) || !CanGrant(scoped, []string{"content.view"}) {
		t.Fatal("a key may only grant scopes it holds itself")
	}
	editor := withPrincipal(context.Background(), &principal{kind: "user", user: &User{ID: "e", Role: "editor", Status: StatusActive}})
	if CanGrant(editor, []string{"*"}) || CanGrant(editor, []string{"users.manage"}) || !CanGrant(editor, []string{"content.view"}) {
		t.Fatal("a session may only grant \"*\" when its role holds \"*\"")
	}
	if CanGrant(context.Background(), []string{"content.view"}) {
		t.Fatal("no principal grants nothing")
	}
}

func keyConsoleFixture(t *testing.T) (*keyService, *User) {
	t.Helper()
	d := newDB(t)
	u := mustUser(t, testService(d), "console@example.com", "admin")
	return testKeys(d).(*keyService), u
}
