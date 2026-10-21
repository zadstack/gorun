package admin

import (
	"net/http"
	"time"

	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/kernel"
)

// CapabilityCheck describes evidence, not a promise that a future operation
// will succeed. Existing connection tests only exercise discovery/connectivity.
type CapabilityCheck struct {
	Kind               string  `json:"kind" enum:"connectivity"`
	Status             string  `json:"status" enum:"passed,failed,unknown,stale"`
	Result             string  `json:"result" enum:"passed,failed,unknown" doc:"Recorded result, retained even when its applicability is stale or unknown"`
	CheckedAt          *string `json:"checked_at" doc:"Persisted RFC3339 time; null if missing or invalid"`
	ConfigurationMatch *bool   `json:"configuration_match" doc:"Null for legacy checks not bound to a configuration version"`
	Reason             string  `json:"reason"`
	Remediation        string  `json:"remediation"`
}

// CapabilityState separates facts we can observe from facts not yet reported.
// Null configured/enabled values must not be interpreted as false or success.
type CapabilityState struct {
	ID                  string           `json:"id"`
	Kind                string           `json:"kind" enum:"module,ai,mcp,s3,unknown"`
	Available           bool             `json:"available"`
	Enabled             *bool            `json:"enabled" doc:"Actual boot activation for modules; null for reusable profiles, which do not own feature activation"`
	Configured          *bool            `json:"configured" doc:"Local profile validation only; null when the module does not publish configuration requirements"`
	Tested              bool             `json:"tested" doc:"True only for successful operation-level evidence matching the current configuration; connectivity never sets this flag"`
	Source              string           `json:"source"`
	ConfigurationStatus string           `json:"configuration_status" enum:"configured,incomplete,unknown"`
	Reason              string           `json:"reason"`
	Remediation         string           `json:"remediation"`
	LastCheck           *CapabilityCheck `json:"last_check" doc:"Last saved check, or null when no check exists; GET does not run a check"`
}

type CapabilityReport struct {
	ObservedAt  string            `json:"observed_at" doc:"When this local snapshot was read, not a health-check timestamp"`
	Modules     []CapabilityState `json:"modules"`
	Connections []CapabilityState `json:"connections"`
	Coverage    string            `json:"coverage"`
}

func (h *handlers) capabilityAPIOperations() {
	h.d.API.Add(api.Op("GET", "/site/capabilities", "Saved capability evidence and actual module activation (no external checks)",
		[]string{"site"}, []string{PermHealth}, nil, CapabilityReport{}, h.capabilitiesAPI))
}

func (h *handlers) capabilitiesAPI(w http.ResponseWriter, r *http.Request) {
	profiles, err := h.listConnections(r.Context())
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	out := CapabilityReport{
		ObservedAt: time.Now().UTC().Format(time.RFC3339), Modules: []CapabilityState{}, Connections: []CapabilityState{},
		Coverage: "Boot module inventory and saved connection profiles only. Module configuration and operation-level verification are not yet reported. Profiles do not own feature activation.",
	}
	for _, m := range kernel.Modules(h.d) {
		state := CapabilityState{ID: m.Name, Kind: "module", Available: m.Available, Enabled: boolPtr(m.Enabled), Source: "runtime.boot.modules", ConfigurationStatus: "unknown",
			Reason: "operation_evidence_unavailable", Remediation: "Review the module's configuration and operation-specific checks before relying on it."}
		if !m.Available {
			state.Source, state.Reason, state.Remediation = "config.addons", "module_unavailable", "Install a compatible build containing this addon, then restart. Existing data is retained."
		} else if !m.Enabled {
			state.Reason, state.Remediation = "module_disabled", "Review the addon's activation configuration, then restart to enable it. Existing data is retained."
		}
		out.Modules = append(out.Modules, state)
	}
	for _, c := range profiles {
		out.Connections = append(out.Connections, h.connectionCapability(c))
	}
	w.Header().Set("Cache-Control", "no-store")
	api.WriteItem(w, out)
}

func boolPtr(b bool) *bool { return &b }

func (h *handlers) connectionCapability(c connectionRecord) CapabilityState {
	state := CapabilityState{ID: c.ID, Kind: c.Kind, Available: true, Source: "settings.connections.profile", Configured: boolPtr(true), ConfigurationStatus: "configured",
		Reason: "operation_not_verified", Remediation: "Verify the specific operation with appropriate permissions; a discovery or connectivity check is insufficient."}
	switch c.Kind {
	case "ai", "mcp", "s3":
	default:
		state.Kind, state.Available = "unknown", false
	}
	if validateConnection(c) != nil {
		state.Configured, state.ConfigurationStatus = boolPtr(false), "incomplete"
		state.Reason, state.Remediation = "invalid_configuration", "Review and save valid connection settings."
	} else if secrets, err := h.connectionSecrets(c); err != nil {
		state.Configured, state.ConfigurationStatus = boolPtr(false), "incomplete"
		state.Reason, state.Remediation = "credentials_unreadable", "Re-enter the connection credentials with the current site secret key."
	} else if !validConnectionSecret(secrets.Token) || !validConnectionSecret(secrets.AccessKey) || !validConnectionSecret(secrets.SecretKey) {
		state.Configured, state.ConfigurationStatus = boolPtr(false), "incomplete"
		state.Reason, state.Remediation = "credentials_invalid", "Re-enter valid connection credentials, then run the connection check."
	} else if (c.Kind == "s3" && (secrets.AccessKey == "" || secrets.SecretKey == "")) || (c.Kind == "ai" && c.Provider == "anthropic" && secrets.Token == "") {
		state.Configured, state.ConfigurationStatus = boolPtr(false), "incomplete"
		state.Reason, state.Remediation = "credentials_missing", "Save the credentials required by this provider, then run the connection check."
	}
	if c.TestStatus == "" && c.TestedAt == "" {
		return state
	}
	check := &CapabilityCheck{Kind: "connectivity", Status: "unknown", Result: "unknown", Reason: "check_evidence_incomplete", Remediation: "Run the connection check again to save current evidence."}
	if c.TestStatus == "pass" {
		check.Result = "passed"
	} else if c.TestStatus == "fail" {
		check.Result = "failed"
	}
	if stamp, err := time.Parse(time.RFC3339, c.TestedAt); err == nil && !stamp.After(time.Now()) {
		formatted := stamp.UTC().Format(time.RFC3339)
		check.CheckedAt = &formatted
		if c.TestStatus == "pass" {
			check.Status, check.Reason, check.Remediation = "passed", "connectivity_only", "Verify the specific operation separately; discovery does not prove execution permissions."
		} else if c.TestStatus == "fail" {
			check.Status, check.Reason, check.Remediation = "failed", "connection_check_failed", "Review connection settings, credentials and provider availability, then run the check again."
		}
	}
	if c.TestVersion != "" {
		check.ConfigurationMatch = boolPtr(c.TestVersion == c.Version)
		if !*check.ConfigurationMatch {
			check.Status, check.Reason = "stale", "configuration_changed"
			check.Remediation = "Run the connection check again for the current configuration."
		}
	}
	state.LastCheck = check
	return state
}
