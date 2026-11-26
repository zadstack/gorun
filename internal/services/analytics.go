package services

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/analytics"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings"
)

// The provider of the services GoRun runs itself (analytics, backups).
const providerGoRun = "gorun"

// Field error keys shared by the services in this package.
const (
	errInvalid  = "services.error.invalid"
	errRequired = "services.error.required"
)

// Limits of the analytics settings page (internal/analytics/settings.go).
const (
	maxAnalyticsExclude    = 200
	maxAnalyticsExcludeLen = 512
)

type analyticsService struct{ d *kernel.Deps }

// Analytics is GoRun's own analytics over analytics.enabled (JSON bool,
// default on) and analytics.exclude (JSON string, one canonical path prefix
// per line), validated as the analytics settings page does. It has no
// secret and no test.
func Analytics(d *kernel.Deps) Service { return analyticsService{d: d} }

func (analyticsService) Name() string        { return "analytics" }
func (analyticsService) Providers() []string { return []string{providerGoRun} }
func (analyticsService) Fields() []Field {
	return []Field{
		{Name: "enabled", Type: "bool", Required: true},
		{Name: "exclude", Type: "string"},
	}
}
func (analyticsService) Secret(string) string { return "" }

func (s analyticsService) enabled(ctx context.Context) (bool, error) {
	var on bool
	switch err := s.d.Settings.Get(ctx, analytics.SettingEnabled, &on); {
	case err == nil:
		return on, nil
	case errors.Is(err, settings.ErrNotFound):
		return true, nil
	default:
		return false, err
	}
}

func (s analyticsService) Status(ctx context.Context) (Status, error) {
	on, err := s.enabled(ctx)
	if err != nil {
		return Status{}, err
	}
	exclude := []string{}
	for _, line := range strings.Split(s.d.Settings.GetString(ctx, analytics.SettingExclude, ""), "\n") {
		if p := strings.TrimSpace(line); p != "" {
			exclude = append(exclude, p)
		}
	}
	st := Status{Name: "analytics", Provider: providerGoRun, State: StateOff, Missing: []string{}, Source: "settings",
		Fields: map[string]any{"enabled": on, "exclude": exclude}}
	if on {
		st.State = StateConfigured
	}
	return st, nil
}

func (s analyticsService) Apply(ctx context.Context, tx *sql.Tx, values map[string]string, _ *string) (map[string]string, error) {
	errs := map[string]string{}
	var on *bool
	if v, ok := values["enabled"]; ok {
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			errs["enabled"] = errInvalid
		} else {
			on = &b
		}
	}
	var exclude *string
	if v, ok := values["exclude"]; ok {
		e, ok := cleanAnalyticsExclude(v)
		if !ok {
			errs["exclude"] = "analytics.settings.error_exclude"
		} else {
			exclude = &e
		}
	}
	if len(errs) > 0 {
		return errs, nil
	}
	if on != nil {
		if err := s.d.Settings.SetTx(ctx, tx, analytics.SettingEnabled, *on); err != nil {
			return nil, err
		}
	}
	if exclude != nil {
		if err := s.d.Settings.SetTx(ctx, tx, analytics.SettingExclude, *exclude); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// cleanAnalyticsExclude is the analytics page's cleanExclude over a comma
// (or newline) separated list: each prefix starts with one "/", has no tab
// or backslash, and is stored canonical (escaped path), one per line.
func cleanAnalyticsExclude(raw string) (string, bool) {
	var out []string
	for _, p := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\t\\") {
			return "", false
		}
		if d, err := url.PathUnescape(p); err == nil {
			p = (&url.URL{Path: d}).EscapedPath()
		}
		if strings.HasPrefix(p, "//") || len(p) > maxAnalyticsExcludeLen {
			return "", false
		}
		out = append(out, p)
	}
	if len(out) > maxAnalyticsExclude {
		return "", false
	}
	return strings.Join(out, "\n"), true
}

func (analyticsService) Test(context.Context, string) (TestResult, error) {
	return TestResult{OK: true, Message: "no test"}, nil
}

// Off stops tracking and keeps the exclusions and the collected data.
func (s analyticsService) Off(ctx context.Context, tx *sql.Tx) error {
	return s.d.Settings.SetTx(ctx, tx, analytics.SettingEnabled, false)
}
