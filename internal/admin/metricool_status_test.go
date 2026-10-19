package admin_test

import (
	"golang.org/x/net/html"
	"net/url"
	"strings"
	"testing"
)

func TestMetricoolConnectionStatusRequiresCredentials(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	check := func(want, unwanted string) {
		t.Helper()
		r, body := s.do("GET", "/admin/settings/metricool", nil)
		// Status assertions belong to the actual settings status, not hidden
		// translations for other components (such as the agent drawer).
		doc, err := html.Parse(strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		var status string
		var found bool
		var text func(*html.Node)
		text = func(n *html.Node) {
			if n.Type == html.TextNode {
				status += n.Data
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				text(c)
			}
		}
		var visit func(*html.Node)
		visit = func(n *html.Node) {
			for _, a := range n.Attr {
				if a.Key == "data-metricool-connection-status" {
					found = true
					text(n)
					return
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				visit(c)
			}
		}
		visit(doc)
		if r.StatusCode != 200 || !found || !strings.Contains(status, want) || strings.Contains(status, unwanted) {
			t.Fatalf("actual connection status %q should contain %q and exclude %q (HTTP %d)", status, want, unwanted, r.StatusCode)
		}
	}
	check("Not configured", "Saved · not tested")
	configureMetricool(t, s)
	check("Saved · not tested", "Not configured")
	c := metricoolConfigValue(t, s)
	if r, _ := s.post("/admin/settings/metricool", url.Values{"version": {c["version"].(string)}, "user_id": {"12"}, "blog_id": {"23"}, "clear_secret": {"on"}}); r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
	check("Not configured", "Saved · not tested")
}
