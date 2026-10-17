package site

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

type pageAccessAdmin struct {
	h   *handlers
	tpl *httpx.Templates
}
type pageAccessView struct {
	Template                                      string
	Templates                                     []string
	AR, Selected, PasswordSet, PlatformRestricted bool
	Items                                         []content.Item
	ID, Title, Mode, Message, Error, Search       string
	Revision                                      int64
}

func registerPageAccessAdmin(d *kernel.Deps, h *handlers) {
	tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	if !ok {
		return
	}
	a := &pageAccessAdmin{h, tpl}
	d.Admin.AddNav(kernel.NavItem{ID: "site.page_access", LabelKey: "pageaccess.nav", Href: "/admin/settings/page-access", Icon: "shield", Permission: "settings.manage", Group: "settings", Position: 731})
	r := d.Router.Admin.With(auth.RequirePerm("settings.manage"))
	r.Get("/settings/page-access", a.show)
	r.Post("/settings/page-access", a.save)
}
func (a *pageAccessAdmin) render(w http.ResponseWriter, r *http.Request, status int, problem string, submitted *pageAccessView) {
	svc, ok := a.h.content()
	if !ok {
		http.Error(w, "Content unavailable", 503)
		return
	}
	policies, ok := svc.(content.PageAccessService)
	if !ok {
		http.Error(w, "Page access unavailable", 503)
		return
	}
	f := pageAccessView{AR: i18n.Locale(r.Context()) == "ar", Search: r.URL.Query().Get("q"), ID: r.URL.Query().Get("id"), Error: problem, Template: r.URL.Query().Get("template")}
	if len(f.Template) > 100 {
		http.Error(w, "Invalid template", 400)
		return
	}
	if len(f.Search) > 200 {
		f.Search = f.Search[:200]
	}
	types, e := svc.Types(r.Context())
	if e != nil {
		http.Error(w, "Unable to read content types", 500)
		return
	}
	for _, typ := range types {
		if !typ.Public {
			continue
		}
		for _, name := range typ.Templates {
			if name != "" && !slices.Contains(f.Templates, name) {
				f.Templates = append(f.Templates, name)
			}
		}
		res, e := svc.Query(r.Context(), content.Query{Type: typ.Slug, Template: f.Template, Search: f.Search, Status: "published,draft", PerPage: 20})
		if e != nil {
			http.Error(w, "Unable to read pages", 500)
			return
		}
		for _, it := range res.Items {
			if it.Template != "" && !slices.Contains(f.Templates, it.Template) {
				f.Templates = append(f.Templates, it.Template)
			}
		}
		f.Items = append(f.Items, res.Items...)
		if len(f.Items) >= 100 {
			f.Items = f.Items[:100]
			break
		}
	}
	slices.Sort(f.Templates)
	if f.ID != "" {
		it, e := svc.Get(r.Context(), f.ID)
		if e != nil || it.Status == "trash" {
			http.NotFound(w, r)
			return
		}
		typ, e := svc.GetType(r.Context(), it.Type)
		if e != nil || !typ.Public {
			http.NotFound(w, r)
			return
		}
		p, e := policies.PageAccess(r.Context(), it.ID)
		if e != nil {
			http.Error(w, "Unable to read page access", 500)
			return
		}
		f.Selected = true
		f.Title = it.Title
		f.Mode = p.Mode
		f.Message = p.Message
		f.Revision = p.Revision
		f.PasswordSet = p.PasswordHash != ""
		platform, e := platformPolicy()
		f.PlatformRestricted = e != nil || platformPageRestricted(platform, f.ID)
	}
	if submitted != nil {
		f.Mode = submitted.Mode
		f.Message = submitted.Message
		f.Revision = submitted.Revision
	}
	w.Header().Set("Cache-Control", "private, no-store")
	a.tpl.RenderWith(w, r, status, "admin", "pages/settings/page-access.html", f)
}
func (a *pageAccessAdmin) show(w http.ResponseWriter, r *http.Request) { a.render(w, r, 200, "", nil) }
func (a *pageAccessAdmin) save(w http.ResponseWriter, r *http.Request) {
	if e := r.ParseForm(); e != nil {
		http.Error(w, "Invalid form", 400)
		return
	}
	id := r.FormValue("content_id")
	q := r.URL.Query()
	q.Set("id", id)
	r.URL.RawQuery = q.Encode()
	rev, e := strconv.ParseInt(r.FormValue("revision"), 10, 64)
	f := &pageAccessView{Mode: r.FormValue("mode"), Message: r.FormValue("message"), Revision: rev}
	if e != nil {
		a.render(w, r, 409, "Reload before saving. / حدّث الصفحة قبل الحفظ.", f)
		return
	}
	svc, ok := a.h.content()
	if !ok {
		http.Error(w, "Unavailable", 503)
		return
	}
	policies, ok := svc.(content.PageAccessService)
	if !ok {
		http.Error(w, "Unavailable", 503)
		return
	}
	_, e = policies.SavePageAccess(r.Context(), id, content.PageAccessInput{Mode: f.Mode, Message: f.Message, Password: r.FormValue("password"), ClearPassword: r.FormValue("clear_password") == "1"}, rev)
	if e != nil {
		status := 422
		problem := "Check mode, message and password (12–256 bytes). / راجع الوضع والرسالة وكلمة المرور."
		if errors.Is(e, content.ErrConflict) {
			status = 409
			problem = "Changed elsewhere. Reload before saving. / تغيرت الإعدادات في مكان آخر. حدّث الصفحة قبل الحفظ."
		}
		if errors.Is(e, content.ErrForbidden) {
			status = 403
		}
		if errors.Is(e, content.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		a.render(w, r, status, problem, f)
		return
	}
	http.Redirect(w, r, "/admin/settings/page-access?id="+url.QueryEscape(id), 303)
}

func platformPageRestricted(platform Availability, id string) bool {
	if platform.Mode == "blocked" || platform.Mode == "password" {
		return true
	}
	for _, rule := range platform.PageRules {
		if strings.EqualFold(rule.ContentID, id) && rule.Mode != "public" {
			return true
		}
	}
	return false
}
