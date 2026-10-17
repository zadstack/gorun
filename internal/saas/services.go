package saas

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

// ServicesUpdate edits recorded service entitlements only. It does not provision
// mailboxes, register domains, change DNS or record a payment.
type ServicesUpdate struct {
	ExpectedVersion      *string `json:"expected_version"`
	Status               *string `json:"status,omitempty"`
	RenewMonths          int     `json:"renew_months,omitempty"`
	CentralOutboundMail  *bool   `json:"central_outbound_mail,omitempty"`
	ExistingDomainLinked *bool   `json:"existing_domain_linked,omitempty"`
}

// UpdateServices preserves ownership, plan, quota and unrelated feature data.
// A stale version prevents a retried renewal from extending the term twice.
func (s *Service) UpdateServices(ctx context.Context, id string, q ServicesUpdate) (Saved, error) {
	var out Saved
	if _, e := manager(ctx); e != nil {
		return out, e
	}
	if id == "" || q.ExpectedVersion == nil || q.RenewMonths < 0 || q.RenewMonths > 36 || (q.Status != nil && !validStatus(*q.Status)) || (q.Status == nil && q.RenewMonths == 0 && q.CentralOutboundMail == nil && q.ExistingDomainLinked == nil) {
		return out, ErrInvalid
	}
	e := db.InTx(ctx, s.DB, func(tx *sql.Tx) error {
		var status, expiry, raw string
		e := tx.QueryRowContext(ctx, `SELECT status,valid_until,features_json FROM saas_entitlements WHERE id=? AND version=?`, id, *q.ExpectedVersion).Scan(&status, &expiry, &raw)
		if errors.Is(e, sql.ErrNoRows) {
			return ErrConflict
		}
		if e != nil {
			return e
		}
		beforeStatus, beforeExpiry := status, expiry
		features := map[string]json.RawMessage{}
		if json.Unmarshal([]byte(raw), &features) != nil || features == nil {
			return ErrInvalid
		}
		if q.Status != nil {
			status = *q.Status
		}
		if q.CentralOutboundMail != nil {
			features["central_outbound_mail"], _ = json.Marshal(*q.CentralOutboundMail)
		}
		if q.ExistingDomainLinked != nil {
			features["existing_domain_linked"], _ = json.Marshal(*q.ExistingDomainLinked)
		}
		now := s.Now().UTC()
		if q.RenewMonths > 0 {
			base := now
			if expiry != "" {
				previous, err := time.Parse(time.RFC3339, expiry)
				if err != nil {
					return ErrInvalid
				}
				if previous.After(base) {
					base = previous.UTC()
				}
			}
			expiry = addCalendarMonths(base, q.RenewMonths).Format(time.RFC3339)
		}
		encoded, e := json.Marshal(features)
		if e != nil {
			return e
		}
		out = Saved{ID: id, Version: db.NewID()}
		result, e := tx.ExecContext(ctx, `UPDATE saas_entitlements SET status=?,valid_until=?,features_json=?,version=?,updated_at=? WHERE id=? AND version=?`, status, expiry, string(encoded), out.Version, now.Format(time.RFC3339), id, *q.ExpectedVersion)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrConflict
		}
		if s.Audit != nil {
			s.Audit.RecordTx(ctx, tx, "saas.entitlement.services", "entitlement", id, map[string]any{"previous_status": beforeStatus, "status": status, "previous_valid_until": beforeExpiry, "valid_until": expiry, "renew_months": q.RenewMonths, "central_outbound_mail": q.CentralOutboundMail, "existing_domain_linked": q.ExistingDomainLinked})
		}
		return nil
	})
	return out, e
}

func addCalendarMonths(t time.Time, months int) time.Time {
	first := time.Date(t.Year(), t.Month()+time.Month(months), 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
	day := t.Day()
	last := first.AddDate(0, 1, -1).Day()
	if day > last {
		day = last
	}
	return first.AddDate(0, 0, day-1)
}
