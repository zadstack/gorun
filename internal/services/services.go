// Package services describes the services a site connects (mail, storage,
// chat, analytics, backups, metricool) in one shape, over the stores each already uses,
// so the CLI, the MCP toolset, the REST API and the admin reach them alike.
// A status never carries a secret's value; a secret arrives only from the
// CLI's stdin or the one-time admin link.
package services

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

// ErrUnknownService is a name no registered service has.
var ErrUnknownService = errors.New("services: unknown service")

// Field is one setting of a service. Type is string, int, bool or enum;
// Providers lists the providers it applies to (empty: all of them).
type Field struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Required  bool     `json:"required,omitempty"`
	Providers []string `json:"providers,omitempty"`
	Values    []string `json:"values,omitempty"` // the choices of an enum
}

// TestResult is how the last safe test went. Step, when the test did not
// pass, names the step that failed (storage: auth, bucket_missing,
// unreachable, write, read or delete; metricool: credential or brand).
type TestResult struct {
	OK      bool      `json:"ok"`
	At      time.Time `json:"at"`
	Message string    `json:"message,omitempty"`
	Step    string    `json:"step,omitempty"`
}

// NoTest is the message of a service with nothing safe to test from the
// server; such a result is never recorded as last_test.
const NoTest = "no test"

// RunResult is the outcome of a service's run action (backups: a backup
// now). Destination is local or the remote destination's id; Remote is the
// upload's status when there was one, Objects its object keys.
type RunResult struct {
	OK          bool      `json:"ok"`
	At          time.Time `json:"at"`
	Message     string    `json:"message"`
	Stamp       string    `json:"stamp,omitempty"`
	Bytes       int64     `json:"bytes"`
	DurationMS  int64     `json:"duration_ms"`
	Destination string    `json:"destination"`
	Remote      string    `json:"remote,omitempty"`
	Objects     []string  `json:"objects,omitempty"`
	Pruned      int       `json:"pruned"`
	Warning     string    `json:"warning,omitempty"`
}

// Runner is a service with an action it can take now (backups).
type Runner interface {
	Run(ctx context.Context, via string) (RunResult, error)
}

// ErrNoRun is a run asked of a service that has no run action.
var ErrNoRun = errors.New("services: this service has no run action")

// errNoRunKey is the field error of ErrNoRun.
const errNoRunKey = "services.error.no_run"

// ErrNoRunKey is the field error of a run asked of a service without one.
const ErrNoRunKey = errNoRunKey

// settingLastTestPrefix + a service name holds its last recorded test.
const settingLastTestPrefix = "services.last_test."

// selfRecorder is a service that keeps its own last test (in its status),
// so the registry neither records nor fills one for it.
type selfRecorder interface{ recordsTests() }

// Status is a service as it stands, without any secret: State is off,
// configured or incomplete; Missing names what keeps it incomplete; Source
// says where the setting comes from (settings, config or local).
type Status struct {
	Name      string         `json:"name"`
	Provider  string         `json:"provider"`
	State     string         `json:"state"`
	Missing   []string       `json:"missing"`
	Fields    map[string]any `json:"fields"`
	SecretSet bool           `json:"secret_set"`
	Source    string         `json:"source"`
	LastTest  *TestResult    `json:"last_test"`
}

// The states a Status reports.
const (
	StateOff        = "off"
	StateConfigured = "configured"
	StateIncomplete = "incomplete"
)

// Service is one connectable service. Apply validates values merged over
// what is stored (fields not given are kept) and writes them in tx; it
// returns field errors (field → i18n key) and writes nothing that counts
// when it does, since the registry rolls the transaction back. A nil secret
// keeps the stored one.
type Service interface {
	Name() string
	Providers() []string
	Fields() []Field
	Secret(provider string) string
	Status(ctx context.Context) (Status, error)
	Apply(ctx context.Context, tx *sql.Tx, values map[string]string, secret *string) (map[string]string, error)
	Test(ctx context.Context, actor string) (TestResult, error)
	Off(ctx context.Context, tx *sql.Tx) error
}

// Registry is the site's services, in the order they were given.
type Registry struct {
	d    *kernel.Deps
	list []Service
	by   map[string]Service
	now  func() time.Time // the clock of secret links; tests set it with setNow
}

// NewRegistry registers svcs over d.
func NewRegistry(d *kernel.Deps, svcs ...Service) *Registry {
	r := &Registry{d: d, list: svcs, by: map[string]Service{}, now: time.Now}
	for _, s := range svcs {
		r.by[s.Name()] = s
	}
	return r
}

// Get is the service named name.
func (r *Registry) Get(name string) (Service, bool) {
	s, ok := r.by[name]
	return s, ok
}

// List is every service's status.
func (r *Registry) List(ctx context.Context) ([]Status, error) {
	out := make([]Status, 0, len(r.list))
	for _, s := range r.list {
		st, err := r.status(ctx, s)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

// Status is the status of the service named name, with its last recorded
// test.
func (r *Registry) Status(ctx context.Context, name string) (Status, error) {
	s, ok := r.by[name]
	if !ok {
		return Status{}, ErrUnknownService
	}
	return r.status(ctx, s)
}

// status is s.Status with last_test filled from the registry's record when
// the service does not keep its own.
func (r *Registry) status(ctx context.Context, s Service) (Status, error) {
	st, err := s.Status(ctx)
	if _, self := s.(selfRecorder); err != nil || self || st.LastTest != nil {
		return st, err
	}
	var last TestResult
	if r.d.Settings.Get(ctx, settingLastTestPrefix+s.Name(), &last) == nil && !last.At.IsZero() {
		st.LastTest = &last
	}
	return st, nil
}

// Test runs the service's safe test with actor (mail: the address to send
// to) and records the result as its last_test, unless the service has no
// test (NoTest) or the test could not run (an error).
func (r *Registry) Test(ctx context.Context, name, actor string) (TestResult, error) {
	s, ok := r.by[name]
	if !ok {
		return TestResult{}, ErrUnknownService
	}
	res, err := s.Test(ctx, actor)
	if _, self := s.(selfRecorder); err != nil || self || res.Message == NoTest {
		return res, err
	}
	if res.At.IsZero() {
		res.At = r.now().UTC()
	}
	if err := r.d.Settings.Set(ctx, settingLastTestPrefix+name, res); err != nil {
		return res, err
	}
	return res, nil
}

// Run takes the service's run action now and audits it as service.run with
// via and whether it passed. A service without one is ErrNoRun.
func (r *Registry) Run(ctx context.Context, name, via string) (RunResult, error) {
	s, ok := r.by[name]
	if !ok {
		return RunResult{}, ErrUnknownService
	}
	rn, ok := s.(Runner)
	if !ok {
		return RunResult{}, ErrNoRun
	}
	res, err := rn.Run(ctx, via)
	if err != nil {
		return res, err
	}
	r.d.Audit.Record(ctx, "service.run", "service", name, map[string]any{"service": name, "via": via, "ok": res.OK})
	return res, nil
}

// errRefused rolls a transaction back when Apply reports field errors.
var errRefused = errors.New("services: refused")

// Set applies values (and the secret, when not nil) to the service in one
// transaction, audits the change by field names only, and returns its new
// status. Field errors leave everything as it was.
func (r *Registry) Set(ctx context.Context, name string, values map[string]string, secret *string, via string) (Status, map[string]string, error) {
	return r.set(ctx, name, values, secret, via, nil, nil)
}

// set is Set with before, when not nil, run first in the same transaction
// (an error from it rolls everything back), and extra, when not nil, adding
// to the audit metadata after the commit.
func (r *Registry) set(ctx context.Context, name string, values map[string]string, secret *string, via string,
	before func(tx *sql.Tx) error, extra func() map[string]any) (Status, map[string]string, error) {
	s, ok := r.by[name]
	if !ok {
		return Status{}, nil, ErrUnknownService
	}
	var fields map[string]string
	err := db.InTx(ctx, r.d.DB, func(tx *sql.Tx) error {
		if before != nil {
			if err := before(tx); err != nil {
				return err
			}
		}
		f, err := s.Apply(ctx, tx, values, secret)
		if err != nil {
			return err
		}
		if len(f) > 0 {
			fields = f
			return errRefused
		}
		// A test of the values before this change says nothing of the new ones.
		return r.d.Settings.DeleteTx(ctx, tx, settingLastTestPrefix+name)
	})
	if errors.Is(err, errRefused) {
		r.d.Settings.Invalidate()
		return Status{}, fields, nil
	}
	if err != nil {
		r.d.Settings.Invalidate()
		return Status{}, nil, err
	}
	keys := make([]string, 0, len(values)+1)
	for k := range values {
		keys = append(keys, k)
	}
	if secret != nil {
		if sk := s.Secret(values["provider"]); sk != "" {
			keys = append(keys, sk)
		}
	}
	sort.Strings(keys)
	meta := map[string]any{"service": name, "keys": keys, "via": via}
	if extra != nil {
		for k, v := range extra() {
			meta[k] = v
		}
	}
	r.d.Audit.Record(ctx, "service.update", "service", name, meta)
	st, err := r.status(ctx, s)
	return st, nil, err
}

// Off turns the service off and audits it.
func (r *Registry) Off(ctx context.Context, name, via string) error {
	s, ok := r.by[name]
	if !ok {
		return ErrUnknownService
	}
	if err := db.InTx(ctx, r.d.DB, func(tx *sql.Tx) error {
		if err := s.Off(ctx, tx); err != nil {
			return err
		}
		return r.d.Settings.DeleteTx(ctx, tx, settingLastTestPrefix+name)
	}); err != nil {
		return err
	}
	r.d.Audit.Record(ctx, "service.update", "service", name, map[string]any{"service": name, "off": true, "via": via})
	return nil
}
