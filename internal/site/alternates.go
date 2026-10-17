package site

import (
	"github.com/zadstack/gorun/internal/content"
	"net/http"
	"net/url"

	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/theme"
)

// alternates is the page in every locale it exists in. An item lists its published
// translations (never a draft: a public link must not 404); any other page (home, archives,
// terms, search) is the same path in each supported locale. One locale only is no alternate.
func (h *handlers) alternates(r *http.Request, tc *theme.Context) []theme.Alternate {
	ctx := r.Context()
	base := kernel.BaseURL(ctx, h.d)
	site := h.siteLocaleOf(ctx)
	var out []theme.Alternate
	add := func(loc, path string) {
		out = append(out, theme.Alternate{Locale: loc, Label: i18n.NativeNames[loc], URL: base + path})
	}
	if it := tc.Content; it != nil {
		svc, ok := h.content()
		if !ok || it.TranslationGroup == "" {
			return nil
		}
		members, err := svc.Translations(ctx, it.ID)
		if err != nil {
			h.d.Log.Error("site: translations", "id", it.ID, "err", err)
			return nil
		}
		links := theme.NewLinker(h.d)
		for i := range members {
			if m := &members[i]; m.Status == "published" && m.Locale != "" && content.PubliclyReadable(ctx, svc, m.ID) {
				add(m.Locale, links.Item(ctx, m))
			}
		}
	} else {
		path := r.URL.Path
		if q := r.URL.Query().Get("q"); q != "" {
			path += "?" + url.Values{"q": {q}}.Encode()
		}
		for _, loc := range i18n.Supported {
			add(loc, i18n.LocalizePath(loc, site, path))
		}
	}
	if len(out) < 2 {
		return nil
	}
	return out
}
