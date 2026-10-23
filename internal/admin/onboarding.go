package admin

import (
	"context"
	"net/http"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/menus"
)

type dashboardSetupItem struct {
	TitleKey, Title, StatusKey, HelpKey, Href, State, Stage, ActionKey string
	Evidence                                                           []onboardingEvidence
	Optional                                                           bool
}
type onboardingEvidence struct {
	KindKey, StatusKey, CheckedAt string
}

type onboardingView struct {
	Items     []dashboardSetupItem
	Attention int
}

// This projection reads local configuration and stored observations only. A GET
// never queues a check, sends mail, imports data or contacts a provider.
func (h *handlers) onboardingState(ctx context.Context, r *http.Request) (onboardingView, error) {
	view := onboardingView{}
	add := func(name, state, href, stage string, optional bool) {
		status := "onboarding.state." + state
		if state == "missing" || state == "off" || state == "configured" {
			status = "dashboardlayout." + state
		}
		view.Items = append(view.Items, dashboardSetupItem{TitleKey: "onboarding." + name, StatusKey: status, HelpKey: "onboarding." + name + "_help", Href: href, State: state, Stage: stage, ActionKey: "onboarding.action." + name, Optional: optional})
		if !optional && (state == "missing" || state == "error") {
			view.Attention++
		}
	}
	identity := "missing"
	if h.d.Settings.GetString(ctx, "site.title", "") != "" {
		identity = "configured"
	}
	add("identity", identity, "/admin/appearance/identity", "essentials", false)
	if can(r, "content.view") {
		if service, ok := h.contentService(); ok {
			published, err := service.Query(ctx, content.Query{Type: "page", Status: "published", PerPage: 1})
			if err != nil {
				return view, err
			}
			state := "missing"
			if published.Total > 0 {
				state = "configured"
			}
			add("content", state, "/admin/content/page", "essentials", false)
		}
	}
	if can(r, "menus.manage") {
		if service, ok := kernel.Get[menus.Service](h.d, kernel.ServiceMenus); ok {
			state, err := onboardingMenuState(ctx, service)
			if err != nil {
				return view, err
			}
			add("menus", state, "/admin/menus", "essentials", false)
		}
	}
	mail := h.onboardingMailState(ctx)
	add("email", mail, "/admin/settings/mail", "optional", true)
	view.Items[len(view.Items)-1].withCapability(h.mailCapability(ctx))
	ai := "missing"
	provider := h.d.Settings.GetString(ctx, "ai.provider", "")
	if provider != "" && h.d.Settings.GetString(ctx, "ai.api_key", "") != "" && h.d.Settings.GetString(ctx, "ai.model", "") != "" {
		ai = "configured"
	}
	var selected string
	if h.d.Settings.Get(ctx, "ai.provider", &selected) == nil && selected == "" {
		ai = "off"
	}
	add("ai", ai, "/admin/settings/ai", "optional", true)
	profiles, err := h.listConnections(ctx)
	if err != nil {
		return view, err
	}
	for _, profile := range profiles {
		add("connections", "missing", "/admin/settings/connections", "optional", true)
		row := &view.Items[len(view.Items)-1]
		row.Title = profile.Name
		row.withCapability(h.connectionCapabilityWithEvidence(ctx, profile))
	}
	cf := "missing"
	snapshot, err := h.cloudflareSnapshot(ctx)
	if err != nil {
		cf = "error"
	} else if snapshot.configured() {
		cf = "configured"
	}
	add("cloudflare", cf, "/admin/settings/integrations#integrations-cloudflare", "optional", true)
	if err == nil {
		observation, readErr := h.observe.Get(ctx, kindCFScopes, cfObservationKey)
		if readErr == nil && (observation.Saved != nil || observation.Attempt != nil) {
			check := checkFromObservation(observation, "connectivity")
			// Cloudflare discovery has a shorter freshness contract than generic profiles.
			if check.Status == "passed" && !currentCFObservation(observation, cfScopesFreshness) {
				check.Status = "expired"
			}
			row := &view.Items[len(view.Items)-1]
			row.Evidence = onboardingChecks(check, nil)
			if check.Status == "passed" {
				row.State = "connected"
				row.StatusKey = "onboarding.state.connected"
			}
		}
	}
	edge := "missing"
	instances, err := h.edgeInstances(ctx)
	if err != nil {
		edge = "error"
	} else if len(instances.Records) > 0 {
		edge = "configured"
		for _, record := range instances.Records {
			if record.Instance.State == "error" {
				edge = "error"
				break
			}
		}
	}
	add("edge", edge, "/admin/settings/edge/instances", "optional", true)
	storage := "unavailable"
	if _, ok := h.mediaService(); ok {
		storage = "local"
		if h.d.Settings.GetString(ctx, "storage.active.media", "") != "" {
			storage = "configured"
		}
	}
	add("storage", storage, "/admin/settings/storage", "operations", true)
	view.Items[len(view.Items)-1].withCapability(h.storageCapability(ctx))
	if can(r, PermBackup) {
		backup := "missing"
		var success backupSuccess
		if h.d.Settings.Get(ctx, SettingBackupLastSuccess, &success) == nil && success.At != "" && success.Stamp != "" {
			backup = "recorded"
		}
		add("backups", backup, "/admin/system/backups", "operations", false)
	}
	if can(r, PermHealth) {
		readiness := "reachable"
		if h.d.DB.PingContext(ctx) != nil {
			readiness = "error"
		}
		add("readiness", readiness, "/admin/system/health", "operations", false)
		add("modules", "review", "/admin/system/modules", "operations", true)
	}
	return view, nil
}

func onboardingMenuState(ctx context.Context, service menus.Service) (string, error) {
	entries, err := service.List(ctx)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.Location == "" {
			continue
		}
		menu, err := service.Get(ctx, entry.Slug)
		if err != nil {
			return "", err
		}
		if len(menu.Items) > 0 {
			return "configured", nil
		}
	}
	return "missing", nil
}

func (h *handlers) onboardingMailState(ctx context.Context) string {
	if h.d.Settings.GetString(ctx, "mail.provider", "smtp") == "resend" {
		if h.d.Settings.GetString(ctx, "mail.resend_api_key", "") != "" && h.d.Settings.GetString(ctx, "mail.from", "") != "" {
			return "configured"
		}
		return "missing"
	}
	var host string
	if h.d.Settings.Get(ctx, "mail.host", &host) != nil {
		return "missing"
	}
	if host == "" {
		return "off"
	}
	return "configured"
}

func (h *handlers) onboardingPage(w http.ResponseWriter, r *http.Request) {
	if !can(r, PermSettings) {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	view, err := h.onboardingState(r.Context(), r)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.tpl.Render(w, r, "pages/system/onboarding.html", view)
}

type onboardingEdgeView struct {
	Records                []edgeInstance
	Revision               string
	AccountID, AccountName string
}

func (h *handlers) edgeInstancesPage(w http.ResponseWriter, r *http.Request) {
	if !can(r, PermSettings) {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	snapshot, err := h.edgeInstances(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	account, err := h.cloudflareSnapshot(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.tpl.Render(w, r, "pages/system/onboarding_edge.html", onboardingEdgeView{Records: snapshot.public(), Revision: snapshot.revision(), AccountID: account.AccountID, AccountName: account.AccountName})
}

func (row *dashboardSetupItem) withCapability(capability CapabilityState) {
	row.Evidence = onboardingChecks(capability.LastCheck, capability.Operation)
	switch {
	case !capability.Available:
		row.State = "unavailable"
	case capability.Tested:
		row.State = "tested"
	case capability.Connected != nil && *capability.Connected:
		row.State = "connected"
	case capability.Configured != nil && *capability.Configured:
		row.State = "configured"
	default:
		row.State = "missing"
	}
	row.StatusKey = "onboarding.state." + row.State
	if row.State == "configured" || row.State == "missing" {
		row.StatusKey = "dashboardlayout." + row.State
	}
}

func onboardingChecks(connectivity, operation *CapabilityCheck) []onboardingEvidence {
	checks := []onboardingEvidence{}
	for _, check := range []*CapabilityCheck{connectivity, operation} {
		if check == nil {
			continue
		}
		stamp := ""
		if check.CheckedAt != nil {
			stamp = *check.CheckedAt
		}
		checks = append(checks, onboardingEvidence{KindKey: "onboarding.evidence." + check.Kind, StatusKey: "onboarding.check." + check.Status, CheckedAt: stamp})
	}
	return checks
}
