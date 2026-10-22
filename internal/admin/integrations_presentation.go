package admin

import (
	"net/url"
	"strconv"
	"strings"
)

const integrationsPageSize = 100
const integrationsSearchLimit = 200

// SectionAvailable distinguishes unavailable data from an empty configured
// section. Clients must not offer writes based on unavailable defaults.
func (r integrationsReport) SectionAvailable(section string) bool {
	for _, issue := range r.Issues {
		if issue.Section == section {
			return false
		}
	}
	return true
}

func (r *integrationsReport) addIssue(section string) {
	if r.SectionAvailable(section) {
		r.Issues = append(r.Issues, integrationIssue{Section: section, Code: "section_unavailable"})
	}
}

// Pagination affects HTML only. API inventory snapshots retain their complete
// saved resource list and explicit provider completeness flag.
func paginateIntegrations(report *integrationsReport, query url.Values) {
	for i := range report.Inventory {
		view := &report.Inventory[i]
		view.Search = inventorySearchQuery(query.Get("inv_q_" + view.Kind))
		items := filterInventory(view.Items, view.Search)
		applyInventoryPage(view, items, query)
	}
}

func inventorySearchQuery(value string) string {
	runes := []rune(strings.TrimSpace(value))
	return string(runes[:min(len(runes), integrationsSearchLimit)])
}

func filterInventory(items []cfResourceView, query string) []cfResourceView {
	if query == "" {
		return items
	}
	filtered := make([]cfResourceView, 0)
	query = strings.ToLower(query)
	for _, item := range items {
		if strings.Contains(strings.ToLower(item.Name+" "+item.ID+" "+item.Location+" "+item.Class), query) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func applyInventoryPage(view *inventoryView, items []cfResourceView, query url.Values) {
	view.TotalItems = len(items)
	view.Pages = max(1, (len(items)+integrationsPageSize-1)/integrationsPageSize)
	page, _ := strconv.Atoi(query.Get("inv_page_" + view.Kind))
	view.Page = min(max(1, page), view.Pages)
	start := (view.Page - 1) * integrationsPageSize
	view.Items = items[start:min(start+integrationsPageSize, len(items))]
	if view.Page > 1 {
		view.PrevURL = inventoryPageURL(query, view, view.Page-1)
	}
	if view.Page < view.Pages {
		view.NextURL = inventoryPageURL(query, view, view.Page+1)
	}
}

func inventoryPageURL(query url.Values, view *inventoryView, page int) string {
	values := url.Values{}
	for k, v := range query {
		if strings.HasPrefix(k, "inv_q_") || strings.HasPrefix(k, "inv_page_") {
			values[k] = append([]string(nil), v...)
		}
	}
	values.Set("inv_page_"+view.Kind, strconv.Itoa(page))
	values.Set("inv_q_"+view.Kind, view.Search)
	return "/admin/settings/integrations?" + values.Encode() + "#inventory-" + view.Kind
}

func unavailableObservation() observationMeta {
	return observationMeta{Status: "failed", ErrorKey: "admin.integrations.observation.unavailable"}
}
