package auth

import (
	"context"

	"github.com/zadstack/gorun/internal/audit"
)

// principal is who is acting on a request: a session user or an API key
// (whose owner is user). csrf is the session's CSRF token.
type principal struct {
	kind   string // "user" | "api_key"
	user   *User
	key    *APIKey
	scopes []string
	csrf   string
	token  string // session cookie token, for logout
	reauth string // session reauth_at: last sign-in or password confirmation
}

type principalKey struct{}

func withPrincipal(ctx context.Context, p *principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func principalFrom(ctx context.Context) *principal {
	p, _ := ctx.Value(principalKey{}).(*principal)
	return p
}

// WithUser returns ctx acting as u (as a session user). Used by tests, the
// CLI and jobs that act on behalf of a user.
func WithUser(ctx context.Context, u *User) context.Context {
	return withPrincipal(ctx, &principal{kind: "user", user: u})
}

// UserFromContext returns the acting user (an API key's owner for key
// requests), or nil.
func UserFromContext(ctx context.Context) *User {
	if p := principalFrom(ctx); p != nil {
		return p.user
	}
	return nil
}

// ActorFromContext returns the audit actor for ctx: the user, the API key,
// or audit.SystemActor.
func ActorFromContext(ctx context.Context) audit.Actor {
	p := principalFrom(ctx)
	switch {
	case p == nil || p.user == nil:
		return audit.SystemActor
	case p.kind == "api_key" && p.key != nil:
		return audit.Actor{Kind: "api_key", ID: p.key.ID, Name: p.key.Name}
	default:
		name := p.user.Name
		if name == "" {
			name = p.user.Email
		}
		return audit.Actor{Kind: "user", ID: p.user.ID, Name: name}
	}
}

// auditActor adapts ActorFromContext to the audit.ActorFromContext hook.
func auditActor(ctx context.Context) (audit.Actor, bool) {
	a := ActorFromContext(ctx)
	return a, a.Kind != audit.SystemActor.Kind
}
