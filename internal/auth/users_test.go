package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestUserCRUD(t *testing.T) {
	s, ctx := testService(newDB(t)), context.Background()
	u, err := s.Create(ctx, "  Ahmed@Example.COM ", "Ahmed", "correct horse battery", "editor")
	if err != nil || u.Email != "ahmed@example.com" || u.Role != "editor" || u.Status != "active" || len(u.ID) != 26 {
		t.Fatalf("%+v %v", u, err)
	}
	if _, err := s.Create(ctx, "ahmed@example.com", "x", "correct horse battery", "editor"); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("want ErrEmailTaken, got %v", err)
	}
	var ve *ValidationError
	if _, err := s.Create(ctx, "not-an-email", "x", "short", "nope"); !errors.As(err, &ve) || len(ve.Fields) != 3 {
		t.Fatalf("want 3 field errors, got %v", err)
	}
	if _, err := s.Create(ctx, "cust@example.com", "C", "", "customer"); err != nil {
		t.Fatalf("customers may have no password: %v", err)
	}
	if got, err := s.GetByEmail(ctx, "AHMED@example.com"); err != nil || got.ID != u.ID {
		t.Fatalf("%+v %v", got, err)
	}
	name, role, pw := "Ahmed M", "admin", "another long password"
	up, err := s.Update(ctx, u.ID, UserPatch{Name: &name, Role: &role, Password: &pw})
	if err != nil || up.Name != name || up.Role != "admin" {
		t.Fatalf("%+v %v", up, err)
	}
	if _, err := s.CheckPassword(withIP("1.1.1.1"), u.Email, pw); err != nil {
		t.Fatalf("new password rejected: %v", err)
	}
	list, total, err := s.List(ctx, ListQuery{Search: "ahmed"})
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("%v %d %v", list, total, err)
	}
	if n, _ := s.Count(ctx); n != 2 {
		t.Fatalf("count %d", n)
	}
	if err := s.Delete(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestLoginLockoutAfterFiveFailures(t *testing.T) {
	s := testService(newDB(t))
	c := newClock()
	s.now = c.now
	s.limiter.now = c.now
	u := mustUser(t, s, "lock@example.com", "editor")
	ctx := withIP("203.0.113.1")
	for i := 0; i < 5; i++ {
		if _, err := s.CheckPassword(ctx, u.Email, "wrong"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, err := s.CheckPassword(ctx, u.Email, "correct horse battery"); !errors.Is(err, ErrLocked) {
		t.Fatalf("6th attempt must be locked even with the right password, got %v", err)
	}
	if _, err := s.CheckPassword(withIP("198.51.100.7"), u.Email, "correct horse battery"); err != nil {
		t.Fatalf("other IP must not be locked: %v", err)
	}
	c.add(15 * time.Minute)
	if _, err := s.CheckPassword(ctx, u.Email, "correct horse battery"); err != nil {
		t.Fatalf("lock should expire after 15 min: %v", err)
	}
	if _, err := s.CheckPassword(ctx, "nobody@example.com", "x"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown user: %v", err)
	}
}

func TestServiceAuditsUserChanges(t *testing.T) {
	d := newDB(t)
	s, ctx := testService(d), context.Background()
	u := mustUser(t, s, "audit@example.com", "editor")
	loc := "en"
	if _, err := s.Update(ctx, u.ID, UserPatch{Locale: &loc}); err != nil {
		t.Fatal(err)
	}
	s.CheckPassword(withIP("1.2.3.4"), u.Email, "wrong")
	if err := s.Delete(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(auditActions(t, d), ",")
	if got != "user.create,user.update,user.login_failed,user.delete" {
		t.Fatalf("audit trail: %s", got)
	}
	var meta string
	d.QueryRow(`SELECT meta_json FROM audit_log WHERE action = 'user.update'`).Scan(&meta)
	if !strings.Contains(meta, "locale") {
		t.Fatalf("update meta should name changed fields: %s", meta)
	}
}

func TestConcurrentLoginAttemptsRespectLockout(t *testing.T) {
	d := newDB(t)
	s := testService(d)
	u := mustUser(t, s, "race@example.com", "editor")
	var verified atomic.Int32
	s.verify = func(hash, pw string) bool { verified.Add(1); return VerifyPassword(hash, pw) }
	ctx := withIP("203.0.113.50")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.CheckPassword(ctx, u.Email, "wrong password")
		}()
	}
	wg.Wait()
	if n := verified.Load(); n > maxLoginFailures {
		t.Fatalf("%d attempts reached password verification, want <= %d", n, maxLoginFailures)
	}
	var count int
	d.QueryRow(`SELECT count FROM login_attempts`).Scan(&count)
	if count < 20 {
		t.Fatalf("counter %d, want >= 20", count)
	}
	if _, err := s.CheckPassword(ctx, u.Email, "correct horse battery"); !errors.Is(err, ErrLocked) {
		t.Fatalf("want locked, got %v", err)
	}
}

func TestPasswordSprayLimitedAcrossEmails(t *testing.T) {
	s := testService(newDB(t))
	var verified atomic.Int32
	s.verify = func(_, _ string) bool { verified.Add(1); return false }
	ctx := withIP("198.51.100.99")
	for i := range 30 {
		if _, err := s.CheckPassword(ctx, fmt.Sprintf("unknown%d@example.com", i), "wrong"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, err := s.CheckPassword(ctx, "another@example.com", "wrong"); !errors.Is(err, ErrLocked) {
		t.Fatalf("email rotation bypasses limit: %v", err)
	}
	if verified.Load() != 30 {
		t.Fatal("locked request verified password")
	}
	if _, err := s.CheckPassword(withIP("198.51.100.100"), "another@example.com", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("other IP locked: %v", err)
	}
}

func TestConcurrentPasswordVerificationBounded(t *testing.T) {
	s := testService(newDB(t))
	entered := make(chan struct{}, 5)
	release := make(chan struct{})
	s.verify = func(_, _ string) bool { entered <- struct{}{}; <-release; return false }
	var wg sync.WaitGroup
	defer wg.Wait()
	defer close(release)
	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.CheckPassword(withIP(fmt.Sprintf("198.51.100.%d", i)), "unknown@example.com", "wrong")
		}()
	}
	for range 4 {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("verification never entered")
		}
	}
	done := make(chan error, 1)
	go func() { _, err := s.CheckPassword(withIP("198.51.100.200"), "extra@example.com", "wrong"); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrLocked) {
			t.Fatalf("overloaded verifier accepted: %v", err)
		}
	case <-entered:
		t.Fatal("fifth password verification ran concurrently")
	case <-time.After(2 * time.Second):
		t.Fatal("overloaded request blocked")
	}
}
