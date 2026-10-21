package saas

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/zadstack/gorun/internal/commerce"
	"github.com/zadstack/gorun/internal/db"
)

// subscriptionPayload accepts the struct Emit was given and the JSON the
// hooks job delivers to listeners.
func subscriptionPayload(payload any) (commerce.SubscriptionEvent, bool) {
	switch p := payload.(type) {
	case commerce.SubscriptionEvent:
		return p, true
	case json.RawMessage:
		return decodeSubscription(p)
	case []byte:
		return decodeSubscription(p)
	}
	return commerce.SubscriptionEvent{}, false
}

func decodeSubscription(raw []byte) (commerce.SubscriptionEvent, bool) {
	var ev commerce.SubscriptionEvent
	if json.Unmarshal(raw, &ev) == nil && (ev.AccountID != "" || ev.SiteID != "" || ev.SubscriptionID != "") {
		return ev, true
	}
	return commerce.SubscriptionEvent{}, false
}

// applySubscription moves owned SaaS entitlements when commerce emits a
// billing event. active and trialing stay usable; past_due is grace;
// canceled and unpaid suspend.
func applySubscription(ctx context.Context, database *sql.DB, ev commerce.SubscriptionEvent) error {
	status := "suspended"
	until := ev.CurrentPeriodEnd
	switch ev.Status {
	case "active", "trialing":
		status = "active"
	case "past_due":
		status = "grace"
		if ev.GraceUntil != "" {
			until = ev.GraceUntil
		}
	}
	if ev.Reason == "grace_expired" {
		status = "suspended"
	}
	if ev.AccountID == "" && ev.SiteID == "" {
		return nil
	}
	now := db.FormatTime(time.Now().UTC())
	_, err := database.ExecContext(ctx, `UPDATE saas_entitlements SET status = ?, valid_until = ?, updated_at = ?
		WHERE (? <> '' AND (owner_id = ? OR external_id = ?)) OR (? <> '' AND external_id = ?)`,
		status, until, now, ev.AccountID, ev.AccountID, ev.AccountID, ev.SiteID, ev.SiteID)
	return err
}
