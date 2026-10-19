package admin

import (
	"context"
	"errors"
	"html/template"
	"net/http"

	"github.com/zadstack/gorun/internal/settings"
)

type dashboardSetupItem struct{ TitleKey, StatusKey, Href string }

func (h *handlers) widgetSetupStatus(ctx context.Context, r *http.Request) (template.HTML, error) {
	if !can(r, PermSettings) {
		return "", nil
	}
	var items []dashboardSetupItem
	advanced := WorkspaceShowsAdvanced(ctx, h.d.Settings)
	entries := []struct{ title, key, href string }{{"email", "mail.host", "/admin/settings/mail"}}
	if advanced {
		entries = append(entries, struct{ title, key, href string }{"ai", "ai.provider", "/admin/settings/ai"})
	}
	for _, entry := range entries {
		status := "missing"
		var v string
		err := h.d.Settings.Get(ctx, entry.key, &v)
		if err != nil && !errors.Is(err, settings.ErrNotFound) {
			return "", err
		}
		if err == nil {
			if v == "" {
				status = "off"
			} else {
				status = "configured"
				if entry.key == "ai.provider" && (h.d.Settings.GetString(ctx, "ai.api_key", "") == "" || h.d.Settings.GetString(ctx, "ai.model", "") == "") {
					status = "missing"
				}
			}
		}
		if entry.title == "email" && h.d.Settings.GetString(ctx, "mail.provider", "smtp") == "resend" {
			status = "missing"
			if h.d.Settings.GetString(ctx, "mail.resend_api_key", "") != "" && h.d.Settings.GetString(ctx, "mail.from", "") != "" {
				status = "configured"
			}
		}
		items = append(items, dashboardSetupItem{"dashboardlayout." + entry.title, "dashboardlayout." + status, entry.href})
	}
	if advanced {
		connections, err := h.listConnections(ctx)
		if err != nil {
			return "", err
		}
		status := "missing"
		if len(connections) > 0 {
			status = "configured"
		}
		items = append(items, dashboardSetupItem{"dashboardlayout.connections", "dashboardlayout." + status, "/admin/settings/connections"})
	}
	metricool, err := h.metricoolConfig(ctx)
	if err != nil {
		return "", err
	}
	social := "missing"
	if metricool.Secret != "" && metricool.UserID != "" && metricool.BlogID != "" {
		social = "configured"
	}
	items = append(items, dashboardSetupItem{"dashboardlayout.social", "dashboardlayout." + social, "/admin/settings/metricool"})
	return h.fragment(r, "pages/system/widgets/setup_status.html", items)
}
