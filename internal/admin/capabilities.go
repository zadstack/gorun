package admin

import (
	"context"
	"net/http"
	"time"

	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/observe"
)

// ModuleRequirementFilter is the hooks filter modules use to report their
// configuration state to GET /site/capabilities. The payload is
// *[]admin.ModuleRequirement; append one entry per module.
const ModuleRequirementFilter = "system.capabilities.modules"

// ModuleRequirement is a module's own configuration verdict: whether its
// required settings are present, why not, and the practical repair step.
type ModuleRequirement struct {
	Module      string `json:"module"`
	Configured  bool   `json:"configured"`
	Reason      string `json:"reason"`
	Remediation string `json:"remediation"`
}

// CapabilityCheck describes evidence, not a promise that a future operation
// will succeed. Kind says what ran: a connectivity/discovery check or an
// explicit operation test.
type CapabilityCheck struct {
	Kind               string  `json:"kind" enum:"connectivity,operation"`
	Scope              string  `json:"scope" doc:"What exactly ran, e.g. connectivity, operation:send, operation:write_read_delete, operation:complete"`
	Status             string  `json:"status" enum:"passed,failed,unknown,stale,expired,waiting,running"`
	Result             string  `json:"result" enum:"passed,failed,unknown" doc:"Recorded result, retained even when its applicability is stale, expired or unknown"`
	CheckedAt          *string `json:"checked_at" doc:"Persisted RFC3339 time of the saved result; null if missing or invalid"`
	ExpiresAt          *string `json:"expires_at" doc:"When the evidence stops counting (checked_at + 7 days); null when unknown"`
	ConfigurationMatch *bool   `json:"configuration_match" doc:"Null for legacy checks not bound to a configuration revision"`
	Reason             string  `json:"reason"`
	Remediation        string  `json:"remediation"`
}

// CapabilityState separates facts we can observe from facts not yet reported.
// Null configured/enabled values must not be interpreted as false or success.
type CapabilityState struct {
	ID                  string           `json:"id"`
	Kind                string           `json:"kind" enum:"module,service,ai,mcp,s3,unknown"`
	Available           bool             `json:"available"`
	Enabled             *bool            `json:"enabled" doc:"Actual boot activation for modules; null for reusable profiles and services, which do not own feature activation"`
	Configured          *bool            `json:"configured" doc:"Local validation of required settings/credentials; null when the module does not publish configuration requirements"`
	Connected           *bool            `json:"connected" doc:"True only for a current, unexpired, passed connectivity check; null when never checked"`
	Tested              bool             `json:"tested" doc:"True only for a current, unexpired, passed operation test; connectivity never sets this flag"`
	OperationSupported  bool             `json:"operation_supported" doc:"Whether a safe operation test exists for this kind"`
	Source              string           `json:"source"`
	ConfigurationStatus string           `json:"configuration_status" enum:"configured,incomplete,unknown"`
	Reason              string           `json:"reason"`
	Remediation         string           `json:"remediation"`
	LastCheck           *CapabilityCheck `json:"last_check" doc:"Last saved connectivity check, or null; GET never runs one"`
	Operation           *CapabilityCheck `json:"operation" doc:"Last saved operation test, or null; GET never runs one"`
}

// CapabilityReport is GET /site/capabilities.
type CapabilityReport struct {
	ObservedAt  string            `json:"observed_at" doc:"When this local snapshot was read, not a health-check timestamp"`
	Modules     []CapabilityState `json:"modules"`
	Services    []CapabilityState `json:"services" doc:"Core services that are not modules (mail)"`
	Connections []CapabilityState `json:"connections"`
	Coverage    string            `json:"coverage"`
}

func (h *handlers) capabilityAPIOperations() {
	h.d.API.Add(api.Op("GET", "/site/capabilities", "Saved capability evidence (availability, activation, configuration, connectivity, operation tests) with no external checks",
		[]string{"site"}, []string{PermHealth}, nil, CapabilityReport{}, h.capabilitiesAPI))
}

func (h *handlers) capabilitiesAPI(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	profiles, err := h.listConnections(ctx)
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	requirements, err := h.moduleRequirements(ctx)
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	out := CapabilityReport{
		ObservedAt: time.Now().UTC().Format(time.RFC3339), Modules: []CapabilityState{}, Services: []CapabilityState{}, Connections: []CapabilityState{},
		Coverage: "Boot module inventory, module-published configuration requirements, the mail service, and saved connection profiles with their connectivity and operation evidence. Evidence is bound to the configuration revision and expires after 7 days. Profiles do not own feature activation; MCP has no operation test.",
	}
	for _, m := range kernel.Modules(h.d) {
		state := CapabilityState{ID: m.Name, Kind: "module", Available: m.Available, Enabled: boolPtr(m.Enabled), Source: "runtime.boot.modules", ConfigurationStatus: "unknown",
			Reason: "operation_evidence_unavailable", Remediation: "Review the module's configuration and operation-specific checks before relying on it."}
		if !m.Available {
			state.Source, state.Reason, state.Remediation = "config.addons", "module_unavailable", "Install a compatible build containing this addon, then restart. Existing data is retained."
		} else if !m.Enabled {
			state.Reason, state.Remediation = "module_disabled", "Review the addon's activation configuration, then restart to enable it. Existing data is retained."
		} else if m.Name == "media" {
			// The media module boots before admin, so its storage description is read here.
			if st, ok := h.activeStorage(); ok {
				info := st.Describe(ctx)
				state.Configured, state.Source, state.ConfigurationStatus = boolPtr(info.Configured), info.Source, "configured"
				if !info.Configured {
					state.ConfigurationStatus = "incomplete"
				}
				state.Reason, state.Remediation = info.Reason, info.Remediation
			}
		} else if _, ok := requirements[m.Name]; !ok {
			state.Reason, state.Remediation = "module_publishes_no_requirements", "This module has not published configuration requirements; its configuration state is not reported, not assumed."
		} else if req, ok := requirements[m.Name]; ok {
			state.Configured, state.Source = boolPtr(req.Configured), "module.requirements"
			state.ConfigurationStatus = "configured"
			if !req.Configured {
				state.ConfigurationStatus = "incomplete"
			}
			if req.Reason != "" {
				state.Reason = req.Reason
			}
			if req.Remediation != "" {
				state.Remediation = req.Remediation
			}
		}
		out.Modules = append(out.Modules, state)
	}
	out.Services = append(out.Services, h.mailCapability(ctx), h.storageCapability(ctx))
	for _, c := range profiles {
		out.Connections = append(out.Connections, h.connectionCapabilityWithEvidence(ctx, c))
	}
	w.Header().Set("Cache-Control", "no-store")
	api.WriteItem(w, out)
}

func (h *handlers) moduleRequirements(ctx context.Context) (map[string]ModuleRequirement, error) {
	list := []ModuleRequirement{}
	out, err := h.d.Hooks.Apply(ctx, ModuleRequirementFilter, &list)
	if err != nil {
		return nil, err
	}
	if p, ok := out.(*[]ModuleRequirement); ok && p != nil {
		list = *p
	}
	m := map[string]ModuleRequirement{}
	for _, r := range list {
		m[r.Module] = r
	}
	return m, nil
}

func boolPtr(b bool) *bool { return &b }

// mailCapability reports the core mail service: configured means SMTP
// settings exist; tested means a test message was accepted recently by the
// server for the current settings.
func (h *handlers) mailCapability(ctx context.Context) CapabilityState {
	state := CapabilityState{ID: "mail", Kind: "service", Available: true, Source: "settings.mail", OperationSupported: true, Configured: boolPtr(false), ConfigurationStatus: "incomplete",
		Reason: "mail_not_configured", Remediation: "Configure SMTP under Settings → Mail; until then messages are written to the log."}
	if h.mail.Configured(ctx) {
		state.Configured, state.ConfigurationStatus = boolPtr(true), "configured"
		state.Reason, state.Remediation = "operation_not_verified", "Send a test message to yourself (POST /site/capabilities/services/mail/operation) to record delivery evidence."
	}
	// Mail operation evidence is per requesting operator; report the newest.
	if list, err := h.observe.List(ctx, kindOperationMail); err == nil {
		var newest *observe.Observation
		for i := range list {
			o := &list[i]
			if o.Saved == nil && o.Attempt == nil {
				continue
			}
			if newest == nil || newerObservation(o, newest) {
				newest = o
			}
		}
		if newest != nil {
			state.Operation = checkFromObservation(*newest, "operation")
			state.Tested = state.Operation.Status == "passed"
		}
	}
	return state
}

func newerObservation(a, b *observe.Observation) bool {
	at := func(o *observe.Observation) string {
		if o.Attempt != nil && o.Attempt.FinishedAt != nil {
			return *o.Attempt.FinishedAt
		}
		if o.Saved != nil {
			return o.Saved.At
		}
		return ""
	}
	return at(a) > at(b)
}

// connectionCapabilityWithEvidence layers saved observations over the
// legacy probe fields: the observation wins when present because it is
// bound to the revision and carries a timestamp; the legacy record is kept
// readable for profiles never checked since the upgrade.
func (h *handlers) connectionCapabilityWithEvidence(ctx context.Context, c connectionRecord) CapabilityState {
	state := h.connectionCapability(c)
	_, state.OperationSupported = operationKind(c.Kind)
	if o, err := h.observe.Get(ctx, kindConnection, c.ID); err == nil && (o.Saved != nil || o.Attempt != nil) {
		state.LastCheck = checkFromObservation(o, "connectivity")
	}
	if state.LastCheck != nil {
		connected := state.LastCheck.Status == "passed"
		state.Connected = &connected
	}
	if kind, ok := operationKind(c.Kind); ok {
		if o, err := h.observe.Get(ctx, kind, c.ID); err == nil && (o.Saved != nil || o.Attempt != nil) {
			state.Operation = checkFromObservation(o, "operation")
			state.Tested = state.Operation.Status == "passed"
			if state.Tested {
				state.Reason, state.Remediation = "operation_verified", "Evidence expires after 7 days or when the configuration changes; re-run the operation test afterwards."
			}
		}
	}
	return state
}

// checkFromObservation maps an observation to evidence. The saved success
// (if any) carries the result; the newest attempt sets failed/waiting/running.
func checkFromObservation(o observe.Observation, kind string) *CapabilityCheck {
	check := &CapabilityCheck{Kind: kind, Status: "unknown", Result: "unknown", Reason: "check_evidence_incomplete", Remediation: "Run the check again to save current evidence."}
	if o.Saved != nil {
		check.Result, check.Scope = "passed", o.Saved.Scope
		at := o.Saved.At
		check.CheckedAt = &at
		if t, err := time.Parse(time.RFC3339, at); err == nil {
			exp := t.Add(evidenceTTL).UTC().Format(time.RFC3339)
			check.ExpiresAt = &exp
		}
		check.ConfigurationMatch = o.Saved.RevisionMatch
	}
	switch {
	case o.Attempt != nil && (o.Attempt.Status == "waiting" || o.Attempt.Status == "running"):
		check.Status, check.Reason, check.Remediation = o.Attempt.Status, "check_in_progress", "Wait for the queued check to finish, then read again."
	case o.Attempt != nil && o.Attempt.Status == "failed" && (o.Saved == nil || o.Attempt.FinishedAt == nil || *o.Attempt.FinishedAt >= o.Saved.At):
		check.Status, check.Result, check.Reason = "failed", "failed", "check_failed"
		if o.Attempt.Error != "" {
			check.Reason = "check_failed: " + o.Attempt.Error
		}
		check.Remediation = o.Attempt.Remediation
		if check.Remediation == "" {
			check.Remediation = "Review the failure reason, fix the configuration or provider, then run the check again."
		}
		if o.Attempt.FinishedAt != nil {
			check.CheckedAt = o.Attempt.FinishedAt
		}
	case o.Saved == nil:
	case o.Saved.RevisionMatch != nil && !*o.Saved.RevisionMatch:
		check.Status, check.Reason, check.Remediation = "stale", "configuration_changed", "Run the check again for the current configuration."
	case time.Duration(o.Saved.AgeSeconds)*time.Second > evidenceTTL:
		check.Status, check.Reason, check.Remediation = "expired", "evidence_expired", "Evidence older than 7 days no longer counts; run the check again."
	default:
		check.Status = "passed"
		if kind == "connectivity" {
			check.Reason, check.Remediation = "connectivity_only", "Verify the specific operation separately; discovery does not prove execution permissions."
		} else {
			check.Reason, check.Remediation = "operation_verified", "Evidence expires after 7 days or when the configuration changes."
		}
	}
	return check
}

// connectionCapability reads the legacy probe fields saved on the profile
// (configuration validation plus the last synchronous check).
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
	check := &CapabilityCheck{Kind: "connectivity", Scope: "connectivity", Status: "unknown", Result: "unknown", Reason: "check_evidence_incomplete", Remediation: "Run the connection check again to save current evidence."}
	if c.TestStatus == "pass" {
		check.Result = "passed"
	} else if c.TestStatus == "fail" {
		check.Result = "failed"
	}
	if stamp, err := time.Parse(time.RFC3339, c.TestedAt); err == nil && !stamp.After(time.Now()) {
		formatted := stamp.UTC().Format(time.RFC3339)
		check.CheckedAt = &formatted
		exp := stamp.Add(evidenceTTL).UTC().Format(time.RFC3339)
		check.ExpiresAt = &exp
		if c.TestStatus == "pass" {
			check.Status, check.Reason, check.Remediation = "passed", "connectivity_only", "Verify the specific operation separately; discovery does not prove execution permissions."
			if time.Since(stamp) > evidenceTTL {
				check.Status, check.Reason, check.Remediation = "expired", "evidence_expired", "Evidence older than 7 days no longer counts; run the check again."
			}
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

// storageCapability reports the storage new uploads use. Configured is the
// module's own description; tested needs a passed write/read/delete probe.
func (h *handlers) storageCapability(ctx context.Context) CapabilityState {
	state := CapabilityState{ID: "storage", Kind: "service", Available: false, Source: "media.storage", OperationSupported: true, ConfigurationStatus: "unknown",
		Reason: "media_module_unavailable", Remediation: "Boot with the media module to report storage."}
	st, ok := h.activeStorage()
	if !ok {
		return state
	}
	info := st.Describe(ctx)
	state.Available, state.Source = true, info.Source
	state.Configured = boolPtr(info.Configured)
	state.ConfigurationStatus = "configured"
	if !info.Configured {
		state.ConfigurationStatus = "incomplete"
	}
	state.Reason, state.Remediation = info.Reason, info.Remediation
	if o, err := h.observe.Get(ctx, kindOperationStorage, "media"); err == nil && (o.Saved != nil || o.Attempt != nil) {
		state.Operation = checkFromObservation(o, "operation")
		state.Tested = state.Operation.Status == "passed"
	}
	return state
}
