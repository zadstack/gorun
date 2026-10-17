// Package membership provides explicit local grants, never payment assertions.
package membership

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
)

var ErrInvalid = errors.New("membership: invalid input")
var ErrConflict = errors.New("membership: stale version")

const PermManage = "membership.manage"

type Service struct {
	DB    *sql.DB
	Audit *audit.Writer
	Now   func() time.Time
}
type Plan struct {
	ID     string           `json:"id"`
	Name   string           `json:"name"`
	Limits map[string]int64 `json:"limits"`
}
type Grant struct {
	ID                string `json:"id"`
	SourceID          string `json:"source_id,omitempty"`
	OwnerID           string `json:"owner_id"`
	PlanID            string `json:"plan_id"`
	Status            string `json:"status"`
	PeriodStart       string `json:"period_start"`
	PeriodEnd         string `json:"period_end"`
	GraceUntil        string `json:"grace_until"`
	CancelAtPeriodEnd bool   `json:"cancel_at_period_end"`
	Version           string `json:"version"`
	EffectiveStatus   string `json:"effective_status"`
}
type GrantInput struct {
	Grant
	ExpectedVersion string `json:"expected_version"`
}
type Quota struct {
	Type      string `json:"type"`
	Used      int64  `json:"used"`
	Limit     int64  `json:"limit"`
	Unlimited bool   `json:"unlimited"`
}
type Overview struct {
	Grants []Grant `json:"grants"`
	Plans  []Plan  `json:"plans"`
	Quotas []Quota `json:"quotas"`
}

func NewService(d *sql.DB) *Service { return &Service{DB: d, Now: time.Now} }
func (g Grant) Effective(now time.Time) string {
	if g.Status != "active" && g.Status != "pending-renewal" && g.Status != "cancelled" {
		return g.Status
	}
	start, e := parse(g.PeriodStart)
	if e != nil {
		return "invalid"
	}
	if !start.IsZero() && now.Before(start) {
		return "pending"
	}
	end, e := parse(g.PeriodEnd)
	if e != nil {
		return "invalid"
	}
	if end.IsZero() || now.Before(end) {
		return "active"
	}
	grace, e := parse(g.GraceUntil)
	if e != nil {
		return "invalid"
	}
	if !grace.IsZero() && now.Before(grace) {
		return "grace"
	}
	return "expired"
}
func parse(v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, v)
}
func actor(ctx context.Context) string {
	u := auth.UserFromContext(ctx)
	if u == nil || u.Status != "active" {
		return ""
	}

	return u.ID
}
func validGrant(g Grant) bool {
	if g.OwnerID == "" || g.PlanID == "" || ((g.Status == "cancelled" || g.Status == "pending-renewal") && g.PeriodEnd == "") {
		return false
	}
	switch g.Status {
	case "active", "pending-renewal", "expired", "cancelled", "suspended":
	default:
		return false
	}
	start, e := parse(g.PeriodStart)
	if e != nil {
		return false
	}
	end, e := parse(g.PeriodEnd)
	if e != nil {
		return false
	}
	grace, e := parse(g.GraceUntil)
	if e != nil {
		return false
	}
	return (start.IsZero() || end.IsZero() || end.After(start)) && (grace.IsZero() || (!end.IsZero() && !grace.Before(end)))
}
func (s *Service) SavePlan(ctx context.Context, p Plan) error {
	if actor(ctx) == "" || !auth.Can(ctx, PermManage) {
		return content.ErrForbidden
	}
	if p.ID == "" || len(p.ID) > 100 || strings.TrimSpace(p.Name) == "" || len(p.Name) > 200 || len(p.Limits) > 100 {
		return ErrInvalid
	}
	for typ, n := range p.Limits {
		if typ == "" || n < 0 {
			return ErrInvalid
		}
	}
	raw, _ := json.Marshal(p.Limits)
	return db.InTx(ctx, s.DB, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `INSERT INTO membership_plans VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,limits_json=excluded.limits_json`, p.ID, p.Name, string(raw))
		if e == nil && s.Audit != nil {
			s.Audit.RecordTx(ctx, tx, "membership.plan.saved", "membership_plan", p.ID, nil)
		}
		return e
	})
}
func (s *Service) SaveGrant(ctx context.Context, q GrantInput) (Grant, error) {
	g := q.Grant
	if actor(ctx) == "" || !auth.Can(ctx, PermManage) {
		return g, content.ErrForbidden
	}
	if !validGrant(g) || g.SourceID != "" {
		return g, ErrInvalid
	}
	e := db.InTx(ctx, s.DB, func(tx *sql.Tx) error {
		var n int
		if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE id=? AND status='active'`, g.OwnerID).Scan(&n); e != nil {
			return e
		}
		if n != 1 {
			return ErrInvalid
		}
		if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM membership_plans WHERE id=?`, g.PlanID).Scan(&n); e != nil {
			return e
		}
		if n != 1 {
			return ErrInvalid
		}
		now := db.FormatTime(s.Now())
		version := db.NewID()
		if g.ID == "" {
			g.ID = db.NewID()
			_, e := tx.ExecContext(ctx, `INSERT INTO membership_grants(id,owner_id,plan_id,status,period_start,period_end,grace_until,cancel_at_period_end,version,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?,?)`, g.ID, g.OwnerID, g.PlanID, g.Status, g.PeriodStart, g.PeriodEnd, g.GraceUntil, g.CancelAtPeriodEnd, version, now, now)
			if e != nil {
				return e
			}
		} else {
			if q.ExpectedVersion == "" {
				return ErrConflict
			}
			r, e := tx.ExecContext(ctx, `UPDATE membership_grants SET plan_id=?,status=?,period_start=?,period_end=?,grace_until=?,cancel_at_period_end=?,version=?,updated_at=? WHERE id=? AND owner_id=? AND version=?`, g.PlanID, g.Status, g.PeriodStart, g.PeriodEnd, g.GraceUntil, g.CancelAtPeriodEnd, version, now, g.ID, g.OwnerID, q.ExpectedVersion)
			if e != nil {
				return e
			}
			n, _ := r.RowsAffected()
			if n != 1 {
				return ErrConflict
			}
		}
		g.Version = version
		if s.Audit != nil {
			s.Audit.RecordTx(ctx, tx, "membership.grant.saved", "membership_grant", g.ID, map[string]any{"owner": g.OwnerID, "status": g.Status})
		}
		return nil
	})
	g.EffectiveStatus = g.Effective(s.Now())
	return g, e
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func grants(ctx context.Context, q queryer, owner string, all bool) ([]Grant, error) {
	rows, e := q.QueryContext(ctx, `SELECT id,COALESCE(source_id,''),owner_id,plan_id,status,period_start,period_end,grace_until,cancel_at_period_end,version FROM membership_grants WHERE owner_id=? OR ? ORDER BY created_at,id`, owner, all)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Grant{}
	for rows.Next() {
		var g Grant
		if e = rows.Scan(&g.ID, &g.SourceID, &g.OwnerID, &g.PlanID, &g.Status, &g.PeriodStart, &g.PeriodEnd, &g.GraceUntil, &g.CancelAtPeriodEnd, &g.Version); e != nil {
			return nil, e
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
func (s *Service) limit(ctx context.Context, q queryer, owner, typ string, free int64) (int64, error) {
	gs, e := grants(ctx, q, owner, false)
	if e != nil {
		return 0, e
	}
	limit := free
	for _, g := range gs {
		state := g.Effective(s.Now())
		if state != "active" && state != "grace" {
			continue
		}
		var raw string
		if e = q.QueryRowContext(ctx, `SELECT limits_json FROM membership_plans WHERE id=?`, g.PlanID).Scan(&raw); e != nil {
			return 0, e
		}
		var limits map[string]int64
		if e = json.Unmarshal([]byte(raw), &limits); e != nil {
			return 0, e
		}
		if limits[typ] > limit {
			limit = limits[typ]
		}
	}
	return limit, nil
}

// AuthorizeSave runs inside the content write transaction: quota checks and
// content insertion/restoration cannot race or be bypassed by another UI.
func (s *Service) AuthorizeSave(ctx context.Context, v any) (any, error) {
	save, ok := v.(*content.SaveAuthorization)
	if !ok || save == nil {
		return v, nil
	}
	it := save.Item
	if it.Status == "trash" {
		return v, nil
	}
	var free int64
	var bypass string
	e := save.Tx.QueryRowContext(ctx, `SELECT free_limit,bypass_permission FROM membership_policies WHERE content_type=?`, it.Type).Scan(&free, &bypass)
	if errors.Is(e, sql.ErrNoRows) {
		return v, nil
	}
	if e != nil {
		return v, e
	}
	if bypass != "" && auth.Can(ctx, bypass) {
		return v, nil
	}
	// System import without a user does not gain an unlimited policy exemption.
	if actor(ctx) == "" || it.AuthorID == "" {
		return v, content.ErrForbidden
	}
	var occupied int
	if e = save.Tx.QueryRowContext(ctx, `SELECT count(*) FROM content WHERE id=? AND author_id=? AND type=? AND status!='trash'`, it.ID, it.AuthorID, it.Type).Scan(&occupied); e != nil {
		return v, e
	}
	if occupied == 1 {
		return v, nil
	}
	limit, e := s.limit(ctx, save.Tx, it.AuthorID, it.Type, free)
	if e != nil {
		return v, e
	}
	var count int64
	e = save.Tx.QueryRowContext(ctx, `SELECT count(*) FROM content WHERE author_id=? AND type=? AND status!='trash' AND id!=?`, it.AuthorID, it.Type, it.ID).Scan(&count)
	if e != nil {
		return v, e
	}
	if count >= limit {
		return v, &content.ValidationError{Fields: content.FieldErrors{"membership": "membership quota exceeded"}}
	}
	return v, nil
}
func (s *Service) Overview(ctx context.Context, all bool) (Overview, error) {
	o := Overview{Grants: []Grant{}, Plans: []Plan{}, Quotas: []Quota{}}
	id := actor(ctx)
	if id == "" || (all && !auth.Can(ctx, PermManage)) || (auth.ActorFromContext(ctx).Kind == "api_key" && !auth.Can(ctx, "membership.read") && !auth.Can(ctx, PermManage)) {
		return o, content.ErrForbidden
	}
	var e error
	o.Grants, e = grants(ctx, s.DB, id, all)
	if e != nil {
		return o, e
	}
	for i := range o.Grants {
		o.Grants[i].EffectiveStatus = o.Grants[i].Effective(s.Now())
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT id,name,limits_json FROM membership_plans ORDER BY id`)
	if e != nil {
		return o, e
	}
	for rows.Next() {
		var p Plan
		var raw string
		if e = rows.Scan(&p.ID, &p.Name, &raw); e != nil {
			rows.Close()
			return o, e
		}
		if e = json.Unmarshal([]byte(raw), &p.Limits); e != nil {
			rows.Close()
			return o, e
		}
		o.Plans = append(o.Plans, p)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return o, e
	}
	rows, e = s.DB.QueryContext(ctx, `SELECT content_type,free_limit,bypass_permission FROM membership_policies ORDER BY content_type`)
	if e != nil {
		return o, e
	}
	type policy struct {
		typ, perm string
		free      int64
	}
	ps := []policy{}
	for rows.Next() {
		var p policy
		if e = rows.Scan(&p.typ, &p.free, &p.perm); e != nil {
			rows.Close()
			return o, e
		}
		ps = append(ps, p)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return o, e
	}
	for _, p := range ps {
		q := Quota{Type: p.typ, Unlimited: p.perm != "" && auth.Can(ctx, p.perm)}
		q.Limit, e = s.limit(ctx, s.DB, id, p.typ, p.free)
		if e != nil {
			return o, e
		}
		e = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM content WHERE author_id=? AND type=? AND status!='trash'`, id, p.typ).Scan(&q.Used)
		if e != nil {
			return o, e
		}
		o.Quotas = append(o.Quotas, q)
	}
	return o, nil
}
