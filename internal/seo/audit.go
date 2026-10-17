package seo

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"golang.org/x/net/html"
)

type auditRow struct {
	ID, Type, Title, Locale                                                                           string
	MissingTitle, MissingDescription, DuplicateTitle, DuplicateDescription, InvalidCanonical, NoIndex bool
	MissingAlt                                                                                        int
	Issues                                                                                            []string
}
type auditData struct {
	Rows                                                              []auditRow
	Total, Scanned, Attention, NoIndex, MissingDescription, Duplicate int
	At                                                                string
	Limited                                                           bool
}

func auditItems(items []content.Item) []auditRow {
	titles, descriptions := map[string]int{}, map[string]int{}
	key := func(locale, value string) string {
		return locale + "\x00" + strings.ToLower(strings.Join(strings.Fields(value), " "))
	}
	for _, it := range items {
		m := FromMap(it.SEO)
		title := first(m.Title, it.Title)
		desc := first(m.Description, it.Excerpt)
		if title != "" {
			titles[key(it.Locale, title)]++
		}
		if desc != "" {
			descriptions[key(it.Locale, desc)]++
		}
	}
	rows := make([]auditRow, 0, len(items))
	for _, it := range items {
		m := FromMap(it.SEO)
		title := first(m.Title, it.Title)
		desc := first(m.Description, it.Excerpt)
		row := auditRow{ID: it.ID, Type: it.Type, Title: it.Title, Locale: it.Locale, MissingTitle: title == "", MissingDescription: desc == "", NoIndex: noindex(&it), DuplicateTitle: title != "" && titles[key(it.Locale, title)] > 1, DuplicateDescription: desc != "" && descriptions[key(it.Locale, desc)] > 1}
		if m.Canonical != "" {
			u, e := url.Parse(m.Canonical)
			row.InvalidCanonical = e != nil || u == nil || strings.ContainsAny(m.Canonical, "\\\r\n\t ") || u.Fragment != "" || u.User != nil || !(strings.HasPrefix(m.Canonical, "/") && !strings.HasPrefix(m.Canonical, "//") || (u.Scheme == "http" || u.Scheme == "https") && u.Host != "")
		}
		tokens := html.NewTokenizer(strings.NewReader(it.Body))
		for {
			kind := tokens.Next()
			if kind == html.ErrorToken {
				if tokens.Err() == io.EOF {
					break
				}
				break
			}
			if kind != html.StartTagToken && kind != html.SelfClosingTagToken {
				continue
			}
			token := tokens.Token()
			if token.Data != "img" {
				continue
			}
			hasAlt := false
			for _, attr := range token.Attr {
				if attr.Key == "alt" {
					hasAlt = true
					break
				}
			}
			if !hasAlt {
				row.MissingAlt++
			}
		}
		for _, flag := range []struct {
			on  bool
			key string
		}{{row.MissingTitle, "missing_title"}, {row.MissingDescription, "description_fallback"}, {row.DuplicateTitle, "duplicate_title"}, {row.DuplicateDescription, "duplicate_description"}, {row.InvalidCanonical, "invalid_canonical"}, {row.MissingAlt > 0, "missing_alt"}} {
			if flag.on {
				row.Issues = append(row.Issues, "seoaudit."+flag.key)
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func (h *settingsHandlers) audit(w http.ResponseWriter, r *http.Request) {
	if !auth.Can(r.Context(), "content.view") {
		h.tpl.RenderError(w, r, http.StatusForbidden)
		return
	}
	report := auditData{At: time.Now().UTC().Format(time.RFC3339)}
	svc, ok := h.svc.content()
	if ok {
		var items []content.Item
		for page := 1; page <= 50; page++ {
			result, err := svc.Query(r.Context(), content.Query{Status: "published", PublicOnly: true, Page: page, PerPage: 100, Order: []content.Order{{By: "created_at", Dir: "asc"}}})
			if err != nil {
				h.deps.Log.Error("seo audit query", "err", err)
				h.tpl.RenderError(w, r, 500)
				return
			}
			report.Total = result.Total
			items = append(items, result.Items...)
			if len(result.Items) < 100 || len(items) >= result.Total {
				break
			}
		}
		report.Rows = auditItems(items)
		report.Scanned = len(items)
		report.Limited = report.Scanned < report.Total
		for _, row := range report.Rows {
			if len(row.Issues) > 0 {
				report.Attention++
			}
			if row.NoIndex {
				report.NoIndex++
			}
			if row.MissingDescription {
				report.MissingDescription++
			}
			if row.DuplicateTitle || row.DuplicateDescription {
				report.Duplicate++
			}
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	h.tpl.Render(w, r, "pages/settings/seo-audit.html", report)
}
