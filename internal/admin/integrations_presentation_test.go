package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestIntegrationsInventoryPresentationBoundsPagesAndPreservesProviderCoverage(t *testing.T) {
	for _, tc := range []struct {
		name, page string
		want       int
	}{{"negative", "-9", 1}, {"past_end", "999999", 3}, {"overflow", "999999999999999999999999999999", 3}} {
		t.Run(tc.name, func(t *testing.T) {
			items := make([]cfResourceView, 250)
			report := integrationsReport{Inventory: []inventoryView{{Kind: "r2", Count: 900, Complete: false, Items: items}}}
			paginateIntegrations(&report, url.Values{"inv_page_r2": {tc.page}})
			v := report.Inventory[0]
			if v.Page != tc.want || v.Pages != 3 || len(v.Items) > 100 || v.TotalItems != 250 {
				t.Fatalf("pagination: page=%d pages=%d len=%d total=%d", v.Page, v.Pages, len(v.Items), v.TotalItems)
			}
			if v.Complete || v.Count != 900 {
				t.Fatal("local paging changed provider coverage")
			}
		})
	}
}

func TestIntegrationsInventorySearchSupportsUnicodeAndEscapedNavigation(t *testing.T) {
	query := `مرسى & "<script>alert(1)</script>`
	items := make([]cfResourceView, 101)
	for i := range items {
		items[i] = cfResourceView{cfResource: cfResource{Name: query, ID: "resource"}}
	}
	report := integrationsReport{Inventory: []inventoryView{{Kind: "r2", Count: 101, Complete: true, Items: items}, {Kind: "d1", Count: 1, Complete: true, Items: []cfResourceView{{cfResource: cfResource{Name: "ÉCOLE"}}}}}}
	paginateIntegrations(&report, url.Values{"inv_q_r2": {query}, "inv_q_d1": {"école"}, "token": {"must-not-copy"}})
	v := report.Inventory[0]
	if len(v.Items) != 100 || v.TotalItems != 101 || !v.Complete {
		t.Fatalf("Unicode search lost rows or coverage: %+v", v)
	}
	next, err := url.Parse(v.NextURL)
	if err != nil {
		t.Fatal(err)
	}
	if next.Query().Get("inv_q_r2") != query || next.Query().Get("inv_page_r2") != "2" || next.Query().Has("token") {
		t.Fatalf("navigation query not safely preserved: %s", v.NextURL)
	}
	if len(report.Inventory[1].Items) != 1 {
		t.Fatal("non-ASCII case-insensitive search failed")
	}
	h, req, _ := integrationsPerformanceFixture(t, 1, false)
	req.URL.RawQuery = url.Values{"inv_q_r2": {query}}.Encode()
	w := httptest.NewRecorder()
	h.integrationsGet(w, req)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "<script>alert(1)</script>") {
		t.Fatal("search query was not safely rendered")
	}
	if !strings.Contains(w.Body.String(), "&lt;script&gt;") {
		t.Fatal("search value missing from escaped input")
	}
}

func TestIntegrationsCorruptCloudflareSettingsLeavesOtherSectionsAvailable(t *testing.T) {
	h, req, calls := integrationsPerformanceFixture(t, 1, false)
	const bad = "bad-value-must-never-render"
	// A wrong JSON type is valid settings storage but invalid Cloudflare config.
	if err := h.d.Settings.Set(context.Background(), settingCFAccount, map[string]string{"sensitive": bad}); err != nil {
		t.Fatal(err)
	}
	if err := h.d.Settings.Set(context.Background(), "mail.reply_to", "support@example.com"); err != nil {
		t.Fatal(err)
	}
	report, err := h.integrationsReport(req.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report.SectionAvailable("cloudflare") || !report.SectionAvailable("storage") || !report.SectionAvailable("mail") {
		t.Fatalf("failure was not scoped: %+v", report.Issues)
	}
	if len(report.Storage) != 2 {
		t.Fatal("storage bindings disappeared")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), bad) || strings.Contains(string(encoded), fakeCFToken) {
		t.Fatal("invalid settings leaked in report")
	}
	w := httptest.NewRecorder()
	h.integrationsGet(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("section failure returned %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"Storage and backup buckets", "Reply-To", "support@example.com"} {
		if !strings.Contains(body, want) {
			t.Errorf("working section missing %q", want)
		}
	}
	if strings.Contains(body, bad) || strings.Contains(body, fakeCFToken) {
		t.Fatal("invalid settings leaked into HTML")
	}
	if calls.Load() != 0 {
		t.Fatal("GET contacted Cloudflare")
	}
}
