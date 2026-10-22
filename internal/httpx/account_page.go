package httpx

import (
	"html/template"
	"net/http"

	"github.com/zadstack/gorun/internal/i18n"
)

// AccountPageCSS is shared presentation for standalone account add-on pages.
// Each enabled add-on serves it from its own route to avoid activation coupling.
const AccountPageCSS = `body{margin:0;background:#f7f8fa;color:#172b26;font:16px system-ui,sans-serif}main{max-width:1100px;margin:auto;padding:28px;min-width:0;overflow-wrap:anywhere}main *{box-sizing:border-box;min-width:0}nav{border-bottom:1px solid #d9e2dc;padding-bottom:14px}a{color:#176d50}h1{font-size:28px}h2{font-size:20px;margin-top:32px}article{background:white;border:1px solid #d9e2dc;border-radius:8px;padding:16px;margin:12px 0}table{width:100%;border-collapse:collapse;background:white}td,th{padding:12px;text-align:start;border-bottom:1px solid #d9e2dc}th{background:#eaf1ee}dl{display:grid;grid-template-columns:minmax(0,160px) minmax(0,1fr)}dd{margin:0;overflow-wrap:anywhere}pre{font-size:12px;max-width:240px;overflow:auto;direction:ltr}.scroll{max-width:100%;overflow:auto;overscroll-behavior-inline:contain}.scroll table{min-width:40rem}.scroll :is(td,th){min-width:8rem;vertical-align:top}.scroll th{white-space:nowrap}input,select,textarea{max-width:100%}label{display:block;margin:12px 0}input,select,button{font:inherit;padding:8px;border:1px solid #cbd8d0;border-radius:5px;margin:4px}button{background:#176d50;color:white;cursor:pointer}@media(max-width:600px){dl{grid-template-columns:minmax(0,1fr)}dd{margin-block-end:12px}}`

// AccountSignInUnavailable explains why no public account sign-in is offered.
// Staff authentication remains separate and must not accept customer accounts.
func AccountSignInUnavailable(w http.ResponseWriter, r *http.Request) {
	locale := i18n.Locale(r.Context())
	if _, resolved := i18n.RequestLocale(r.Context()); !resolved {
		locale = i18n.LocaleFromRequest(r, "")
	}
	direction, title := "rtl", "الدخول غير متاح"
	if locale == "en" {
		direction, title = "ltr", "Sign-in unavailable"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = accountSignInUnavailableTemplate.Execute(w, struct{ Locale, Direction, Title string }{locale, direction, title})
}

var accountSignInUnavailableTemplate = template.Must(template.New("account-sign-in-unavailable").Parse(`<!doctype html><html lang="{{.Locale}}" dir="{{.Direction}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}}</title></head><body><main><h1 lang="ar" dir="rtl">الدخول إلى حساب العميل غير متاح</h1><p lang="ar" dir="rtl">تواصل مع مسؤول الموقع لتفعيل إضافة الحسابات Commerce. بيانات العضويات والاشتراكات محفوظة.</p><p lang="en" dir="ltr">Customer sign-in is unavailable. Contact the site operator to enable Commerce account sign-in. Membership and subscription data are preserved.</p><p><a href="/">الموقع · Home</a> · <a href="/admin/login">دخول الموظفين · Staff sign-in</a></p><p><code>account_sign_in_unavailable</code></p></main></body></html>`))
