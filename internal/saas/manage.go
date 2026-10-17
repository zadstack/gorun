package saas

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,79}$`)

type AppInput struct {
	OwnerID         string `json:"owner_id"`
	Name            string `json:"name"`
	Slug            string `json:"slug"`
	BaseURL         string `json:"base_url"`
	Status          string `json:"status"`
	Mode            string `json:"mode"`
	ExpectedVersion string `json:"expected_version"`
	IdempotencyKey  string `json:"idempotency_key"`
}
type SubscriptionInput struct {
	ExpectedVersion string           `json:"expected_version"`
	AppID           string           `json:"app_id"`
	OwnerID         string           `json:"owner_id"`
	PlanID          string           `json:"plan_id"`
	Status          string           `json:"status"`
	Features        map[string]bool  `json:"features"`
	Limits          map[string]int64 `json:"limits"`
	ValidUntil      string           `json:"valid_until"`
	IdempotencyKey  string           `json:"idempotency_key"`
}
type Saved struct {
	ID       string `json:"id"`
	Version  string `json:"version,omitempty"`
	Replayed bool   `json:"replayed"`
}

func manager(ctx context.Context) (string, error) {
	id, e := actor(ctx)
	if e != nil || !auth.Can(ctx, PermManage) {
		return "", ErrForbidden
	}
	return id, nil
}
func ownerExists(ctx context.Context, tx *sql.Tx, id string) error {
	var n int
	e := tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE id=? AND status='active'`, id).Scan(&n)
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrInvalid
	}
	return nil
}
func validStatus(s string) bool {
	return s == "active" || s == "pending" || s == "suspended" || s == "expired" || s == "revoked"
}
func validateApp(q AppInput) bool {
	if q.OwnerID == "" || strings.TrimSpace(q.Name) == "" || len(q.Name) > 200 || !slugPattern.MatchString(q.Slug) || (q.Status != "active" && q.Status != "disabled") || (q.Mode != "subscription" && q.Mode != "external") {
		return false
	}
	if q.BaseURL != "" {
		u, e := url.Parse(q.BaseURL)
		if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return false
		}
	}
	return true
}
func request(ctx context.Context, tx *sql.Tx, owner, kind, key string, payload any) (Saved, string, error) {
	if len(key) < 16 || len(key) > 200 {
		return Saved{}, "", ErrInvalid
	}
	raw, e := json.Marshal(payload)
	if e != nil {
		return Saved{}, "", ErrInvalid
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	var old, id string
	e = tx.QueryRowContext(ctx, `SELECT payload_hash,result_id FROM saas_configuration_requests WHERE actor_id=? AND kind=? AND request_key=?`, owner, kind, key).Scan(&old, &id)
	if e == nil {
		if old != hash {
			return Saved{}, hash, ErrConflict
		}
		return Saved{ID: id, Replayed: true}, hash, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return Saved{}, hash, e
	}
	return Saved{}, hash, nil
}
func saveRequest(ctx context.Context, tx *sql.Tx, actor, kind, key, hash, id, now string) error {
	_, e := tx.ExecContext(ctx, `INSERT INTO saas_configuration_requests VALUES(?,?,?,?,?,?)`, actor, kind, key, hash, id, now)
	return e
}

// CreateApp establishes explicit ownership and local subscription gating; it never provisions infrastructure.
func (s *Service) CreateApp(ctx context.Context, q AppInput) (Saved, error) {
	var out Saved
	actor, e := manager(ctx)
	if e != nil {
		return out, e
	}
	if q.Mode == "" {
		q.Mode = "subscription"
	}
	if !validateApp(q) {
		return out, ErrInvalid
	}
	e = db.InTx(ctx, s.DB, func(tx *sql.Tx) error {
		var hash string
		var e error
		out, hash, e = request(ctx, tx, actor, "app", q.IdempotencyKey, q)
		if e != nil {
			return e
		}
		if out.Replayed {
			return tx.QueryRowContext(ctx, `SELECT version FROM saas_apps WHERE id=?`, out.ID).Scan(&out.Version)
		}
		if e = ownerExists(ctx, tx, q.OwnerID); e != nil {
			return e
		}
		var n int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM saas_apps WHERE slug=?`, q.Slug).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return ErrConflict
		}
		now := s.Now().UTC().Format(time.RFC3339)
		out = Saved{ID: db.NewID(), Version: db.NewID()}
		_, e = tx.ExecContext(ctx, `INSERT INTO saas_apps(id,public_id,owner_id,name,slug,base_url,status,mode,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, out.ID, out.ID, q.OwnerID, q.Name, q.Slug, q.BaseURL, q.Status, q.Mode, out.Version, now, now)
		if e != nil {
			return e
		}
		if e = saveRequest(ctx, tx, actor, "app", q.IdempotencyKey, hash, out.ID, now); e != nil {
			return e
		}
		if s.Audit != nil {
			s.Audit.RecordTx(ctx, tx, "saas.app.create", "app", out.ID, map[string]any{"owner": q.OwnerID, "mode": q.Mode})
		}
		return nil
	})
	return out, e
}
func (s *Service) UpdateApp(ctx context.Context, id string, q AppInput) (Saved, error) {
	var out Saved
	if _, e := manager(ctx); e != nil {
		return out, e
	}
	if !validateApp(q) {
		return out, ErrInvalid
	}
	e := db.InTx(ctx, s.DB, func(tx *sql.Tx) error {
		if e := ownerExists(ctx, tx, q.OwnerID); e != nil {
			return e
		}
		var count int
		if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM saas_apps WHERE slug=? AND id<>?`, q.Slug, id).Scan(&count); e != nil {
			return e
		}
		if count > 0 {
			return ErrConflict
		}
		out = Saved{ID: id, Version: db.NewID()}
		r, e := tx.ExecContext(ctx, `UPDATE saas_apps SET owner_id=?,name=?,slug=?,base_url=?,status=?,mode=?,version=?,updated_at=? WHERE id=? AND version=?`, q.OwnerID, q.Name, q.Slug, q.BaseURL, q.Status, q.Mode, out.Version, s.Now().UTC().Format(time.RFC3339), id, q.ExpectedVersion)
		if e != nil {
			return e
		}
		n, e := r.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrConflict
		}
		if s.Audit != nil {
			s.Audit.RecordTx(ctx, tx, "saas.app.update", "app", id, map[string]any{"owner": q.OwnerID, "status": q.Status})
		}
		return nil
	})
	return out, e
}
func (s *Service) CreateSubscription(ctx context.Context, q SubscriptionInput) (Saved, error) {
	var out Saved
	actor, e := manager(ctx)
	if e != nil {
		return out, e
	}
	if q.AppID == "" || q.OwnerID == "" || strings.TrimSpace(q.PlanID) == "" || len(q.PlanID) > 100 || !validStatus(q.Status) || len(q.Limits) > 100 || len(q.Features) > 100 {
		return out, ErrInvalid
	}
	for k, v := range q.Limits {
		if len(k) < 1 || len(k) > 100 || v < 0 {
			return out, ErrInvalid
		}
	}
	for k := range q.Features {
		if len(k) < 1 || len(k) > 100 {
			return out, ErrInvalid
		}
	}
	if q.ValidUntil != "" {
		if _, e = time.Parse(time.RFC3339, q.ValidUntil); e != nil {
			return out, ErrInvalid
		}
	}
	features, _ := json.Marshal(q.Features)
	limits, _ := json.Marshal(q.Limits)
	if q.Features == nil {
		features = []byte("{}")
	}
	if q.Limits == nil {
		limits = []byte("{}")
	}
	e = db.InTx(ctx, s.DB, func(tx *sql.Tx) error {
		var hash string
		var e error
		out, hash, e = request(ctx, tx, actor, "subscription", q.IdempotencyKey, q)
		if e != nil || out.Replayed {
			return e
		}
		if e = ownerExists(ctx, tx, q.OwnerID); e != nil {
			return e
		}
		var n int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM saas_apps WHERE id=? AND status='active'`, q.AppID).Scan(&n); e != nil {
			return e
		}
		if n != 1 {
			return ErrInvalid
		}
		var existing int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM saas_entitlements WHERE app_id=? AND owner_id=? AND status IN ('active','pending')`, q.AppID, q.OwnerID).Scan(&existing); e != nil {
			return e
		}
		if existing > 0 {
			return ErrConflict
		}
		now := s.Now().UTC().Format(time.RFC3339)
		out.ID = db.NewID()
		out.Version = db.NewID()
		_, e = tx.ExecContext(ctx, `INSERT INTO saas_entitlements(id,public_id,app_id,owner_id,subject_type,external_id,plan_id,status,features_json,limits_json,valid_until,created_at,updated_at,version) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, out.ID, out.ID, q.AppID, q.OwnerID, "user", q.OwnerID, q.PlanID, q.Status, string(features), string(limits), q.ValidUntil, now, now, out.Version)
		if e != nil {
			return e
		}
		if e = saveRequest(ctx, tx, actor, "subscription", q.IdempotencyKey, hash, out.ID, now); e != nil {
			return e
		}
		if s.Audit != nil {
			s.Audit.RecordTx(ctx, tx, "saas.subscription.create", "entitlement", out.ID, map[string]any{"owner": q.OwnerID, "app": q.AppID, "plan": q.PlanID})
		}
		return nil
	})
	return out, e
}

// DeleteUnused permanently removes only an unused native object. Imported or referenced history must be disabled/revoked instead.
func (s *Service) DeleteUnused(ctx context.Context, kind, id string) error {
	if _, e := manager(ctx); e != nil {
		return e
	}
	if kind != "app" && kind != "subscription" {
		return ErrInvalid
	}
	return db.InTx(ctx, s.DB, func(tx *sql.Tx) error {
		var source sql.NullString
		var e error
		if kind == "app" {
			e = tx.QueryRowContext(ctx, `SELECT source_id FROM saas_apps WHERE id=?`, id).Scan(&source)
		} else {
			e = tx.QueryRowContext(ctx, `SELECT source_id FROM saas_entitlements WHERE id=?`, id).Scan(&source)
		}
		if errors.Is(e, sql.ErrNoRows) {
			return ErrConflict
		}
		if e != nil {
			return e
		}
		if source.Valid {
			return ErrConflict
		}
		var n int
		if kind == "app" {
			e = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM saas_entitlements WHERE app_id=?)+(SELECT count(*) FROM saas_key_versions WHERE app_id=?)+(SELECT count(*) FROM saas_usage_counters WHERE app_id=?)+(SELECT count(*) FROM saas_webhook_ledger WHERE app_id=?)`, id, id, id, id).Scan(&n)
		} else {
			e = tx.QueryRowContext(ctx, `SELECT count(*) FROM saas_usage_events WHERE entitlement_id=?`, id).Scan(&n)
		}
		if e != nil {
			return e
		}
		if n > 0 {
			return ErrConflict
		}
		if kind == "app" {
			_, e = tx.ExecContext(ctx, `DELETE FROM saas_apps WHERE id=?`, id)
		} else {
			_, e = tx.ExecContext(ctx, `DELETE FROM saas_entitlements WHERE id=?`, id)
		}
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `DELETE FROM saas_configuration_requests WHERE result_id=?`, id)
		if e != nil {
			return e
		}
		if s.Audit != nil {
			s.Audit.RecordTx(ctx, tx, "saas."+kind+".delete", kind, id, nil)
		}
		return nil
	})
}

// UpdateSubscription changes explicit native plan limits with a version guard. Existing usage is retained.
func (s *Service) UpdateSubscription(ctx context.Context, id string, q SubscriptionInput) (Saved, error) {
	var out Saved
	if _, e := manager(ctx); e != nil {
		return out, e
	}
	if q.AppID == "" || q.OwnerID == "" || q.PlanID == "" || len(q.PlanID) > 100 || !validStatus(q.Status) || len(q.Features) > 100 || len(q.Limits) > 100 {
		return out, ErrInvalid
	}
	for k, v := range q.Limits {
		if len(k) < 1 || len(k) > 100 || v < 0 {
			return out, ErrInvalid
		}
	}
	for k := range q.Features {
		if len(k) < 1 || len(k) > 100 {
			return out, ErrInvalid
		}
	}
	if q.ValidUntil != "" {
		if _, e := time.Parse(time.RFC3339, q.ValidUntil); e != nil {
			return out, ErrInvalid
		}
	}
	features, _ := json.Marshal(q.Features)
	limits, _ := json.Marshal(q.Limits)
	if q.Features == nil {
		features = []byte("{}")
	}
	if q.Limits == nil {
		limits = []byte("{}")
	}
	e := db.InTx(ctx, s.DB, func(tx *sql.Tx) error {
		// Keep ownership and app fixed; transferring existing subscription quota requires a dedicated audited migration.
		out = Saved{ID: id, Version: db.NewID()}
		r, e := tx.ExecContext(ctx, `UPDATE saas_entitlements SET plan_id=?,status=?,features_json=?,limits_json=?,valid_until=?,version=?,updated_at=? WHERE id=? AND app_id=? AND owner_id=? AND version=?`, q.PlanID, q.Status, string(features), string(limits), q.ValidUntil, out.Version, s.Now().UTC().Format(time.RFC3339), id, q.AppID, q.OwnerID, q.ExpectedVersion)
		if e != nil {
			return e
		}
		n, e := r.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrConflict
		}
		if s.Audit != nil {
			s.Audit.RecordTx(ctx, tx, "saas.subscription.update", "entitlement", id, map[string]any{"plan": q.PlanID, "status": q.Status})
		}
		return nil
	})
	return out, e
}
