package auth

import (
	"context"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

func TestRecentlyAuthenticated(t *testing.T) {
	base := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	reauthNow = func() time.Time { return base }
	t.Cleanup(func() { reauthNow = time.Now })
	session := func(at time.Time) context.Context {
		return withPrincipal(context.Background(), &principal{kind: "user", user: &User{ID: "u"}, token: "t", reauth: db.FormatTime(at)})
	}
	for name, c := range map[string]struct {
		ctx  context.Context
		want bool
	}{
		"just signed in":        {session(base), true},
		"nine minutes ago":      {session(base.Add(-9 * time.Minute)), true},
		"eleven minutes ago":    {session(base.Add(-11 * time.Minute)), false},
		"from the future":       {session(base.Add(time.Hour)), false},
		"pre-migration session": {withPrincipal(context.Background(), &principal{kind: "user", user: &User{ID: "u"}, token: "t"}), false},
		"no session (CLI, job)": {WithUser(context.Background(), &User{ID: "u"}), false},
		"api key":               {withPrincipal(context.Background(), &principal{kind: "api_key", user: &User{ID: "u"}, reauth: db.FormatTime(base)}), false},
		"anonymous":             {context.Background(), false},
	} {
		if got := RecentlyAuthenticated(c.ctx, ReauthWindow); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}
