package saas

import "time"

const renewalReminderDays = 7
const secondsPerDay = int64(24 * 60 * 60)

// Renewal describes expiry only; entitlement status still controls access.
// Missing/invalid dates leave remaining values null, never implying unlimited access.
type Renewal struct {
	State            string `json:"state" enum:"unscheduled,invalid,expired,due_soon,current" doc:"Expiry state only; does not override subscription status."`
	RemainingSeconds *int64 `json:"remaining_seconds" doc:"Positive remaining seconds rounded up; zero when expired, null for missing or invalid expiry."`
	RemainingDays    *int   `json:"remaining_days" doc:"Remaining 24-hour days rounded up; zero when expired, null for missing or invalid expiry."`
	ReminderDays     int    `json:"reminder_days" doc:"Reminder window in days (7). No notification is sent."`
	ObservedAt       string `json:"observed_at" doc:"UTC RFC3339 observation time shared by this overview."`
}

func renewalMetadata(validUntil string, now time.Time) Renewal {
	now = now.UTC()
	r := Renewal{State: "unscheduled", ReminderDays: renewalReminderDays, ObservedAt: now.Format(time.RFC3339Nano)}
	if validUntil == "" {
		return r
	}
	expiry, err := time.Parse(time.RFC3339Nano, validUntil)
	if err != nil {
		r.State = "invalid"
		return r
	}
	seconds, days := int64(0), 0
	r.RemainingSeconds, r.RemainingDays = &seconds, &days
	if !expiry.After(now) {
		r.State = "expired"
		return r
	}
	// Unix differences avoid time.Duration saturation for far-future valid dates.
	seconds = expiry.Unix() - now.Unix()
	if expiry.Nanosecond() > now.Nanosecond() {
		seconds++
	}
	days = int((seconds + secondsPerDay - 1) / secondsPerDay)
	r.State = "current"
	if !expiry.After(now.Add(renewalReminderDays * 24 * time.Hour)) {
		r.State = "due_soon"
	}
	return r
}

func validRenewalFilter(state string) bool {
	switch state {
	case "", "needs_renewal", "due_soon", "expired", "current", "unscheduled", "invalid":
		return true
	default:
		return false
	}
}

// filterOverviewRenewal narrows an already authorized overview without expanding
// its ownership scope or interpreting expiry as a change to subscription status.
func filterOverviewRenewal(o Overview, state string) Overview {
	if state == "" {
		return o
	}
	filtered := Overview{Apps: []App{}, Entitlements: []Entitlement{}, Usage: []Usage{}}
	apps := map[string]bool{}
	subjects := map[[2]string]bool{}
	for _, ent := range o.Entitlements {
		matches := ent.Renewal.State == state
		if state == "needs_renewal" {
			matches = ent.Renewal.State == "due_soon" || ent.Renewal.State == "expired"
		}
		if matches {
			filtered.Entitlements = append(filtered.Entitlements, ent)
			apps[ent.AppID] = true
			subjects[[2]string{ent.AppID, ent.ExternalID}] = true
		}
	}
	for _, app := range o.Apps {
		if apps[app.ID] {
			filtered.Apps = append(filtered.Apps, app)
		}
	}
	for _, usage := range o.Usage {
		if subjects[[2]string{usage.AppID, usage.SubjectID}] {
			filtered.Usage = append(filtered.Usage, usage)
		}
	}
	return filtered
}
