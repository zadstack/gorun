package theme

import (
	"net/url"
	"slices"
	"strings"
	"unicode"

	"github.com/zadstack/gorun/internal/content"
)

// PublicAction is a navigation action, never a command or authorization grant.
// Disabled/unavailable descriptors deliberately carry no usable destination.
type PublicAction struct {
	Label   string
	URL     string
	State   string
	Reason  string
	Current bool
}

// PublicDetail exposes only a published, publicly readable item's summary.
// The destination is the item's actual locale; callers must disclose Fallback.
type PublicDetail struct {
	Title    string
	Summary  string
	URL      string
	Locale   string
	Fallback bool
}

func (f *funcs) publicUI(tc *Context) bool {
	return tc != nil && slices.Contains(tc.Theme.Supports, "public-ui")
}

// publicAction adapts a trusted template's descriptor. Capability decisions
// belong to the calling domain service, not to this presentation helper.
func (f *funcs) publicAction(in map[string]any) PublicAction {
	get := func(key string) string { value, _ := in[key].(string); return strings.TrimSpace(value) }
	a := PublicAction{Label: get("label"), State: get("state"), Reason: get("reason")}
	if a.State == "" {
		a.State = "available"
	}
	if !slices.Contains([]string{"available", "disabled", "unavailable", "requires_setup"}, a.State) {
		a.State = "unavailable"
	}
	if a.State != "available" || a.Label == "" {
		if a.State != "available" && a.Reason == "" {
			a.Reason = f.t("public_ui_unavailable")
		}
		return a
	}
	target := get("url")
	if !publicNavigationURL(target) {
		a.State = "unavailable"
		if a.Reason == "" {
			a.Reason = f.t("public_ui_unavailable")
		}
		return a
	}
	// Localize only engine-known routes. Arbitrary translated pages must be
	// resolved by destination/content/linker rather than inventing /ar/ URLs.
	if strings.HasPrefix(target, "#") {
		a.URL = target
	} else {
		a.URL = f.url(target)
	}
	return a
}

func publicNavigationURL(raw string) bool {
	if raw == "" || strings.Contains(raw, "\\") || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || strings.Contains(u.Path, "\\") || strings.IndexFunc(u.Path, unicode.IsControl) >= 0 {
		return false
	}
	// Only root-relative paths, same-document fragments and explicit web URLs.
	// Protocol-relative destinations and encoded redirect-like authorities fail.
	if u.Scheme == "" {
		return (strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") && !strings.HasPrefix(u.Path, "//")) || (strings.HasPrefix(raw, "#") && u.Fragment != "")
	}
	return (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.Opaque == ""
}

func (f *funcs) publicDetail(it *content.Item) *PublicDetail {
	if !f.published(it) {
		return nil
	}
	locale := f.itemLocale(it)
	return &PublicDetail{Title: it.Title, Summary: excerpt(it), URL: f.url(it), Locale: locale, Fallback: locale != f.pageLocale()}
}

// PublicNavigation is a flat, resolved top-level menu for contextual navigation.
// Children remain available through the existing menu helper for full site menus.
type PublicNavigation struct {
	Label string
	Links []PublicAction
}

func (f *funcs) publicNavigation(location string) PublicNavigation {
	out := PublicNavigation{Label: f.t("public_ui_context")}
	menu := f.menu(location)
	if menu == nil {
		return out
	}
	for _, item := range menu.Items {
		action := f.publicAction(map[string]any{"label": item.Label, "url": item.URL})
		if action.URL != "" {
			if target, err := url.Parse(action.URL); err == nil && f.st.tc.Request != nil {
				base, _ := url.Parse(f.st.tc.Site.BaseURL)
				action.Current = (target.Host == "" || target.Host == base.Host) && target.Path == f.st.tc.Request.URL.Path
			}
			out.Links = append(out.Links, action)
		}
	}
	return out
}

// publicReturn preserves an explicit local discovery URL, including its query
// and fragment. Without one, the known localized search route is the fallback.
func (f *funcs) publicReturn(raw string) PublicAction {
	if !strings.HasPrefix(raw, "/") || !publicNavigationURL(raw) {
		raw = "/search"
	}
	return f.publicAction(map[string]any{"label": f.t("back"), "url": raw})
}
