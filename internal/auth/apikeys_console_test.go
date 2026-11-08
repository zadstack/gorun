package auth

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
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

func TestConcurrentImmediateRotationsIssueOnlyOneReplacement(t *testing.T) {
	s, u := keyConsoleFixture(t)
	_, key, err := s.Create(t.Context(), "rotate once", u.ID, []string{"content.view"}, 30, "")
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	attempts := make(chan error, 8)
	for range cap(attempts) {
		go func() {
			<-start
			_, _, err := s.Rotate(t.Context(), key.ID, 0)
			attempts <- err
		}()
	}
	close(start)
	var issued int
	for range cap(attempts) {
		if err := <-attempts; err == nil {
			issued++
		} else if !errors.Is(err, ErrKeyRevoked) {
			t.Errorf("rotation failed unexpectedly: %v", err)
		}
	}
	var stored int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM api_keys WHERE rotated_from = ?`, key.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if issued != 1 || stored != 1 {
		t.Fatalf("one-time rotation issued=%d stored=%d replacements, want one", issued, stored)
	}
}

func TestRevocationDuringRotationPreparationCannotIssueReplacement(t *testing.T) {
	s, u := keyConsoleFixture(t)
	_, key, err := s.Create(t.Context(), "revoked during rotation", u.ID, []string{"content.view"}, 30, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	prepared, resume := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(resume) }) }
	defer release()
	var clockCalls atomic.Int32
	now := time.Now()
	// Pause the request's creation timestamp at the clock boundary, allowing
	// revocation to commit before the replacement transaction starts.
	s.now = func() time.Time {
		if clockCalls.Add(1) == 1 {
			close(prepared)
			<-resume
		}
		return now
	}
	done := make(chan error, 1)
	go func() { _, _, err := s.Rotate(ctx, key.ID, 0); done <- err }()
	select {
	case <-prepared:
	case <-ctx.Done():
		t.Fatal("rotation did not reach preparation")
	}
	if err := s.Revoke(ctx, key.ID); err != nil {
		t.Fatal(err)
	}
	release()
	if err := <-done; !errors.Is(err, ErrKeyRevoked) {
		t.Fatalf("rotation issued a replacement after revocation: %v", err)
	}
	var replacements int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_keys WHERE rotated_from = ?`, key.ID).Scan(&replacements); err != nil || replacements != 0 {
		t.Fatalf("revoked key has %d replacements: %v", replacements, err)
	}
}
