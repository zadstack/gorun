package admin

import (
	"context"
	"html/template"
	"net/http"
	"time"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/jobs"
)

const recentAuditCount = 10

// dashboardPage is the data of pages/dashboard.html. It matches the auth
// placeholder handler's data (Widgets only) so both routes can render the
// template until the placeholder is removed.
type dashboardPage struct{ Widgets []template.HTML }

func (h *handlers) dashboard(w http.ResponseWriter, r *http.Request) {
	var page dashboardPage
	state, err := h.dashboardLayout(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	widgets, err := h.dashboardRendered(r, false)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	for _, wd := range orderedDashboard(widgets, state.Order) {
		controls, err := h.fragment(r, "pages/system/fragments/dashboard_layout_controls.html", struct {
			ID      string
			Version int64
		}{wd.ID, state.Version})
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		page.Widgets = append(page.Widgets, controls+wd.HTML)
	}

	h.tpl.Render(w, r, "pages/dashboard.html", page)
}

func (h *handlers) registerWidgets() {
	h.d.Admin.AddWidget("review_queue", h.widgetReviewQueue)
	h.d.Admin.AddWidget("workspace_choice", h.widgetWorkspaceChoice)
	h.d.Admin.AddWidget("workspace_guide", h.widgetWorkspaceGuide)
	h.d.Admin.AddWidget("setup_status", h.widgetSetupStatus)
	h.d.Admin.AddWidget("quick_actions", h.widgetQuickActions)
	h.d.Admin.AddWidget("dashboard_pins", h.widgetDashboardPins)
	h.d.Admin.AddWidget("workspace", h.widgetWorkspace)
	h.d.Admin.AddWidget("recent_audit", h.widgetRecentAudit)
	h.d.Admin.AddWidget("jobs_summary", h.widgetJobs)
	h.d.Admin.AddWidget("system_summary", h.widgetSystem)
	h.d.Admin.AddWidget("canvas_gallery", h.widgetCanvasGallery)
}

type quickAction struct{ LabelKey, Label, Href, Icon string }

// widgetQuickActions links to "new" screens the user may use: one per
// content type (when the content module is present), new user, new key.
func (h *handlers) widgetQuickActions(ctx context.Context, r *http.Request) (template.HTML, error) {
	var actions []quickAction
	if cs, ok := h.contentService(); ok && can(r, "content.create") {
		types, err := cs.Types(ctx)
		if err != nil {
			return "", err
		}
		locale := i18n.Locale(ctx)
		for _, t := range types {
			name := t.Singular[locale]
			if name == "" {
				name = t.Singular["en"]
			}
			if name == "" {
				name = t.Slug
			}
			icon := t.Icon
			if icon == "" {
				icon = "file-plus"
			}
			// Assumes the content admin's "new item" URL.
			actions = append(actions, quickAction{Label: name, Href: "/admin/content/" + t.Slug + "/new", Icon: icon})
		}
	}
	if can(r, PermUsers) {
		actions = append(actions, quickAction{LabelKey: "admin.dashboard.new_user", Href: "/admin/users/new", Icon: "user-plus"})
	}
	if can(r, PermAPIKeys) && WorkspaceShowsAdvanced(ctx, h.d.Settings) {
		actions = append(actions, quickAction{LabelKey: "admin.dashboard.new_key", Href: "/admin/agents/keys", Icon: "key-round"})
	}
	if len(actions) == 0 {
		return "", nil
	}
	return h.fragment(r, "pages/system/widgets/quick_actions.html", actions)
}

func (h *handlers) widgetRecentAudit(ctx context.Context, r *http.Request) (template.HTML, error) {
	if !can(r, PermHealth) {
		return "", nil
	}
	entries, _, err := h.d.Audit.List(ctx, audit.ListQuery{Limit: recentAuditCount})
	if err != nil {
		return "", err
	}
	return h.fragment(r, "pages/system/widgets/recent_audit.html", entries)
}

func (h *handlers) widgetJobs(ctx context.Context, r *http.Request) (template.HTML, error) {
	if !can(r, PermHealth) {
		return "", nil
	}
	stats, err := h.d.Jobs.Stats(ctx)
	if err != nil {
		return "", err
	}
	return h.fragment(r, "pages/system/widgets/jobs_summary.html", struct{ Stats jobs.Stats }{stats})
}

type systemSummary struct {
	GoVersion, DBSize, WALSize, DiskFree, Uptime, Theme string
}

func (h *handlers) widgetSystem(ctx context.Context, r *http.Request) (template.HTML, error) {
	if !can(r, PermHealth) {
		return "", nil
	}
	sum := systemSummary{GoVersion: goVersion(), DBSize: fileSize(h.d.Config.DBPath()), WALSize: fileSize(h.d.Config.DBPath() + "-wal"),
		DiskFree: "—", Uptime: time.Since(h.started).Truncate(time.Second).String(), Theme: h.activeTheme(ctx)}
	if free, _, ok := diskUsage(h.d.Config.DataDir); ok {
		sum.DiskFree = humanBytes(free)
	}
	return h.fragment(r, "pages/system/widgets/system_summary.html", sum)
}
