package saas

import (
	"context"
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

//go:embed migrations/*.sql
var migrations embed.FS

//go:embed catalog/*.json
var catalogs embed.FS

// Module adds owner-scoped SaaS state. Import and external protocol adapters are separate concerns.
type Module struct{ Service *Service }

func New() *Module                { return &Module{} }
func (*Module) Name() string      { return "saas" }
func (*Module) Migrations() fs.FS { return migrations }
func (*Module) Catalogs() fs.FS   { return catalogs }
func (m *Module) Register(d *kernel.Deps) error {
	d.Perms.Declare("saas.read", map[string]string{"en": "Read owned SaaS subscriptions", "ar": "عرض اشتراكات SaaS المملوكة"})
	d.Perms.Declare("saas.use", map[string]string{"en": "Use owned SaaS subscriptions", "ar": "استخدام اشتراكات SaaS المملوكة"})
	d.Perms.Declare(PermManage, map[string]string{"en": "Manage SaaS subscriptions", "ar": "إدارة تطبيقات SaaS"})
	if e := auth.ReloadRoles(context.Background(), d.DB); e != nil {
		return e
	}
	m.Service = NewService(d.DB)
	m.Service.Audit = d.Audit
	d.Hooks.Filter(admin.HealthCheckFilter, 55, m.Service.healthChecks)
	d.Services.Set("saas", m.Service)
	adminTpl, _ := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	m.Service.adminTpl = adminTpl
	h := &handlers{s: m.Service}
	d.Router.Public.Get("/saas", h.page(false))
	d.Router.Public.Get("/saas/assets.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Write([]byte(pageCSS))
	})
	d.Router.Admin.With(auth.RequirePerm(PermManage)).Get("/saas", h.page(true))
	d.Admin.AddNav(kernel.NavItem{ID: "saas", LabelKey: "saas.nav", Icon: "boxes", Href: "/admin/saas", Permission: PermManage, Group: "commerce", Position: 45})
	for _, management := range []bool{false, true} {
		path, summary := "/v1/saas", "Read owned apps, subscriptions and usage"
		var scopes []string
		if management {
			path, summary, scopes = "/v1/saas/manage", "Read all apps, subscriptions and usage", []string{PermManage}
		}
		op := api.Op("GET", path, summary, []string{"saas"}, scopes, nil, Overview{}, h.overview(management))
		op.Params = []kernel.APIParam{{Name: "renewal_state", In: "query", Type: "string", Description: "Optional expiry filter on the overview snapshot (up to 500 subscriptions): needs_renewal (due_soon or expired), due_soon, expired, current, unscheduled, invalid. Returns matching subscriptions and related apps/usage only. Does not change access status. Unknown or repeated values return 422. Example: needs_renewal."}}
		d.API.Add(op)
	}
	consume := api.Op("POST", "/v1/saas/entitlements/{id}/consume", "Consume owned subscription quota atomically", []string{"saas"}, nil, ConsumeRequest{}, Consumption{}, h.consume)
	consume.Params = []kernel.APIParam{{Name: "id", In: "path", Type: "string", Required: true}}
	d.API.Add(consume)
	patch := api.Op("PATCH", "/v1/saas/entitlements/{id}", "Update subscription state with optimistic concurrency", []string{"saas"}, []string{PermManage}, StatusUpdate{}, StatusUpdate{}, h.update)
	patch.Params = consume.Params
	d.API.Add(patch)
	registerManagement(d, h)
	return nil
}

type handlers struct{ s *Service }

func fail(w http.ResponseWriter, r *http.Request, e error) {
	switch {
	case errors.Is(e, ErrForbidden):
		api.WriteError(w, 403, "forbidden", "subscription not available to this account", nil)
	case errors.Is(e, ErrInvalid):
		api.WriteError(w, 422, "validation_failed", "invalid SaaS request", nil)
	case errors.Is(e, ErrConflict):
		api.WriteError(w, 409, "conflict", "request conflicts with stored state", nil)
	case errors.Is(e, ErrInactive):
		api.WriteError(w, 409, "inactive", "subscription inactive or expired", nil)
	case errors.Is(e, ErrQuota):
		api.WriteError(w, 409, "quota_exceeded", "quota unavailable or exceeded", nil)
	default:
		api.Fail(w, r, e)
	}
}
func (h *handlers) overview(management bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		o, e := h.s.Overview(r.Context(), management)
		if e != nil {
			fail(w, r, e)
			return
		}
		values := r.URL.Query()["renewal_state"]
		state := r.URL.Query().Get("renewal_state")
		if len(values) > 1 || !validRenewalFilter(state) {
			api.WriteError(w, 422, "validation_failed", "invalid renewal_state filter", nil)
			return
		}
		api.WriteItem(w, filterOverviewRenewal(o, state))
	}
}
func (h *handlers) consume(w http.ResponseWriter, r *http.Request) {
	var q ConsumeRequest
	if e := api.Decode(w, r, &q); e != nil {
		api.Fail(w, r, e)
		return
	}
	v, e := h.s.Consume(r.Context(), chi.URLParam(r, "id"), q)
	if e != nil {
		fail(w, r, e)
		return
	}
	api.WriteItem(w, v)
}
func (h *handlers) update(w http.ResponseWriter, r *http.Request) {
	var q StatusUpdate
	if e := api.Decode(w, r, &q); e != nil {
		api.Fail(w, r, e)
		return
	}
	if e := h.s.UpdateStatus(r.Context(), chi.URLParam(r, "id"), q); e != nil {
		fail(w, r, e)
		return
	}
	api.WriteItem(w, q)
}

var pageTemplate = template.Must(template.New("saas").Parse(`<!doctype html><html lang="ar" dir="rtl"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>تطبيقاتي · SaaS</title><link rel="stylesheet" href="/saas/assets.css"></head><body><main><nav><a href="/account">حسابي</a> · <a href="/shop">المتجر</a> · <a href="/">الموقع</a>{{if .Manage}} · <a href="/admin">الإدارة</a> · <a href="/admin/saas/new">إضافة تطبيق أو اشتراك</a>{{end}}</nav><h1>{{if .Manage}}إدارة التطبيقات والاشتراكات{{else}}تطبيقاتي واشتراكاتي{{end}}</h1><p>الاستخدام والصلاحيات مرتبطة بحسابك. الاشتراكات تعمل محليًا بمفاتيح GoRun وصلاحيات محددة.</p><h2>التطبيقات</h2>{{range .Data.Apps}}<article><h3>{{.Name}}</h3><p>{{.Runtime}} · {{.Framework}} · {{.Status}}</p><dl><dt>المعرّف</dt><dd>{{.PublicID}}</dd><dt>آخر ظهور</dt><dd>{{.LastSeenAt}}</dd><dt>نمط الخدمة</dt><dd>{{if eq .Mode "subscription"}}اشتراكات واستهلاك محلي{{else}}{{if .FulfillmentEnabled}}تشغيل خارجي مفعّل{{else}}تشغيل خارجي غير مهيّأ{{end}}{{end}}</dd></dl>{{if $.Manage}}<details><summary>تعديل التطبيق</summary><form method="post" action="/admin/saas/apps/{{.ID}}"><input type="hidden" name="_csrf" value="{{$.CSRF}}"><input type="hidden" name="expected_version" value="{{.Version}}"><input type="hidden" name="owner_id" value="{{.OwnerID}}"><input type="hidden" name="mode" value="{{.Mode}}"><label>الاسم<input name="name" value="{{.Name}}" required></label><label>slug<input name="slug" value="{{.Slug}}" required></label><label>الرابط<input name="base_url" value="{{.BaseURL}}"></label><label>الحالة<select name="status"><option value="{{.Status}}">{{.Status}}</option><option value="active">نشط</option><option value="disabled">متوقف</option></select></label><button>حفظ التطبيق</button></form></details>{{end}}</article>{{else}}<p>لا توجد تطبيقات مرتبطة بحسابك. <a href="/shop">استعرض الخدمات</a>.</p>{{end}}<h2>الاشتراكات</h2><div class="scroll"><table><thead><tr><th>التطبيق</th><th>الخطة</th><th>الحالة</th><th>حتى</th><th>المزايا</th><th>الحدود</th>{{if .Manage}}<th>إدارة الحالة</th>{{end}}</tr></thead><tbody>{{range .Data.Entitlements}}<tr><td>{{.AppName}}</td><td>{{.PlanID}}</td><td>{{.Status}}</td><td>{{.ValidUntil}}</td><td><pre>{{printf "%s" .Features}}</pre></td><td><pre>{{printf "%s" .Limits}}</pre></td>{{if $.Manage}}<td><form method="post" action="/admin/saas/subscriptions/{{.ID}}/status"><input type="hidden" name="_csrf" value="{{$.CSRF}}"><input type="hidden" name="expected_status" value="{{.Status}}"><select name="status"><option value="active">نشط</option><option value="suspended">معلق</option><option value="expired">منتهي</option><option value="revoked">ملغي</option></select><button>حفظ</button></form></td>{{end}}</tr>{{else}}<tr><td colspan="6">لا توجد اشتراكات لهذا الحساب.</td></tr>{{end}}</tbody></table></div><h2>الاستهلاك</h2><table><thead><tr><th>التطبيق</th><th>المقياس</th><th>الكمية</th></tr></thead><tbody>{{range .Data.Usage}}<tr><td>{{.AppID}}</td><td>{{.Metric}}</td><td>{{.Quantity}}</td></tr>{{else}}<tr><td colspan="3">لم يسجّل استخدام بعد.</td></tr>{{end}}</tbody></table></main></body></html>`))

const pageCSS = `body{margin:0;background:#f7f8fa;color:#172b26;font:16px system-ui,sans-serif}main{max-width:1100px;margin:auto;padding:28px}nav{border-bottom:1px solid #d9e2dc;padding-bottom:14px}a{color:#176d50}h1{font-size:28px}h2{font-size:20px;margin-top:32px}article{background:white;border:1px solid #d9e2dc;border-radius:8px;padding:16px;margin:12px 0}table{width:100%;border-collapse:collapse;background:white}td,th{padding:12px;text-align:start;border-bottom:1px solid #d9e2dc}th{background:#eaf1ee}dl{display:grid;grid-template-columns:160px 1fr}dd{margin:0}pre{font-size:12px;max-width:240px;overflow:auto;direction:ltr}.scroll{overflow:auto}label{display:block;margin:12px 0}input,select,button{font:inherit;padding:8px;border:1px solid #cbd8d0;border-radius:5px;margin:4px}button{background:#176d50;color:white;cursor:pointer}`

func (h *handlers) page(management bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		if auth.UserFromContext(r.Context()) == nil {
			http.Redirect(w, r, "/account?next=/saas", 303)
			return
		}
		o, e := h.s.Overview(r.Context(), management)
		if e != nil {
			fail(w, r, e)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data := struct {
			Manage bool
			Data   Overview
			CSRF   string
		}{management, o, auth.CSRFToken(r.Context())}
		if management && h.s.adminTpl != nil {
			h.s.adminTpl.Render(w, r, "pages/saas/index.html", data)
			return
		}
		_ = pageTemplate.Execute(w, data)
	}
}
