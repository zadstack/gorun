package admin

import (
	"testing"

	"github.com/zadstack/gorun/internal/observe"
)

func TestCheckFromObservationSeparatesResultFromApplicability(t *testing.T) {
	str := func(s string) *string { return &s }
	match, mismatch := true, false
	cases := []struct {
		name   string
		o      observe.Observation
		status string
		result string
	}{
		{"fresh pass", observe.Observation{Saved: &observe.Saved{At: "2026-10-22T10:00:00Z", AgeSeconds: 60, RevisionMatch: &match, Scope: "connectivity"}, Attempt: &observe.Attempt{Status: "succeeded", FinishedAt: str("2026-10-22T10:00:00Z")}}, "passed", "passed"},
		{"edited configuration", observe.Observation{Saved: &observe.Saved{At: "2026-10-22T10:00:00Z", AgeSeconds: 60, RevisionMatch: &mismatch}}, "stale", "passed"},
		{"older than a week", observe.Observation{Saved: &observe.Saved{At: "2026-10-01T10:00:00Z", AgeSeconds: 8 * 86400, RevisionMatch: &match}}, "expired", "passed"},
		{"newer failure keeps saved result", observe.Observation{Saved: &observe.Saved{At: "2026-10-22T10:00:00Z", AgeSeconds: 60, RevisionMatch: &match}, Attempt: &observe.Attempt{Status: "failed", Error: "timeout", FinishedAt: str("2026-10-22T10:05:00Z"), Remediation: "fix"}}, "failed", "failed"},
		{"queued", observe.Observation{Attempt: &observe.Attempt{Status: "waiting"}}, "waiting", "unknown"},
		{"failed never passed", observe.Observation{Attempt: &observe.Attempt{Status: "failed", Error: "HTTP 401", FinishedAt: str("2026-10-22T10:05:00Z")}}, "failed", "failed"},
		{"legacy without revision", observe.Observation{Saved: &observe.Saved{At: "2026-10-22T10:00:00Z", AgeSeconds: 60}}, "passed", "passed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := checkFromObservation(tc.o, "operation")
			if c.Status != tc.status || c.Result != tc.result || c.Reason == "" || c.Remediation == "" {
				t.Fatalf("%+v", c)
			}
			if tc.o.Saved != nil && (c.ExpiresAt == nil || c.CheckedAt == nil) && c.Status != "failed" {
				t.Fatalf("missing timestamps: %+v", c)
			}
			if tc.name == "newer failure keeps saved result" && (c.CheckedAt == nil || *c.CheckedAt != "2026-10-22T10:05:00Z" || c.Reason != "check_failed: timeout") {
				t.Fatalf("%+v", c)
			}
		})
	}
	if _, ok := operationKind("mcp"); ok {
		t.Fatal("MCP must not have an operation test")
	}
}
