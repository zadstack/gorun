package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMagicLinkSingleUseAndExpiry(t *testing.T) {
	d := newDB(t)
	s := testService(d)
	u := mustUser(t, s, "cust@example.com", "customer")
	deps, ctx := testDeps(d), context.Background()
	token, err := NewMagicLink(ctx, deps, u.ID, time.Hour)
	if err != nil || len(token) < 40 {
		t.Fatalf("%q %v", token, err)
	}
	var n int
	d.QueryRow(`SELECT count(*) FROM magic_links WHERE token_hash = ?`, token).Scan(&n)
	if n != 0 {
		t.Fatal("token must be stored hashed")
	}
	got, err := ConsumeMagicLink(ctx, deps, token)
	if err != nil || got.ID != u.ID {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := ConsumeMagicLink(ctx, deps, token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("second use: %v", err)
	}
	expired, _ := NewMagicLink(ctx, deps, u.ID, -time.Minute)
	if _, err := ConsumeMagicLink(ctx, deps, expired); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired: %v", err)
	}
	if _, err := ConsumeMagicLink(ctx, deps, "nope"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("unknown: %v", err)
	}
}
