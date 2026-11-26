package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
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
	Link      string    `json:"link" doc:"One-time admin page for the secret: valid 15 minutes, once, for this service and its provider"`
	ExpiresAt time.Time `json:"expires_at"`
	CLI       string    `json:"cli" doc:"The same from a shell on the site, with the secret on stdin"`
	Pending   bool      `json:"pending,omitempty" doc:"The values were not saved yet: they are held with the link and applied together with the secret"`
	Note      string    `json:"note,omitempty" doc:"Set when the link was redacted (a stored result): how to get a fresh one"`
}

// errUnknownField is the field error of a key the service does not declare.
const errUnknownField = "services.error.unknown_field"

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

// RemoteFields refuses every key of values a caller that never carries a
// secret (MCP, REST) may not send: a key naming the service's secret
// (ErrKeySecretValue), and any other key that is not one of its declared
// Fields (unknown), so a secret under another name (secret_key,
// resend_api_key) is never parked, stored or shown.
func RemoteFields(s Service, values map[string]string) map[string]string {
	fields := SecretValueFields(s, values)
	declared := map[string]bool{}
	for _, f := range s.Fields() {
		declared[f.Name] = true
	}
	for k := range values {
		if fields[k] == "" && !declared[k] {
			fields[k] = errUnknownField
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
// api): a secret given as a value, or any key the service does not
// declare, is refused before anything is written. When the result is
// incomplete for want of the secret it carries a one-time link (absolute
// on the site's base URL when known) made for the calling user and bound
// to the provider, and the CLI line. When the only field error is the
// secret being required (storage on a new bucket), nothing is saved: the
// values are checked as they would be with a secret, held with the link,
// and applied together with the secret when the link is used.
func (r *Registry) SetRemote(ctx context.Context, name string, values map[string]string, via string) (SetResult, map[string]string, error) {
	s, ok := r.by[name]
	if !ok {
		return SetResult{}, nil, ErrUnknownService
	}
	if len(values) == 0 {
		return SetResult{}, map[string]string{"values": errRequired}, nil
	}
	if fields := RemoteFields(s, values); len(fields) > 0 {
		return SetResult{}, fields, nil
	}
	st, fields, err := r.Set(ctx, name, values, nil, via)
	if err != nil {
		return SetResult{}, nil, err
	}
	if len(fields) > 0 {
		return r.pendingSet(ctx, s, values, fields)
	}
	res := SetResult{Status: st}
	if sk := s.Secret(st.Provider); st.State == StateIncomplete && sk != "" && slices.Contains(st.Missing, sk) {
		link, expires, err := r.secretLink(ctx, name, callerID(ctx), st.Provider, nil)
		if err != nil {
			return SetResult{}, nil, err
		}
		res.NeedsSecret = &NeedsSecret{Link: kernel.BaseURL(ctx, r.d) + link, ExpiresAt: expires, CLI: cliLine(name, nil)}
	}
	return res, nil, nil
}

// placeholderSecret stands in for the secret when values are checked
// before a link is made; it has the ACCESS:SECRET shape storage wants.
const placeholderSecret = "placeholder-access:placeholder-secret"

// pendingSet handles a remote set refused with fields: when the only one
// is the service's secret being required, values are checked with a
// placeholder secret in a transaction that is always rolled back, and a
// link holding them is made; any other field error is returned as is.
func (r *Registry) pendingSet(ctx context.Context, s Service, values, fields map[string]string) (SetResult, map[string]string, error) {
	st, err := s.Status(ctx)
	if err != nil {
		return SetResult{}, nil, err
	}
	provider := strings.TrimSpace(values["provider"])
	if provider == "" {
		provider = st.Provider
	}
	sk := s.Secret(provider)
	if sk == "" || len(fields) != 1 || fields[sk] != errRequired {
		return SetResult{}, fields, nil
	}
	placeholder := placeholderSecret
	var checked map[string]string
	err = db.InTx(ctx, r.d.DB, func(tx *sql.Tx) error {
		f, err := s.Apply(ctx, tx, values, &placeholder)
		if err != nil {
			return err
		}
		checked = f
		return errRefused // a check only: nothing it wrote counts
	})
	r.d.Settings.Invalidate()
	if err != nil && !errors.Is(err, errRefused) {
		return SetResult{}, nil, err
	}
	delete(checked, sk) // the placeholder is not what is checked
	if len(checked) > 0 {
		return SetResult{}, checked, nil
	}
	pending := make(map[string]string, len(values))
	for k, v := range values {
		pending[k] = v
	}
	link, expires, err := r.secretLink(ctx, s.Name(), callerID(ctx), provider, pending)
	if err != nil {
		return SetResult{}, nil, err
	}
	return SetResult{Status: st, NeedsSecret: &NeedsSecret{Link: kernel.BaseURL(ctx, r.d) + link, ExpiresAt: expires,
		CLI: cliLine(s.Name(), pending), Pending: true}}, nil, nil
}

// cliLine is the shell line that gives the secret on stdin, with the
// pending values (none of them secret) as single-quoted key=value words.
func cliLine(name string, values map[string]string) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	line := `printf '%s\n' "$SECRET" | gorun service set ` + name
	for _, k := range keys {
		line += " '" + strings.ReplaceAll(k+"="+values[k], "'", `'\''`) + "'"
	}
	return line + " --secret-stdin"
}

// Redacted is res with the one-time link's token cut out, for a result
// that is stored (an approved MCP call): the token is a credential, so
// only the live answer carries it. note says how to get a fresh link.
func (res SetResult) Redacted(note string) SetResult {
	if res.NeedsSecret == nil {
		return res
	}
	ns := *res.NeedsSecret
	if i := strings.Index(ns.Link, "?t="); i >= 0 {
		ns.Link = ns.Link[:i] + "?t=…redacted"
	}
	ns.Note = note
	res.NeedsSecret = &ns
	return res
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
// then each field with its new value (a secret-named or undeclared key is
// marked refused, its value never shown).
func (r *Registry) Describe(ctx context.Context, name string, values map[string]string) (string, error) {
	s, ok := r.by[name]
	if !ok {
		return "", ErrUnknownService
	}
	st, err := s.Status(ctx)
	if err != nil {
		return "", err
	}
	refused := RemoteFields(s, values)
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
		switch refused[k] {
		case "":
		case ErrKeySecretValue:
			fmt.Fprintf(&b, "- %s: refused, a secret is never sent as a value\n", k)
			continue
		default:
			fmt.Fprintf(&b, "- %s: refused, not a field of this service (value not shown)\n", k)
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
