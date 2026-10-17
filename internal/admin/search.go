package admin

import (
	"net/http"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
)

const (
	searchMinRunes = 2
	searchLimit    = 5
)

func goVersion() string { return runtime.Version() }

type searchHit struct{ Title, Sub, Href, Icon string }

type searchResults struct {
	Query      string
	Full       bool // full page (not the topbar dropdown)
	Content    []searchHit
	Users      []searchHit
	Pages      []searchHit
	HasContent bool
	TooShort   bool
}

func (s searchResults) Empty() bool { return len(s.Content)+len(s.Users)+len(s.Pages) == 0 }

// extraPages are searchable admin screens that have no nav item.
var extraPages = []struct{ LabelKey, Href, Icon, Perm string }{
	{"admin.settings.tab_mail", "/admin/settings/mail", "mail", PermSettings},
	{"admin.settings.tab_ai", "/admin/settings/ai", "sparkles", PermSettings},
	{"admin.users.new", "/admin/users/new", "user-plus", PermUsers},
}

// search finds content titles, users (users.manage) and admin pages by
// label for the current user.
func (h *handlers) search(r *http.Request) (searchResults, error) {
	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	res := searchResults{Query: q}
	cs, hasContent := h.contentService()
	res.HasContent = hasContent
	if utf8.RuneCountInString(q) < searchMinRunes {
		res.TooShort = q != ""
		return res, nil
	}
	if hasContent && can(r, "content.view") {
		query := content.Query{Search: q, Page: 1, PerPage: searchLimit}
		if u := auth.UserFromContext(ctx); u != nil && (u.Role == "author" || u.Role == "contributor") {
			query.AuthorID = u.ID
		}
		pg, err := cs.Query(ctx, query)
		if err != nil {
			return res, err
		}
		for _, it := range pg.Items {
			// Assumes the content admin's edit URL.
			res.Content = append(res.Content, searchHit{Title: it.Title, Sub: it.Type, Href: "/admin/content/" + it.Type + "/" + it.ID, Icon: "file-text"})
		}
	}
	if can(r, PermUsers) {
		list, _, err := listUsers(ctx, h.d.DB, userQuery{Search: q, Page: 1, PerPage: searchLimit})
		if err != nil {
			return res, err
		}
		for _, u := range list {
			res.Users = append(res.Users, searchHit{Title: u.Name, Sub: u.Email, Href: "/admin/users/" + u.ID, Icon: "user"})
		}
	}
	needle := strings.ToLower(q)
	match := func(labelKey string) (string, bool) {
		label := h.tr(r, labelKey)
		return label, strings.Contains(strings.ToLower(label), needle)
	}
	for _, n := range h.d.Admin.Nav(ctx) {
		if label, ok := match(n.LabelKey); ok {
			res.Pages = append(res.Pages, searchHit{Title: label, Href: n.Href, Icon: n.Icon})
		}
	}
	for _, p := range extraPages {
		if !can(r, p.Perm) {
			continue
		}
		if label, ok := match(p.LabelKey); ok {
			res.Pages = append(res.Pages, searchHit{Title: label, Href: p.Href, Icon: p.Icon})
		}
	}
	return res, nil
}

// searchPage is the full-page result (the topbar form's no-JS target).
func (h *handlers) searchPage(w http.ResponseWriter, r *http.Request) {
	res, err := h.search(r)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	res.Full = true
	h.tpl.Render(w, r, "pages/system/search.html", res)
}

// searchFragment is the topbar dropdown (HTMX GET /admin/x/search).
func (h *handlers) searchFragment(w http.ResponseWriter, r *http.Request) {
	res, err := h.search(r)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.tpl.RenderWith(w, r, http.StatusOK, "", "pages/system/search.html", res)
}
