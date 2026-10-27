package admin

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestCapabilityCheckDistinguishesConnectivityAndConfigurationEvidence(t *testing.T) {
	h := &handlers{d: &kernel.Deps{Config: config.Config{SecretKey: make([]byte, 32)}}}
	// Fresh within evidenceTTL (7d); a fixed calendar stamp expires and flakes CI.
	fresh := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	base := connectionRecord{ID: "profile-id", Version: "config-v2", Name: "Sensitive name", Kind: "mcp", Endpoint: "https://mcp.example/private-capability", TestedAt: fresh}
	for _, tc := range []struct {
		name, saved, version, stamp, want string
		match                             *bool
	}{
		{"legacy pass", "pass", "", base.TestedAt, "passed", nil},
		{"bound pass", "pass", base.Version, base.TestedAt, "passed", boolPtr(true)},
		{"bound failure", "fail", base.Version, base.TestedAt, "failed", boolPtr(true)},
		{"stale success", "pass", "config-v1", base.TestedAt, "stale", boolPtr(false)},
		{"invalid time", "pass", base.Version, "invalid", "unknown", boolPtr(true)},
		{"missing time", "pass", "", "", "unknown", nil},
		{"unknown status", "unexpected", "", base.TestedAt, "unknown", nil},
		{"expired success", "pass", base.Version, time.Now().Add(-8 * 24 * time.Hour).UTC().Format(time.RFC3339), "expired", boolPtr(true)},
		{"future time", "pass", base.Version, time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339), "unknown", boolPtr(true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := base
			c.TestStatus, c.TestVersion, c.TestedAt = tc.saved, tc.version, tc.stamp
			state := h.connectionCapability(c)
			if !state.Available || state.Enabled != nil || state.Configured == nil || !*state.Configured || state.Tested {
				t.Fatalf("invented activation or operational verification: %+v", state)
			}
			check := state.LastCheck
			if check == nil || check.Status != tc.want || check.Kind != "connectivity" || check.Reason == "" || check.Remediation == "" {
				t.Fatalf("bad check: %+v", check)
			}
			if (check.ConfigurationMatch == nil) != (tc.match == nil) || (tc.match != nil && *check.ConfigurationMatch != *tc.match) {
				t.Fatalf("configuration evidence mismatch: %+v", check)
			}
			if (tc.stamp == "" || tc.stamp == "invalid") && check.CheckedAt != nil {
				t.Fatal("invented a check timestamp")
			}
			if tc.saved == "pass" && check.Result != "passed" {
				t.Fatal("lost saved result")
			}
			body, _ := json.Marshal(state)
			for _, private := range []string{"Sensitive name", "private-capability", "mcp.example"} {
				if strings.Contains(string(body), private) {
					t.Fatal("private profile metadata exposed")
				}
			}
		})
	}
	base.TestedAt = ""
	if state := h.connectionCapability(base); state.LastCheck != nil {
		t.Fatal("never-checked profile has fabricated evidence")
	}
}

func TestCapabilityConfigurationDoesNotClaimIncompleteCredentials(t *testing.T) {
	h := &handlers{d: &kernel.Deps{Config: config.Config{SecretKey: make([]byte, 32)}}}
	partial, err := h.encryptConnectionSecrets(connectionSecrets{AccessKey: "only-access-key"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		profile connectionRecord
		reason  string
	}{
		{connectionRecord{Name: "S3", Kind: "s3", Endpoint: "https://s3.example", Bucket: "backups", Region: "auto"}, "credentials_missing"},
		{connectionRecord{Name: "S3", Kind: "s3", Endpoint: "https://s3.example", Bucket: "backups", Region: "auto", Secret: partial}, "credentials_missing"},
		{connectionRecord{Name: "AI", Kind: "ai", Provider: "anthropic", Endpoint: "https://api.anthropic.com/v1", Model: "test-model"}, "credentials_missing"},
		{connectionRecord{Name: "MCP", Kind: "mcp", Endpoint: "https://mcp.example", Secret: "corrupt-secret-sentinel"}, "credentials_unreadable"},
		{connectionRecord{Name: "MCP", Kind: "mcp", Endpoint: "http://mcp.example"}, "invalid_configuration"},
	} {
		state := h.connectionCapability(tc.profile)
		if state.Configured == nil || *state.Configured || state.ConfigurationStatus != "incomplete" || state.Reason != tc.reason {
			t.Fatalf("bad configuration assessment: %+v", state)
		}
	}
	for _, value := range []string{"private\r\nheader", "private\x00token", strings.Repeat("x", 2049)} {
		for _, secrets := range []connectionSecrets{{Token: value}, {AccessKey: value}, {SecretKey: value}} {
			encrypted, err := h.encryptConnectionSecrets(secrets)
			if err != nil {
				t.Fatal(err)
			}
			state := h.connectionCapability(connectionRecord{Name: "Legacy MCP", Kind: "mcp", Endpoint: "https://mcp.example", Secret: encrypted})
			if state.Configured == nil || *state.Configured || state.Reason != "credentials_invalid" {
				t.Fatalf("invalid legacy credentials accepted: %+v", state)
			}
		}
	}
}

func TestCapabilityProbeEvidenceIsVersionBoundAndInvalidatedOnEdit(t *testing.T) {
	base := connectionRecord{Version: "configuration-version"}
	for _, probeErr := range []error{nil, errors.New("private-url-or-secret")} {
		recorded := recordConnectionProbe(base, connectionProbeResult{Count: 3}, probeErr)
		if recorded.TestVersion != base.Version || recorded.TestedAt == "" {
			t.Fatal("new probe is not bound to its configuration")
		}
		raw, _ := json.Marshal(recorded)
		if strings.Contains(string(raw), "private-url-or-secret") {
			t.Fatal("raw provider error persisted")
		}
		edited := changedConnection(recorded)
		if edited.Version == base.Version || edited.TestVersion != "" || edited.TestedAt != "" || edited.TestStatus != "" {
			t.Fatalf("edit preserved obsolete check: %+v", edited)
		}
	}
}
