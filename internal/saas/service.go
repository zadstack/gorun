// Package saas preserves app ownership, subscriptions and atomic usage independently of commerce provisioning.
package saas

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
)

const PermManage = "saas.manage"

var (
	ErrForbidden = errors.New("saas: forbidden")
	ErrInvalid   = errors.New("saas: invalid input")
	ErrConflict  = errors.New("saas: idempotency conflict")
	ErrInactive  = errors.New("saas: entitlement inactive or expired")
	ErrQuota     = errors.New("saas: quota exceeded or undefined")
)

type App struct {
	OwnerID            string `json:"owner_id"`
	Version            string `json:"version"`
	Mode               string `json:"mode"`
	ID                 string `json:"id"`
	PublicID           string `json:"public_id"`
	Name               string `json:"name"`
	Slug               string `json:"slug"`
	Status             string `json:"status"`
	BaseURL            string `json:"base_url"`
	Runtime            string `json:"runtime"`
	Framework          string `json:"framework"`
	LastSeenAt         string `json:"last_seen_at"`
	FulfillmentEnabled bool   `json:"fulfillment_enabled"`
}
type Entitlement struct {
	Version     string          `json:"version"`
	ID          string          `json:"id"`
	PublicID    string          `json:"public_id"`
	AppID       string          `json:"app_id"`
	AppName     string          `json:"app_name"`
	SubjectType string          `json:"subject_type"`
	ExternalID  string          `json:"external_id"`
	PlanID      string          `json:"plan_id"`
	Status      string          `json:"status"`
	Features    json.RawMessage `json:"features"`
	Limits      json.RawMessage `json:"limits"`
	ValidUntil  string          `json:"valid_until"`
	Renewal     Renewal         `json:"renewal"`
}
type Usage struct {
	AppID     string `json:"app_id"`
	SubjectID string `json:"subject_id"`
	Metric    string `json:"metric"`
	Quantity  int64  `json:"quantity"`
}
type Overview struct {
	Apps         []App         `json:"apps"`
	Entitlements []Entitlement `json:"entitlements"`
	Usage        []Usage       `json:"usage"`
}
type ConsumeRequest struct {
	Metric         string `json:"metric"`
	Quantity       int64  `json:"quantity"`
	IdempotencyKey string `json:"idempotency_key"`
}
type Consumption struct {
	Quantity int64 `json:"quantity"`
	Replayed bool  `json:"replayed"`
}
type Service struct {
	adminTpl *httpx.Templates
	Audit    *audit.Writer
	DB       *sql.DB
	Now      func() time.Time
}

func NewService(d *sql.DB) *Service { return &Service{DB: d, Now: time.Now} }
func actor(ctx context.Context) (string, error) {
	u := auth.UserFromContext(ctx)
	if u == nil || u.Status != "active" || (auth.ActorFromContext(ctx).Kind == "api_key" && !auth.Can(ctx, "saas.use") && !auth.Can(ctx, "saas.read") && !auth.Can(ctx, PermManage)) {
		return "", ErrForbidden
	}
	return u.ID, nil
}
func (s *Service) Overview(ctx context.Context, management bool) (Overview, error) {
	o := Overview{Apps: []App{}, Entitlements: []Entitlement{}, Usage: []Usage{}}
	id, e := actor(ctx)
	if e != nil {
		return o, e
	}
	if management && !auth.Can(ctx, PermManage) {
		return o, ErrForbidden
	}
	admin := 0
	if management {
		admin = 1
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT a.id,a.public_id,a.name,a.slug,a.status,a.base_url,a.runtime,a.framework,a.last_seen_at,a.fulfillment_enabled,COALESCE(a.owner_id,''),a.version,a.mode FROM saas_apps a WHERE ?=1 OR a.owner_id=? OR EXISTS(SELECT 1 FROM saas_entitlements e WHERE e.app_id=a.id AND e.owner_id=?) ORDER BY a.name LIMIT 500`, admin, id, id)
	if e != nil {
		return o, e
	}
	for rows.Next() {
		var a App
		if e = rows.Scan(&a.ID, &a.PublicID, &a.Name, &a.Slug, &a.Status, &a.BaseURL, &a.Runtime, &a.Framework, &a.LastSeenAt, &a.FulfillmentEnabled, &a.OwnerID, &a.Version, &a.Mode); e != nil {
			rows.Close()
			return o, e
		}
		o.Apps = append(o.Apps, a)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return o, e
	}
	observedAt := s.Now().UTC()
	rows, e = s.DB.QueryContext(ctx, `SELECT e.id,e.public_id,e.app_id,a.name,e.subject_type,e.external_id,e.plan_id,e.status,e.features_json,e.limits_json,e.valid_until,e.version FROM saas_entitlements e JOIN saas_apps a ON a.id=e.app_id WHERE ?=1 OR e.owner_id=? ORDER BY e.created_at DESC LIMIT 500`, admin, id)
	if e != nil {
		return o, e
	}
	for rows.Next() {
		var v Entitlement
		var f, l string
		if e = rows.Scan(&v.ID, &v.PublicID, &v.AppID, &v.AppName, &v.SubjectType, &v.ExternalID, &v.PlanID, &v.Status, &f, &l, &v.ValidUntil, &v.Version); e != nil {
			rows.Close()
			return o, e
		}
		v.Features = json.RawMessage(f)
		v.Limits = json.RawMessage(l)
		v.Renewal = renewalMetadata(v.ValidUntil, observedAt)
		o.Entitlements = append(o.Entitlements, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return o, e
	}
	rows, e = s.DB.QueryContext(ctx, `SELECT u.app_id,u.subject_id,u.metric,u.quantity FROM saas_usage_counters u WHERE ?=1 OR EXISTS(SELECT 1 FROM saas_entitlements e WHERE e.app_id=u.app_id AND e.external_id=u.subject_id AND e.owner_id=?) ORDER BY u.metric LIMIT 500`, admin, id)
	if e != nil {
		return o, e
	}
	for rows.Next() {
		var u Usage
		if e = rows.Scan(&u.AppID, &u.SubjectID, &u.Metric, &u.Quantity); e != nil {
			rows.Close()
			return o, e
		}
		o.Usage = append(o.Usage, u)
	}
	e = rows.Err()
	rows.Close()
	return o, e
}

// Consume applies one owned entitlement's usage atomically. Missing quotas deny rather than grant unlimited usage.
func (s *Service) Consume(ctx context.Context, entitlement string, req ConsumeRequest) (Consumption, error) {
	var result Consumption
	owner, e := actor(ctx)
	if e != nil {
		return result, e
	}
	if auth.ActorFromContext(ctx).Kind == "api_key" && !auth.Can(ctx, "saas.use") {
		return result, ErrForbidden
	}
	if entitlement == "" || strings.TrimSpace(req.Metric) != req.Metric || len(req.Metric) < 1 || len(req.Metric) > 100 || req.Quantity < 1 || req.Quantity > 1000000 || len(req.IdempotencyKey) < 16 || len(req.IdempotencyKey) > 200 {
		return result, ErrInvalid
	}
	raw, _ := json.Marshal(struct {
		Metric   string
		Quantity int64
	}{req.Metric, req.Quantity})
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	e = db.InTx(ctx, s.DB, func(tx *sql.Tx) error {
		var app, subject, status, limits, expiry string
		e := tx.QueryRowContext(ctx, `SELECT e.app_id,e.external_id,e.status,e.limits_json,e.valid_until FROM saas_entitlements e JOIN saas_apps a ON a.id=e.app_id WHERE e.id=? AND e.owner_id=? AND a.status='active'`, entitlement, owner).Scan(&app, &subject, &status, &limits, &expiry)
		if errors.Is(e, sql.ErrNoRows) {
			return ErrForbidden
		}
		if e != nil {
			return e
		}
		var oldHash string
		var total int64
		e = tx.QueryRowContext(ctx, `SELECT payload_hash,quantity FROM saas_usage_events WHERE entitlement_id=? AND idempotency_key=?`, entitlement, req.IdempotencyKey).Scan(&oldHash, &total)
		if e == nil {
			if oldHash != hash {
				return ErrConflict
			}
			result = Consumption{Quantity: total, Replayed: true}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if status != "active" && status != "trialing" && status != "grace" {
			return ErrInactive
		}
		if expiry != "" {
			until, e := time.Parse(time.RFC3339, expiry)
			if e != nil {
				until, e = time.Parse("2006-01-02 15:04:05", expiry)
			}
			if e != nil || !until.After(s.Now().UTC()) {
				return ErrInactive
			}
		}
		var quotas map[string]int64
		if json.Unmarshal([]byte(limits), &quotas) != nil {
			return ErrQuota
		}
		limit, ok := quotas[req.Metric]
		if !ok || limit < 0 {
			return ErrQuota
		}
		var current int64
		e = tx.QueryRowContext(ctx, `SELECT quantity FROM saas_usage_counters WHERE app_id=? AND subject_id=? AND metric=?`, app, subject, req.Metric).Scan(&current)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if current > limit || req.Quantity > limit-current {
			return ErrQuota
		}
		total = current + req.Quantity
		now := s.Now().UTC().Format(time.RFC3339)
		_, e = tx.ExecContext(ctx, `INSERT INTO saas_usage_counters(id,app_id,subject_id,metric,quantity,created_at,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(app_id,subject_id,metric) DO UPDATE SET quantity=excluded.quantity,updated_at=excluded.updated_at`, db.NewID(), app, subject, req.Metric, total, now, now)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO saas_usage_events(id,entitlement_id,idempotency_key,payload_hash,quantity,created_at) VALUES(?,?,?,?,?,?)`, db.NewID(), entitlement, req.IdempotencyKey, hash, total, now)
		result.Quantity = total
		if e == nil && s.Audit != nil {
			s.Audit.RecordTx(ctx, tx, "saas.usage.consume", "entitlement", entitlement, map[string]any{"metric": req.Metric, "quantity": req.Quantity, "total": total})
		}
		return e
	})
	return result, e
}

// AcceptEvent records replay protection only. It does not authenticate a legacy external webhook or fulfill subscriptions.
func (s *Service) AcceptEvent(ctx context.Context, app, event, key, hash string) (bool, error) {
	owner, e := actor(ctx)
	if e != nil {
		return false, e
	}
	if event == "" || len(event) > 200 || len(key) < 16 || len(key) > 200 || len(hash) != 64 {
		return false, ErrInvalid
	}
	if _, e = hex.DecodeString(hash); e != nil {
		return false, ErrInvalid
	}
	replay := false
	e = db.InTx(ctx, s.DB, func(tx *sql.Tx) error {
		var id string
		if e := tx.QueryRowContext(ctx, `SELECT id FROM saas_apps WHERE id=? AND owner_id=?`, app, owner).Scan(&id); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return ErrForbidden
			}
			return e
		}
		var oldHash, oldEvent, oldKey string
		e := tx.QueryRowContext(ctx, `SELECT payload_hash,event_id,idempotency_key FROM saas_webhook_ledger WHERE app_id=? AND (event_id=? OR idempotency_key=?)`, app, event, key).Scan(&oldHash, &oldEvent, &oldKey)
		if e == nil {
			if oldHash != hash || oldEvent != event || oldKey != key {
				return ErrConflict
			}
			replay = true
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO saas_webhook_ledger(id,event_id,app_id,idempotency_key,payload_hash,received_at) VALUES(?,?,?,?,?,?)`, db.NewID(), event, app, key, hash, s.Now().UTC().Format(time.RFC3339))
		return e
	})
	return replay, e
}

// StatusUpdate requires the current status to avoid lost concurrent management changes.
type StatusUpdate struct {
	ExpectedStatus string `json:"expected_status"`
	Status         string `json:"status"`
}

func (s *Service) UpdateStatus(ctx context.Context, id string, q StatusUpdate) error {
	if _, e := actor(ctx); e != nil {
		return e
	}
	if !auth.Can(ctx, PermManage) {
		return ErrForbidden
	}
	allowed := map[string]bool{"active": true, "suspended": true, "expired": true, "pending": true, "revoked": true}
	if !allowed[q.Status] || q.ExpectedStatus == "" || id == "" {
		return ErrInvalid
	}
	return db.InTx(ctx, s.DB, func(tx *sql.Tx) error {
		r, e := tx.ExecContext(ctx, `UPDATE saas_entitlements SET status=?,version=hex(randomblob(16)),updated_at=? WHERE id=? AND status=?`, q.Status, s.Now().UTC().Format(time.RFC3339), id, q.ExpectedStatus)
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
			s.Audit.RecordTx(ctx, tx, "saas.entitlement.status", "entitlement", id, map[string]any{"from": q.ExpectedStatus, "to": q.Status})
		}
		return nil
	})
}

// ConsumeNonce is an internal replay guard, not a substitute for authenticating a request signature.
// A legacy key is never accepted until a protocol adapter marks it configured.
func (s *Service) ConsumeNonce(ctx context.Context, app, key, nonce string, expires time.Time) error {
	owner, e := actor(ctx)
	if e != nil {
		return e
	}
	now := s.Now().UTC()
	if len(nonce) < 16 || len(nonce) > 256 || !expires.After(now) || expires.After(now.Add(10*time.Minute)) {
		return ErrInvalid
	}
	return db.InTx(ctx, s.DB, func(tx *sql.Tx) error {
		var status, revoked, protocol, grace string
		e := tx.QueryRowContext(ctx, `SELECT k.status,k.revoked_at,k.protocol,k.grace_expires_at FROM saas_key_versions k JOIN saas_apps a ON a.id=k.app_id WHERE k.public_id=? AND k.app_id=? AND a.owner_id=?`, key, app, owner).Scan(&status, &revoked, &protocol, &grace)
		if errors.Is(e, sql.ErrNoRows) {
			return ErrForbidden
		}
		if e != nil {
			return e
		}
		if revoked != "" || protocol == "legacy-unconfigured" || (status != "active" && status != "grace") {
			return ErrForbidden
		}
		if status == "grace" {
			until, e := time.Parse(time.RFC3339, grace)
			if e != nil || !until.After(now) {
				return ErrForbidden
			}
		}
		var seen int
		e = tx.QueryRowContext(ctx, `SELECT count(*) FROM saas_consumed_nonces WHERE api_key_public_id=? AND nonce=?`, key, nonce).Scan(&seen)
		if e != nil {
			return e
		}
		if seen > 0 {
			return ErrConflict
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO saas_consumed_nonces(id,nonce,api_key_public_id,consumed_at,expires_at) VALUES(?,?,?,?,?)`, db.NewID(), nonce, key, now.Format(time.RFC3339), expires.UTC().Format(time.RFC3339))
		return e
	})
}
