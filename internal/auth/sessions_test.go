package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSessionCreateLoadExpire(t *testing.T) {
	d := newDB(t)
	s := testService(d)
	u := mustUser(t, s, "s@example.com", "editor")
	c := newClock()
	st := newSessionStore(d)
	st.now = c.now
	ctx := context.Background()

	token, sess, err := st.create(ctx, u.ID, "1.2.3.4", "test-agent")
	if err != nil || token == "" || sess.CSRF == "" || sess.ID == token {
		t.Fatalf("%q %+v %v", token, sess, err)
	}
	got, user, refreshed, err := st.load(ctx, token)
	if err != nil || user.ID != u.ID || got.CSRF != sess.CSRF || refreshed {
		t.Fatalf("%+v %+v %v %v", got, user, refreshed, err)
	}
	c.add(2 * 24 * time.Hour) // sliding: older than a day → extended
	if _, _, refreshed, err := st.load(ctx, token); err != nil || !refreshed {
		t.Fatalf("want sliding refresh, got %v %v", refreshed, err)
	}
	c.add(13 * 24 * time.Hour) // still within the extended 14 days
	if _, _, _, err := st.load(ctx, token); err != nil {
		t.Fatalf("extended session expired early: %v", err)
	}
	c.add(15 * 24 * time.Hour)
	if _, _, _, err := st.load(ctx, token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want expired, got %v", err)
	}
	if _, _, _, err := st.load(ctx, "garbage"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown token: %v", err)
	}
	token2, _, _ := st.create(ctx, u.ID, "", "")
	if err := st.delete(ctx, token2); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := st.load(ctx, token2); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted session still loads")
	}
}
