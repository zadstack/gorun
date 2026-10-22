package membership

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
	"html/template"
	"io/fs"
	"net/http"
)

//go:embed migrations/*.sql catalog/*.json
var migrations embed.FS

type Module struct{ Service *Service }

func New() *Module                { return &Module{} }
func (*Module) Name() string      { return "membership" }
func (*Module) Migrations() fs.FS { return migrations }
func (*Module) Catalogs() fs.FS   { return migrations }

// Enabled implements kernel.OptionalModule (addons.membership in the config
// or the addons.membership.enabled setting; on by default). Grants, plans and
// submissions are kept while the module is off.
func (*Module) Enabled(ctx context.Context, d *sql.DB) (bool, error) {
	return kernel.AddonEnabled(ctx, d, "membership")
}

func (m *Module) Register(d *kernel.Deps) error {
	m.Service = NewService(d.DB)
	m.Service.Audit = d.Audit
	s := m.Service
	d.Router.Public.Get("/membership/assets.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write([]byte(httpx.AccountPageCSS))
	})
	d.Services.Set("membership", s)
	d.Admin.AddNav(kernel.NavItem{ID: "membership", LabelKey: "membership.nav", Icon: "badge-check", Href: "/admin/membership", Permission: PermManage, Group: "commerce", Position: 46})
	d.Perms.Declare(PermManage, map[string]string{"ar": "إدارة العضويات", "en": "Manage memberships"})
	d.Perms.Declare("membership.read", map[string]string{"ar": "عرض العضوية", "en": "Read own membership"})
	d.Perms.Declare("membership.unlimited", map[string]string{"ar": "تجاوز حصص العضوية", "en": "Bypass membership quotas"})
	d.Hooks.Filter("content.authorize_save", 50, s.AuthorizeSave)
	if e := auth.ReloadRoles(context.Background(), d.DB); e != nil {
		return e
	}
	registerSubmissions(d, s)
	overview := func(all bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "private, no-store")
			o, e := s.Overview(r.Context(), all)
			if e != nil {
				fail(w, r, e)
				return
			}
			api.WriteItem(w, o)
		}
	}
	d.API.Add(api.Op("GET", "/v1/membership", "Read own membership, effective expiry and content quota", []string{"membership"}, nil, nil, Overview{}, overview(false)))
	d.API.Add(api.Op("GET", "/v1/membership/manage", "Read all memberships", []string{"membership"}, []string{PermManage}, nil, Overview{}, overview(true)))
	d.API.Add(api.Op("POST", "/v1/membership/plans", "Save explicit membership plan limits", []string{"membership"}, []string{PermManage}, Plan{}, Plan{}, func(w http.ResponseWriter, r *http.Request) {
		var p Plan
		if e := api.Decode(w, r, &p); e != nil {
			api.Fail(w, r, e)
			return
		}
		if e := s.SavePlan(r.Context(), p); e != nil {
			fail(w, r, e)
			return
		}
		api.WriteItem(w, p)
	}))
	save := func(w http.ResponseWriter, r *http.Request) {
		var q GrantInput
		if e := api.Decode(w, r, &q); e != nil {
			api.Fail(w, r, e)
			return
		}
		if id := chi.URLParam(r, "id"); id != "" {
			if q.ID != "" && q.ID != id {
				fail(w, r, ErrInvalid)
				return
			}
			q.ID = id
		} else if q.ID != "" {
			fail(w, r, ErrInvalid)
			return
		}
		g, e := s.SaveGrant(r.Context(), q)
		if e != nil {
			fail(w, r, e)
			return
		}
		api.WriteItem(w, g)
	}
	d.API.Add(api.Op("POST", "/v1/membership/grants", "Create explicit local membership; never charges or renews payment", []string{"membership"}, []string{PermManage}, GrantInput{}, Grant{}, save))
	update := api.Op("PUT", "/v1/membership/grants/{id}", "Update membership with expected version and immutable owner", []string{"membership"}, []string{PermManage}, GrantInput{}, Grant{}, save)
	update.Params = []kernel.APIParam{{Name: "id", In: "path", Required: true, Type: "string"}}
	d.API.Add(update)
	page := func(all bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "private, no-store")
			_, commerceOn := kernel.Get[any](d, kernel.ServiceCommerce)
			_, saasOn := kernel.Get[any](d, "saas")
			_, workAgentsOn := kernel.Get[any](d, "workagents")
			if actor(r.Context()) == "" {
				if !commerceOn {
					httpx.AccountSignInUnavailable(w)
					return
				}
				http.Redirect(w, r, "/account?next=/membership", 303)
				return
			}
			o, e := s.Overview(r.Context(), all)
			if e != nil {
				fail(w, r, e)
				return
			}
			subs, e := s.Submissions(r.Context())
			if e != nil {
				fail(w, r, e)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = pageTemplate.Execute(w, struct {
				Data        Overview
				Manage      bool
				CSRF        string
				Submissions []Submission
				CanSubmit   bool
				Commerce    bool
				SaaS        bool
				WorkAgents  bool
			}{o, all, auth.CSRFToken(r.Context()), subs, auth.Can(r.Context(), "membership.submit"), commerceOn, saasOn, workAgentsOn})
		}
	}
	d.Router.Public.Get("/membership", page(false))
	admin := d.Router.Admin.With(auth.RequirePerm(PermManage), auth.CSRFMiddleware())
	admin.Get("/membership", page(true))
	admin.Post("/membership/grants", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		if e := r.ParseForm(); e != nil {
			fail(w, r, ErrInvalid)
			return
		}
		q := GrantInput{Grant: Grant{ID: r.Form.Get("id"), OwnerID: r.Form.Get("owner_id"), PlanID: r.Form.Get("plan_id"), Status: r.Form.Get("status"), PeriodStart: r.Form.Get("period_start"), PeriodEnd: r.Form.Get("period_end"), GraceUntil: r.Form.Get("grace_until"), CancelAtPeriodEnd: r.Form.Get("cancel_at_period_end") == "on"}, ExpectedVersion: r.Form.Get("expected_version")}
		if _, e := s.SaveGrant(r.Context(), q); e != nil {
			fail(w, r, e)
			return
		}
		http.Redirect(w, r, "/admin/membership", 303)
	})
	return nil
}
func fail(w http.ResponseWriter, r *http.Request, e error) {
	switch {
	case errors.Is(e, content.ErrForbidden):
		api.WriteError(w, 403, "forbidden", "membership not available", nil)
	case errors.Is(e, ErrInvalid):
		api.WriteError(w, 422, "validation_failed", "invalid membership configuration", nil)
	case errors.Is(e, ErrConflict):
		api.WriteError(w, 409, "conflict", "membership changed; reload before saving", nil)
	default:
		var validation *content.ValidationError
		if errors.As(e, &validation) {
			api.WriteError(w, 422, "validation_failed", "content could not be submitted; check required fields and membership quota", validation.Fields)
			return
		}
		api.Fail(w, r, e)
	}
}

var pageTemplate = template.Must(template.New("membership").Parse(`<!doctype html><html lang="ar" dir="rtl"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>العضوية</title><link rel="stylesheet" href="/membership/assets.css"></head><body><main><nav><a href="/">الموقع</a>{{if .WorkAgents}} · <a href="/agents">الإيجنتس</a>{{end}}{{if .Commerce}} · <a href="/account">حسابي</a> · <a href="/shop">المتجر</a>{{end}}{{if .SaaS}} · <a href="/saas">تطبيقاتي</a>{{end}}{{if .Manage}} · <a href="/admin">الإدارة</a>{{end}}</nav><h1>العضويات والحصص</h1><p>تظهر الحالة الفعلية حسب مدة العضوية وفترة السماح. لا يوجد تجديد أو خصم مالي تلقائي. الإلغاء يوقف التجديد؛ التعليق يوقف الوصول فورًا.</p>{{range .Data.Grants}}<article><h2>{{.PlanID}}</h2><p>الحالة: {{.EffectiveStatus}} · الحالة المسجلة: {{.Status}}</p><p>من {{.PeriodStart}} إلى {{.PeriodEnd}} · السماح حتى {{.GraceUntil}}</p>{{if .CancelAtPeriodEnd}}<p>التجديد ملغي في نهاية المدة.</p>{{end}}{{if $.Manage}}<details><summary>إدارة العضوية · {{.OwnerID}}</summary><form method="post" action="/admin/membership/grants"><input type="hidden" name="_csrf" value="{{$.CSRF}}"><input type="hidden" name="id" value="{{.ID}}"><input type="hidden" name="owner_id" value="{{.OwnerID}}"><input type="hidden" name="expected_version" value="{{.Version}}"><label>الخطة<input name="plan_id" value="{{.PlanID}}" required></label><label>الحالة<select name="status"><option value="{{.Status}}">{{.Status}}</option><option>active</option><option>pending-renewal</option><option>cancelled</option><option>expired</option><option>suspended</option></select></label><label>بداية المدة UTC<input name="period_start" value="{{.PeriodStart}}"></label><label>نهاية المدة UTC<input name="period_end" value="{{.PeriodEnd}}"></label><label>نهاية السماح UTC<input name="grace_until" value="{{.GraceUntil}}"></label><label><input type="checkbox" name="cancel_at_period_end" {{if .CancelAtPeriodEnd}}checked{{end}}>إيقاف التجديد</label><button>حفظ</button></form></details>{{end}}</article>{{else}}<p>لا توجد عضوية مدفوعة؛ تُطبّق حدود الحساب المجاني.</p>{{end}}{{if .CanSubmit}}<h2>إرسال محتوى للمراجعة</h2><form method="post" action="/membership/submissions"><input type="hidden" name="_csrf" value="{{.CSRF}}"><label>النوع<select name="type" required>{{range .Data.Quotas}}<option value="{{.Type}}">{{.Type}}</option>{{end}}</select></label><label>العنوان<input name="title" required maxlength="500"></label><label>التفاصيل<textarea name="body" rows="6" maxlength="131072" required></textarea></label><button>إرسال للمراجعة</button></form><p>يُحفظ بحسابك وحالة انتظار المراجعة؛ لا يُنشر تلقائيًا.</p>{{end}}<h2>مساهماتي</h2>{{range .Submissions}}<details id="submission-{{.ID}}"><summary><a href="#submission-{{.ID}}">{{.Title}}</a> · {{.Type}} · {{.Status}}</summary><pre>{{.Body}}</pre></details>{{else}}<p>لا توجد مساهمات لهذا الحساب.</p>{{end}}<h2>حصص حسابك</h2><table><thead><tr><th>نوع المحتوى</th><th>الاستخدام</th><th>الحد</th></tr></thead><tbody>{{range .Data.Quotas}}<tr><td>{{.Type}}</td><td>{{.Used}}</td><td>{{if .Unlimited}}غير محدود بصلاحية صريحة{{else}}{{.Limit}}{{end}}</td></tr>{{end}}</tbody></table><p>تشمل الحصة جميع الحالات ما عدا المحذوفات. يبقى تعديل المحتوى الموجود متاحًا عند بلوغ الحصة.</p></main></body></html>`))
