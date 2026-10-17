package site

import (
	"context"
	"net/url"
	"strings"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/i18n"
	"golang.org/x/net/html"
)

type formSource struct {
	Title, Path, Locale, Kind string
	Enabled                   bool
}

// Source paths deliberately exclude arbitrary parameters and private/admin routes.
func formSourcePath(raw string) string {
	if !localPath(raw) {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	p := u.Path
	if !localPath(p) || strings.ContainsAny(p, "\\\x00\r\n") {
		return ""
	}
	_, rest := i18n.SplitLocalePath(p)
	for _, prefix := range []string{"/admin", "/api", "/forms", "/start"} {
		if rest == prefix || strings.HasPrefix(rest, prefix+"/") {
			return ""
		}
	}
	return p
}

func contactContent(it content.Item) bool {
	if it.Template == "contact" || it.Template == "page-contact" || (it.Template == "" && it.Type == "page" && it.Slug == "contact") {
		return true
	}
	z := html.NewTokenizer(strings.NewReader(it.Body))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return false
		case html.StartTagToken, html.SelfClosingTagToken:
			t := z.Token()
			var class, action string
			for _, a := range t.Attr {
				if a.Key == "class" {
					class = a.Val
				}
				if a.Key == "action" {
					action = a.Val
				}
			}
			if (it.Template == "builder" && t.Data == "section" && strings.Contains(" "+class+" ", " gp-contact ")) || (t.Data == "form" && action == "/forms/contact") {
				return true
			}
		}
	}
}
func (h *handlers) contactSources(ctx context.Context) ([]formSource, error) {
	out := []formSource{}
	svc, ok := h.content()
	if !ok {
		return out, nil
	}
	for page := 1; ; page++ {
		result, err := svc.Query(ctx, content.Query{Status: "published", PublicOnly: true, Page: page, PerPage: 100})
		if err != nil {
			return nil, err
		}
		for _, it := range result.Items {
			if !contactContent(it) {
				continue
			}
			typ, err := svc.GetType(ctx, it.Type)
			if err != nil {
				return nil, err
			}
			if !typ.Public || it.Status != "published" {
				continue
			}
			path := formSourcePath(h.links.Item(ctx, &it))
			if path == "" {
				continue
			}
			_, routePath := i18n.SplitLocalePath(path)
			if it.Type == "page" && routePath != "/" {
				parent := it.ParentID
				valid := true
				for depth := 0; parent != ""; depth++ {
					if depth >= maxPathSegments-1 {
						valid = false
						break
					}
					p, err := svc.Get(ctx, parent)
					if err != nil || p.Status != "published" || p.Type != "page" {
						valid = false
						break
					}
					parent = p.ParentID
				}
				if !valid {
					continue
				}
			}
			locale := it.Locale
			if locale == "" {
				locale = h.siteLocaleOf(ctx)
			}
			out = append(out, formSource{Title: it.Title, Path: path, Locale: locale, Kind: "contact", Enabled: true})
		}
		if page*result.PerPage >= result.Total || len(result.Items) == 0 {
			break
		}
	}
	return out, nil
}
func resolveFormSource(raw string, sources []formSource) formSource {
	p := formSourcePath(raw)
	if p == "" {
		return formSource{}
	}
	for _, source := range sources {
		if source.Path == p {
			return source
		}
	}
	return formSource{}
}
func (h *handlers) intakeSources(ctx context.Context, enabled bool) []formSource {
	out := []formSource{}
	for _, loc := range i18n.Supported {
		for _, kind := range []string{"incident", "follow-up"} {
			out = append(out, formSource{Path: i18n.LocalizePath(loc, h.siteLocaleOf(ctx), "/reports/"+kind), Locale: loc, Kind: kind, Enabled: enabled})
		}
	}
	return out
}
func (h *handlers) intakeSource(ctx context.Context, raw, kind string) formSource {
	for _, source := range h.intakeSources(ctx, true) {
		if source.Path == raw && source.Kind == kind {
			return source
		}
	}
	return formSource{}
}
