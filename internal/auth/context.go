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
	// granted are permissions a module lent this principal for one
	// operation it has already authorized itself (WithGrant). The role and
	// the key's scopes are not consulted for them.
	granted map[string]bool
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

// WithGrant returns ctx with perms added to the acting principal, for the
// calls a module makes on that person's behalf after it has authorized the
// operation under its own, narrower rule: the builder publishes a Studio
// editor's text changes through the content service, which asks for
// content.edit and content.publish, once it has checked that the draft
// changes texts only. The audit actor stays the person; a context without
// a principal is returned unchanged.
func WithGrant(ctx context.Context, perms ...string) context.Context {
	p := principalFrom(ctx)
	if p == nil {
		return ctx
	}
	copied := *p
	copied.granted = make(map[string]bool, len(p.granted)+len(perms))
	for k := range p.granted {
		copied.granted[k] = true
	}
	for _, perm := range perms {
		copied.granted[perm] = true
	}
	return withPrincipal(ctx, &copied)
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
