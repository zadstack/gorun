package saas

import (
	"html/template"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

func registerManagement(d *kernel.Deps, h *handlers) {
	tags := []string{"saas"}
	manage := []string{PermManage}
	d.API.Add(api.Op("POST", "/v1/saas/apps", "Create owned native application", tags, manage, AppInput{}, Saved{}, h.createApp))
	update := api.Op("PATCH", "/v1/saas/apps/{id}", "Update app with expected version", tags, manage, AppInput{}, Saved{}, h.updateApp)
	update.Params = []kernel.APIParam{{Name: "id", In: "path", Required: true, Type: "string"}}
	d.API.Add(update)
	for _, item := range []struct{ path, kind string }{{"apps", "app"}, {"entitlements", "subscription"}} {
		kind := item.kind
		op := api.Op("DELETE", "/v1/saas/"+item.path+"/{id}", "Delete unused native SaaS object; preserve imported/referenced history", tags, manage, nil, Saved{}, func(w http.ResponseWriter, r *http.Request) {
			id := chi.URLParam(r, "id")
			if e := h.s.DeleteUnused(r.Context(), kind, id); e != nil {
				fail(w, r, e)
				return
			}
			api.WriteItem(w, Saved{ID: id})
		})
		op.Params = update.Params
		d.API.Add(op)
	}
	configuration := api.Op("PUT", "/v1/saas/entitlements/{id}", "Update owned plan quotas with expected version; retain usage", tags, manage, SubscriptionInput{}, Saved{}, h.updateSubscription)
	configuration.Params = update.Params
	d.API.Add(configuration)
	services := api.Op("PATCH", "/v1/saas/entitlements/{id}/services", "Edit recorded services and renew subscription; no payment or infrastructure provisioning", tags, manage, ServicesUpdate{}, Saved{}, h.updateServices)
	services.Params = update.Params
	services.Contract = &kernel.APIContract{Errors: []kernel.APIErrorResponse{{Status: 409, Code: "conflict", Description: "Stale expected_version or missing entitlement; refresh before retrying"}, {Status: 422, Code: "validation_failed", Description: "Expected version and at least one valid change required; renew_months 1 through 36"}}}
	d.API.Add(services)

	d.API.Add(api.Op("POST", "/v1/saas/entitlements", "Create owned subscription and quotas", tags, manage, SubscriptionInput{}, Saved{}, h.createSubscription))
	routes := d.Router.Admin.With(auth.RequirePerm(PermManage), auth.CSRFMiddleware())
	routes.Get("/saas/new", h.managementForm)
	routes.Post("/saas/apps", h.appForm)
	routes.Post("/saas/apps/{id}", h.appUpdateForm)
	routes.Post("/saas/subscriptions", h.subscriptionForm)
	routes.Post("/saas/subscriptions/{id}/status", h.statusForm)
}
func (h *handlers) createApp(w http.ResponseWriter, r *http.Request) {
	var q AppInput
	if e := api.Decode(w, r, &q); e != nil {
		api.Fail(w, r, e)
		return
	}
	v, e := h.s.CreateApp(r.Context(), q)
	if e != nil {
		fail(w, r, e)
		return
	}
	api.WriteItem(w, v)
}
func (h *handlers) updateApp(w http.ResponseWriter, r *http.Request) {
	var q AppInput
	if e := api.Decode(w, r, &q); e != nil {
		api.Fail(w, r, e)
		return
	}
	v, e := h.s.UpdateApp(r.Context(), chi.URLParam(r, "id"), q)
	if e != nil {
		fail(w, r, e)
		return
	}
	api.WriteItem(w, v)
}
func (h *handlers) createSubscription(w http.ResponseWriter, r *http.Request) {
	var q SubscriptionInput
	if e := api.Decode(w, r, &q); e != nil {
		api.Fail(w, r, e)
		return
	}
	v, e := h.s.CreateSubscription(r.Context(), q)
	if e != nil {
		fail(w, r, e)
		return
	}
	api.WriteItem(w, v)
}

type ownerOption struct{ ID, Name, Email string }

var formTemplate = template.Must(template.New("saas-create").Parse(`<!doctype html><html lang="ar" dir="rtl"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>إدارة SaaS</title><link rel="stylesheet" href="/saas/assets.css"></head><body><main><nav><a href="/admin/saas">التطبيقات والاشتراكات</a> · <a href="/admin">الإدارة</a></nav><h1>تطبيق واشتراك جديد</h1><p>صلاحيات واستهلاك محلي عبر حسابات GoRun ومفاتيح API ذات scopes. بدون نشر سيرفر أو دفع.</p><article><h2>إضافة تطبيق</h2><form method="post" action="/admin/saas/apps"><input type="hidden" name="_csrf" value="{{.CSRF}}"><input type="hidden" name="request_key" value="{{.AppKey}}"><label>الاسم<input name="name" required maxlength="200"></label><label>slug<input name="slug" required pattern="[a-z0-9][a-z0-9-]{0,79}"></label><label>المالك<select name="owner_id" required>{{range .Owners}}<option value="{{.ID}}">{{.Name}} · {{.Email}}</option>{{end}}</select></label><label>رابط التطبيق الاختياري<input type="url" name="base_url"></label><button>إنشاء التطبيق</button></form></article><article><h2>إضافة اشتراك وخطة</h2><form method="post" action="/admin/saas/subscriptions"><input type="hidden" name="_csrf" value="{{.CSRF}}"><input type="hidden" name="request_key" value="{{.SubKey}}"><label>التطبيق<select name="app_id" required>{{range .Apps}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select></label><label>حساب العميل<select name="owner_id" required>{{range .Owners}}<option value="{{.ID}}">{{.Name}} · {{.Email}}</option>{{end}}</select></label><label>الخطة<input name="plan_id" required maxlength="100"></label><label>المقياس<input name="metric" value="tasks" required></label><label>الحد<input type="number" min="0" name="limit" value="100" required></label><label>انتهاء اختياري UTC<input name="valid_until" placeholder="2027-01-01T00:00:00Z"></label><button>إنشاء الاشتراك</button></form></article></main></body></html>`))

func (h *handlers) managementForm(w http.ResponseWriter, r *http.Request) {
	o, e := h.s.Overview(r.Context(), true)
	if e != nil {
		fail(w, r, e)
		return
	}
	owners := []ownerOption{}
	rows, e := h.s.DB.QueryContext(r.Context(), `SELECT id,name,email FROM users WHERE status='active' ORDER BY name LIMIT 500`)
	if e != nil {
		fail(w, r, e)
		return
	}
	for rows.Next() {
		var u ownerOption
		if e = rows.Scan(&u.ID, &u.Name, &u.Email); e != nil {
			rows.Close()
			fail(w, r, e)
			return
		}
		owners = append(owners, u)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		fail(w, r, e)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = formTemplate.Execute(w, struct {
		CSRF, AppKey, SubKey string
		Owners               []ownerOption
		Apps                 []App
	}{auth.CSRFToken(r.Context()), db.NewID(), db.NewID(), owners, o.Apps})
}
func (h *handlers) appForm(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if e := r.ParseForm(); e != nil {
		fail(w, r, ErrInvalid)
		return
	}
	_, e := h.s.CreateApp(r.Context(), AppInput{OwnerID: r.Form.Get("owner_id"), Name: r.Form.Get("name"), Slug: r.Form.Get("slug"), BaseURL: r.Form.Get("base_url"), Status: "active", Mode: "subscription", IdempotencyKey: r.Form.Get("request_key")})
	if e != nil {
		fail(w, r, e)
		return
	}
	http.Redirect(w, r, "/admin/saas", 303)
}
func (h *handlers) subscriptionForm(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if e := r.ParseForm(); e != nil {
		fail(w, r, ErrInvalid)
		return
	}
	limit, e := strconv.ParseInt(r.Form.Get("limit"), 10, 64)
	if e != nil {
		fail(w, r, ErrInvalid)
		return
	}
	_, e = h.s.CreateSubscription(r.Context(), SubscriptionInput{AppID: r.Form.Get("app_id"), OwnerID: r.Form.Get("owner_id"), PlanID: r.Form.Get("plan_id"), Status: "active", Features: map[string]bool{"subscription": true}, Limits: map[string]int64{r.Form.Get("metric"): limit}, ValidUntil: r.Form.Get("valid_until"), IdempotencyKey: r.Form.Get("request_key")})
	if e != nil {
		fail(w, r, e)
		return
	}
	http.Redirect(w, r, "/admin/saas", 303)
}
func (h *handlers) statusForm(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if e := r.ParseForm(); e != nil {
		fail(w, r, ErrInvalid)
		return
	}
	e := h.s.UpdateStatus(r.Context(), chi.URLParam(r, "id"), StatusUpdate{ExpectedStatus: r.Form.Get("expected_status"), Status: r.Form.Get("status")})
	if e != nil {
		fail(w, r, e)
		return
	}
	http.Redirect(w, r, "/admin/saas", 303)
}

func (h *handlers) appUpdateForm(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if e := r.ParseForm(); e != nil {
		fail(w, r, ErrInvalid)
		return
	}
	_, e := h.s.UpdateApp(r.Context(), chi.URLParam(r, "id"), AppInput{OwnerID: r.Form.Get("owner_id"), Name: r.Form.Get("name"), Slug: r.Form.Get("slug"), BaseURL: r.Form.Get("base_url"), Status: r.Form.Get("status"), Mode: r.Form.Get("mode"), ExpectedVersion: r.Form.Get("expected_version")})
	if e != nil {
		fail(w, r, e)
		return
	}
	http.Redirect(w, r, "/admin/saas", 303)
}

func (h *handlers) updateSubscription(w http.ResponseWriter, r *http.Request) {
	var q SubscriptionInput
	if e := api.Decode(w, r, &q); e != nil {
		api.Fail(w, r, e)
		return
	}
	v, e := h.s.UpdateSubscription(r.Context(), chi.URLParam(r, "id"), q)
	if e != nil {
		fail(w, r, e)
		return
	}
	api.WriteItem(w, v)
}

func (h *handlers) updateServices(w http.ResponseWriter, r *http.Request) {
	var q ServicesUpdate
	if e := api.Decode(w, r, &q); e != nil {
		api.Fail(w, r, e)
		return
	}
	v, e := h.s.UpdateServices(r.Context(), chi.URLParam(r, "id"), q)
	if e != nil {
		fail(w, r, e)
		return
	}
	api.WriteItem(w, v)
}
