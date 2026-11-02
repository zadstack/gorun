package commerce

import (
	"context"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/zadstack/gorun/internal/config"
)

func accountElement(t *testing.T, root *html.Node, tag, key, value string) *html.Node {
	t.Helper()
	var walk func(*html.Node) *html.Node
	walk = func(n *html.Node) *html.Node {
		if n.Type == html.ElementNode && n.Data == tag {
			for _, a := range n.Attr {
				if a.Key == key && a.Val == value {
					return n
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if found := walk(c); found != nil {
				return found
			}
		}
		return nil
	}
	return walk(root)
}

func accountDocument(t *testing.T, page string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestAccountAlternativeMethodsRemainUsableWithoutJavaScript(t *testing.T) {
	for _, tc := range []struct{ theme, locale, label string }{
		{"default", "ar", "الدخول برابط عبر البريد بدلًا من كلمة المرور"},
		{"business", "en", "Sign in by email instead"},
	} {
		t.Run(tc.theme+"/"+tc.locale, func(t *testing.T) {
			e := bootCfg(t, func(c *config.Config) { c.Theme = tc.theme })
			if err := e.svc.d.Settings.Set(context.Background(), "site.locale", tc.locale); err != nil {
				t.Fatal(err)
			}
			if err := e.svc.d.Settings.Set(context.Background(), "commerce.registration_enabled", true); err != nil {
				t.Fatal(err)
			}
			resp, page := e.do("GET", "/account?next=/agents", nil)
			if resp.StatusCode != 200 {
				t.Fatalf("account: %d", resp.StatusCode)
			}
			if !strings.Contains(page, tc.label) {
				t.Fatal("account method label is not localized")
			}
			doc := accountDocument(t, page)
			for _, path := range []string{"/account/password-login", "/account/login", "/account/register"} {
				form := accountElement(t, doc, "form", "action", path)
				if form == nil || accountElement(t, form, "input", "name", "_csrf") == nil {
					t.Fatalf("method %s lost its form or CSRF field", path)
				}
				if path != "/account/login" && accountElement(t, form, "input", "value", "/agents") == nil {
					t.Fatalf("method %s lost its return destination", path)
				}
				if path == "/account/password-login" {
					continue
				}
				if form.Parent == nil || form.Parent.Data != "details" {
					t.Fatalf("alternative %s must use native progressive disclosure", path)
				}
				var summary bool
				for n := form.Parent.FirstChild; n != nil; n = n.NextSibling {
					summary = summary || n.Type == html.ElementNode && n.Data == "summary"
				}
				if !summary {
					t.Fatalf("alternative %s has no keyboard-operable summary", path)
				}
			}
			if !strings.Contains(page, "/themes/default/assets/commerce.css?") {
				t.Fatal("inherited account layout stylesheet missing")
			}
			if len(e.mail.all()) != 0 || len(e.stripe.reqs) != 0 {
				t.Fatal("rendering account must not send mail or request payment")
			}
		})
	}
}

func TestAccountFeedbackOpensTheRelevantDisclosure(t *testing.T) {
	e := boot(t)
	if err := e.svc.d.Settings.Set(context.Background(), "commerce.registration_enabled", true); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, action, role string }{
		{"/account?sent=1", "/account/login", "status"},
		{"/account?error=email", "/account/login", "alert"},
		{"/account?error=rate", "/account/login", "alert"},
		{"/account?error=registration", "/account/register", "alert"},
	} {
		_, page := e.do("GET", tc.path, nil)
		doc := accountDocument(t, page)
		form := accountElement(t, doc, "form", "action", tc.action)
		if form == nil || form.Parent == nil || form.Parent.Data != "details" {
			t.Fatalf("%s form disclosure absent", tc.action)
		}
		open := false
		for _, a := range form.Parent.Attr {
			open = open || a.Key == "open"
		}
		if !open || accountElement(t, doc, "p", "role", tc.role) == nil {
			t.Fatalf("%s feedback is hidden or unannounced", tc.path)
		}
	}
}

func TestSignedInEmptyAccountHasDistinctServicesAndOrders(t *testing.T) {
	e := boot(t)
	_, user, err := e.svc.ensureCustomer(context.Background(), "empty@example.test")
	if err != nil {
		t.Fatal(err)
	}
	e.signInCustomer(user.ID)
	_, page := e.do("GET", "/account", nil)
	doc := accountDocument(t, page)
	for _, id := range []string{"account-services-title", "account-orders-title"} {
		section := accountElement(t, doc, "section", "aria-labelledby", id)
		if section == nil || accountElement(t, section, "h2", "id", id) == nil || accountElement(t, section, "a", "href", "http://shop.test/shop") == nil {
			t.Fatalf("empty %s section needs a heading and a next action", id)
		}
	}
	if accountElement(t, doc, "form", "action", "/account/password-login") != nil || accountElement(t, doc, "form", "action", "/account/billing-portal") != nil {
		t.Fatal("signed-in empty account exposes guest or unavailable billing actions")
	}
}
