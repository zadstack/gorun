package services

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

// ServiceRegistry is the Deps.Services name of the site's *Registry, set by
// the services module for the surfaces booted after it (the MCP toolset).
const ServiceRegistry = "services.registry"

// ErrKeySecretValue is the field error of a secret given as a value.
const ErrKeySecretValue = "services.error.secret_value"

// errNotConfigured is the field error of a test the service cannot run yet.
const errNotConfigured = "services.error.not_configured"

// NeedsSecret is how a caller that never carries a secret (MCP, REST)
// finishes a service: a one-time admin link for a person, or the CLI line.
type NeedsSecret struct {
	Link      string    `json:"link" doc:"One-time admin page for the secret: valid 15 minutes, once, for this service"`
	ExpiresAt time.Time `json:"expires_at"`
	CLI       string    `json:"cli" doc:"The same from a shell on the site, with the secret on stdin"`
}

// SetResult is the answer of a remote set: the new status and, when the
// service still lacks its secret, how to give it.
type SetResult struct {
	Status      Status       `json:"status"`
	NeedsSecret *NeedsSecret `json:"needs_secret,omitempty"`
}

// SecretValueFields refuses every key of values that names the service's
// secret for any of its providers: a secret never comes as a value.
func SecretValueFields(s Service, values map[string]string) map[string]string {
	fields := map[string]string{}
	for _, p := range s.Providers() {
		if sk := s.Secret(p); sk != "" {
			if _, ok := values[sk]; ok {
				fields[sk] = ErrKeySecretValue
			}
		}
	}
	return fields
}

// TakesSecret reports whether any provider of s has a secret.
func TakesSecret(s Service) bool {
	for _, p := range s.Providers() {
		if s.Secret(p) != "" {
			return true
		}
	}
	return false
}

// StringValues turns JSON values (strings, numbers, booleans) into the
// strings Set takes; anything else is a field error.
func StringValues(in map[string]any) (map[string]string, map[string]string) {
	out, bad := make(map[string]string, len(in)), map[string]string{}
	for k, v := range in {
		switch x := v.(type) {
		case string:
			out[k] = x
		case bool:
			out[k] = strconv.FormatBool(x)
		case float64:
			out[k] = strconv.FormatFloat(x, 'f', -1, 64)
		case int:
			out[k] = strconv.Itoa(x)
		default:
			bad[k] = errInvalid
		}
	}
	return out, bad
}

// SetRemote is Set for a caller that never carries a secret (via mcp or
// api): a secret given as a value is refused before anything is written,
// and when the result is incomplete for want of the secret it carries a
// one-time link (absolute on the site's base URL when known) made for the
// calling user, and the CLI line.
func (r *Registry) SetRemote(ctx context.Context, name string, values map[string]string, via string) (SetResult, map[string]string, error) {
	s, ok := r.by[name]
	if !ok {
		return SetResult{}, nil, ErrUnknownService
	}
	if len(values) == 0 {
		return SetResult{}, map[string]string{"values": errRequired}, nil
	}
	if fields := SecretValueFields(s, values); len(fields) > 0 {
		return SetResult{}, fields, nil
	}
	st, fields, err := r.Set(ctx, name, values, nil, via)
	if err != nil || len(fields) > 0 {
		return SetResult{}, fields, err
	}
	res := SetResult{Status: st}
	if sk := s.Secret(st.Provider); st.State == StateIncomplete && sk != "" && slices.Contains(st.Missing, sk) {
		link, expires, err := r.SecretLink(ctx, name, callerID(ctx))
		if err != nil {
			return SetResult{}, nil, err
		}
		res.NeedsSecret = &NeedsSecret{Link: kernel.BaseURL(ctx, r.d) + link, ExpiresAt: expires,
			CLI: `printf '%s\n' "$SECRET" | gorun service set ` + name + ` --secret-stdin`}
	}
	return res, nil, nil
}

// callerID is who asks for a secret link: the signed-in user, or the user
// owning the API key, else the actor itself.
func callerID(ctx context.Context) string {
	if u := auth.UserFromContext(ctx); u != nil {
		return u.ID
	}
	return auth.ActorFromContext(ctx).ID
}

// TestFor runs the service's safe test for a remote caller: mail sends one
// test message to the calling user's own address, never to another one.
func (r *Registry) TestFor(ctx context.Context, name string) (TestResult, error) {
	s, ok := r.by[name]
	if !ok {
		return TestResult{}, ErrUnknownService
	}
	to := ""
	if u := auth.UserFromContext(ctx); u != nil {
		to = u.Email
	}
	return s.Test(ctx, to)
}

// TestError is a test that could not run for a reason the caller can fix
// (no address to send to, mail not configured); Field names the input.
type TestError struct {
	Field, Key string
	Err        error
}

func (e *TestError) Error() string { return e.Err.Error() }
func (e *TestError) Unwrap() error { return e.Err }

// ClassifyTestError wraps the test errors a caller can act on in a
// *TestError; others are returned unchanged.
func ClassifyTestError(err error) error {
	switch {
	case errors.Is(err, ErrMailTestAddress):
		return &TestError{Field: "to", Key: errInvalid, Err: err}
	case errors.Is(err, ErrMailNotConfigured):
		return &TestError{Field: "name", Key: errNotConfigured, Err: err}
	}
	return err
}

// Describe is a plain-text account of what setting values on the service
// would change, for a person deciding on the call: the service's state now,
// then each field with its new value (a secret-named key is marked refused,
// its value never shown).
func (r *Registry) Describe(ctx context.Context, name string, values map[string]string) (string, error) {
	s, ok := r.by[name]
	if !ok {
		return "", ErrUnknownService
	}
	st, err := s.Status(ctx)
	if err != nil {
		return "", err
	}
	refused := SecretValueFields(s, values)
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	now := st.State
	if st.Provider != "" {
		now += ", provider " + st.Provider
	}
	fmt.Fprintf(&b, "Set service %s (now %s):\n", name, now)
	for _, k := range keys {
		if refused[k] != "" {
			fmt.Fprintf(&b, "- %s: refused, a secret is never sent as a value\n", k)
			continue
		}
		fmt.Fprintf(&b, "- %s = %q", k, values[k])
		if old, ok := st.Fields[k]; ok {
			fmt.Fprintf(&b, " (was %v)", old)
		}
		b.WriteString("\n")
	}
	b.WriteString("Other fields and the stored secret stay as they are.")
	return b.String(), nil
}
