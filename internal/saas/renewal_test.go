package saas

import (
	"encoding/json"
	"github.com/zadstack/gorun/internal/api/apitest"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRenewalMetadataBoundaries(t *testing.T) {
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name, expiry, state string
		seconds             int64
		days                int
		null                bool
	}{
		{"absent", "", "unscheduled", 0, 0, true},
		{"invalid", "tomorrow", "invalid", 0, 0, true},
		{"past", "2026-10-13T00:00:00Z", "expired", 0, 0, false},
		{"exact expiry", "2026-10-14T00:00:00Z", "expired", 0, 0, false},
		{"positive fraction", "2026-10-14T00:00:00.001Z", "due_soon", 1, 1, false},
		{"exact day", "2026-10-15T00:00:00Z", "due_soon", 86400, 1, false},
		{"day plus fraction", "2026-10-15T00:00:00.001Z", "due_soon", 86401, 2, false},
		{"exact reminder", "2026-10-21T00:00:00Z", "due_soon", 604800, 7, false},
		{"outside reminder", "2026-10-21T00:00:00.001Z", "current", 604801, 8, false},
		{"timezone equivalent", "2026-10-21T04:00:00+04:00", "due_soon", 604800, 7, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := renewalMetadata(tc.expiry, now.In(time.FixedZone("local", 14400)))
			if got.State != tc.state || got.ReminderDays != 7 || got.ObservedAt != "2026-10-14T00:00:00Z" {
				t.Fatalf("metadata: %+v", got)
			}
			if tc.null {
				if got.RemainingSeconds != nil || got.RemainingDays != nil {
					t.Fatal("unknown expiry must not have countdown")
				}
				return
			}
			if got.RemainingSeconds == nil || *got.RemainingSeconds != tc.seconds || got.RemainingDays == nil || *got.RemainingDays != tc.days {
				t.Fatalf("countdown: %+v", got)
			}
		})
	}
}

func TestOverviewRenewalJSONAndStatusPreservation(t *testing.T) {
	s, owner, _ := setup(t)
	for _, status := range []string{"active", "suspended", "revoked"} {
		if _, err := s.DB.Exec(`UPDATE saas_entitlements SET status=?,valid_until='2026-10-21T00:00:00Z'`, status); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		(&handlers{s}).overview(false)(w, httptest.NewRequest("GET", "/api/v1/saas", nil).WithContext(owner))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var response struct {
			Data Overview `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Data.Entitlements) != 1 {
			t.Fatal(w.Body.String())
		}
		ent := response.Data.Entitlements[0]
		if ent.Status != status || ent.Renewal.State != "due_soon" || ent.Renewal.RemainingDays == nil || *ent.Renewal.RemainingDays != 7 {
			t.Fatalf("status/countdown lost: %+v", ent)
		}
		var stored string
		if err := s.DB.QueryRow(`SELECT status FROM saas_entitlements WHERE id='ent'`).Scan(&stored); err != nil || stored != status {
			t.Fatal("read mutated state", stored, err)
		}
	}
}

func TestRenewalOpenAPIContract(t *testing.T) {
	app := apitest.Boot(t, New())
	w := apitest.NewClient(t, app).Anonymous().Do("GET", "/api/v1/openapi.json", nil, nil)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	for _, field := range []string{`"renewal"`, `"remaining_seconds"`, `"remaining_days"`, `"reminder_days"`, `"due_soon"`, `"unscheduled"`} {
		if !strings.Contains(w.Body.String(), field) {
			t.Fatalf("missing OpenAPI contract %s", field)
		}
	}
}
