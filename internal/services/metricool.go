package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/mailer"
	"github.com/zadstack/gorun/internal/settings"
)

// ErrMetricoolNotConfigured is a test asked before the user id, the brand
// id and the token are all set.
var ErrMetricoolNotConfigured = errors.New("services: metricool is not configured")

const providerMetricool = "metricool"

// metricoolService is Settings → Metricool (the metricool.config setting
// the admin form writes): user_id, blog_id (the brand), tracker_hash (the
// public web tracker) and the API token, stored encrypted with the site
// key. transport, when not nil, carries the test's requests (tests only).
type metricoolService struct {
	d         *kernel.Deps
	transport http.RoundTripper
}

// Metricool is the Metricool service: the drafts integration and its web
// tracker. Its secret is the API token; its test reads the brand list.
func Metricool(d *kernel.Deps) Service { return metricoolService{d: d} }

func (metricoolService) Name() string         { return "metricool" }
func (metricoolService) Providers() []string  { return []string{providerMetricool} }
func (metricoolService) Secret(string) string { return "token" }
func (metricoolService) Fields() []Field {
	return []Field{
		{Name: "user_id", Type: "string", Required: true},
		{Name: "blog_id", Type: "string", Required: true},
		{Name: "tracker_hash", Type: "string"},
	}
}

// recordsTests: the test result lives in metricool.config (tested_at,
// test_status), which the admin form clears when the brand changes.
func (metricoolService) recordsTests() {}

func (m metricoolService) load(ctx context.Context) (admin.MetricoolConfig, error) {
	var c admin.MetricoolConfig
	err := m.d.Settings.Get(ctx, admin.MetricoolConfigKey, &c)
	if errors.Is(err, settings.ErrNotFound) {
		err = nil
	}
	return c, err
}

func loadMetricoolTx(ctx context.Context, tx *sql.Tx) (admin.MetricoolConfig, error) {
	var c admin.MetricoolConfig
	var raw string
	switch err := tx.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key=?`, admin.MetricoolConfigKey).Scan(&raw); {
	case errors.Is(err, sql.ErrNoRows):
		return c, nil
	case err != nil:
		return c, err
	}
	return c, json.Unmarshal([]byte(raw), &c)
}

// Status is off while nothing is set, incomplete while the user id, the
// brand id or the token is missing, else configured. drafts_ready says the
// admin's draft composer can send (the token passed its test on the current
// settings); the token itself is never shown. last_test is the stored test.
func (m metricoolService) Status(ctx context.Context) (Status, error) {
	c, err := m.load(ctx)
	if err != nil {
		return Status{}, err
	}
	st := Status{Name: "metricool", Provider: providerMetricool, State: StateOff, Missing: []string{}, Source: "settings",
		SecretSet: c.Secret != "",
		Fields: map[string]any{"user_id": c.UserID, "blog_id": c.BlogID, "tracker_hash": c.TrackerHash,
			"drafts_ready": c.Secret != "" && c.TestStatus == "pass"}}
	if c.TestStatus != "" {
		if at, err := time.Parse(time.RFC3339, c.TestedAt); err == nil {
			res := TestResult{OK: c.TestStatus == "pass", At: at, Message: "brand accessible"}
			if !res.OK {
				res.Message = "brand access not verified"
			}
			st.LastTest = &res
		}
	}
	if c.UserID == "" && c.BlogID == "" && c.Secret == "" && c.TrackerHash == "" {
		return st, nil
	}
	for _, f := range []struct {
		name string
		set  bool
	}{{"user_id", c.UserID != ""}, {"blog_id", c.BlogID != ""}, {"token", c.Secret != ""}} {
		if !f.set {
			st.Missing = append(st.Missing, f.name)
		}
	}
	st.State = StateConfigured
	if len(st.Missing) > 0 {
		st.State = StateIncomplete
	}
	return st, nil
}

// Apply merges values over metricool.config, validated as the admin form
// does (an empty value clears that field), with secret, when not nil, as
// the new token. A change of user, brand or token clears the last test, so
// drafts wait for a new one; every save gives the setting a new version,
// so an admin form opened before it is refused rather than overwriting it.
func (m metricoolService) Apply(ctx context.Context, tx *sql.Tx, values map[string]string, secret *string) (map[string]string, error) {
	errs := map[string]string{}
	c, err := loadMetricoolTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	before := c
	for k, v := range values {
		v = strings.TrimSpace(v)
		switch k {
		case "user_id", "blog_id":
			if v != "" && !admin.ValidMetricoolID(v) {
				errs[k] = errInvalid
			}
			if k == "user_id" {
				c.UserID = v
			} else {
				c.BlogID = v
			}
		case "tracker_hash":
			if v != "" && !admin.ValidMetricoolTrackerHash(v) {
				errs[k] = errInvalid
			}
			c.TrackerHash = v
		default:
			errs[k] = errUnknownField
		}
	}
	if secret != nil {
		if !admin.ValidMetricoolToken(*secret) {
			errs["token"] = errInvalid
		} else if c.Secret, err = mailer.EncryptSecret(m.d.Config.SecretKey, *secret); err != nil {
			return nil, err
		}
	}
	if len(errs) > 0 {
		return errs, nil
	}
	if c.UserID != before.UserID || c.BlogID != before.BlogID || secret != nil {
		c.TestStatus, c.TestedAt = "", ""
	}
	c.Version = db.NewID()
	return nil, m.d.Settings.SetTx(ctx, tx, admin.MetricoolConfigKey, c)
}

// Test is the admin's read-only brand test: the token opens the user's
// brand list and blog_id is in it. The outcome is stored in
// metricool.config as the admin's test stores it (unless the settings
// changed meanwhile). Step is credential (the token is refused or cannot be
// decrypted) or brand (the brand is not the user's, or deleted).
func (m metricoolService) Test(ctx context.Context, _ string) (TestResult, error) {
	c, err := m.load(ctx)
	if err != nil {
		return TestResult{}, err
	}
	if c.UserID == "" || c.BlogID == "" || c.Secret == "" {
		return TestResult{}, ErrMetricoolNotConfigured
	}
	res := TestResult{At: time.Now().UTC().Truncate(time.Second), OK: true, Message: "brand accessible"}
	token, err := mailer.DecryptSecret(m.d.Config.SecretKey, c.Secret)
	if err != nil {
		res.OK, res.Step, res.Message = false, "credential", "the stored token cannot be opened with this site's key"
		return res, nil
	}
	switch err := admin.CheckMetricool(ctx, c, token, m.transport); {
	case err == nil:
	case errors.Is(err, admin.ErrMetricoolRefused):
		res.OK, res.Step, res.Message = false, "credential", "Metricool refused the token"
	case errors.Is(err, admin.ErrMetricoolBrand):
		res.OK, res.Step, res.Message = false, "brand", "brand "+c.BlogID+" is not one of this user's brands"
	default:
		res.OK, res.Step, res.Message = false, "credential", "Metricool's answer could not be verified"
	}
	err = db.InTx(ctx, m.d.DB, func(tx *sql.Tx) error {
		cur, err := loadMetricoolTx(ctx, tx)
		if err != nil || cur.Version != c.Version {
			return err // changed meanwhile: that test is not of the current settings
		}
		cur.TestStatus, cur.TestedAt = "pass", res.At.Format(time.RFC3339)
		if !res.OK {
			cur.TestStatus = "fail"
		}
		cur.Version = db.NewID()
		if err := m.d.Settings.SetTx(ctx, tx, admin.MetricoolConfigKey, cur); err != nil {
			return err
		}
		m.d.Audit.RecordTx(ctx, tx, "metricool.test", "integration", "metricool", map[string]any{"ok": res.OK})
		return nil
	})
	return res, err
}

// Off removes metricool.config, the token and the tracker with it; draft
// records stay.
func (m metricoolService) Off(ctx context.Context, tx *sql.Tx) error {
	return m.d.Settings.DeleteTx(ctx, tx, admin.MetricoolConfigKey)
}
