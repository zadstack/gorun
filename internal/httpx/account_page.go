package httpx

import (
	"html/template"
	"net/http"

	"github.com/zadstack/gorun/internal/i18n"
)

// AccountPageCSS is shared presentation for standalone account add-on pages.
// Each enabled add-on serves it from its own route to avoid activation coupling.
const AccountPageCSS = `
body{margin:0;background:var(--standalone-bg,#f8fafb);color:var(--standalone-ink,#1e293b);font:16px/1.65 var(--font-ui,system-ui,sans-serif)}
main{max-width:var(--gp-width,1100px);margin:auto;padding:28px;min-width:0;overflow-wrap:anywhere}
main *{box-sizing:border-box;min-width:0}
nav{display:flex;flex-wrap:wrap;align-items:center;gap:4px 12px;border-bottom:1px solid var(--standalone-line,#d9e2dc);padding-bottom:14px}
nav a{display:inline-flex;align-items:center;min-height:44px}
a{color:var(--standalone-link,#315f9b);text-underline-offset:3px}
h1{font-size:28px;line-height:1.3}h2{font-size:20px;line-height:1.4;margin-top:32px}h3{line-height:1.4}
article,table{background:var(--standalone-panel,#fbfdfc)}
article{border:1px solid var(--standalone-line,#d9e2dc);border-radius:8px;padding:16px;margin:12px 0}
table{width:100%;border-collapse:collapse}
td,th{padding:12px;text-align:start;border-bottom:1px solid var(--standalone-line,#d9e2dc)}
th{background:var(--standalone-bg,#eaf1ee)}
dl{display:grid;grid-template-columns:minmax(0,160px) minmax(0,1fr);gap:4px 12px}
dd{margin:0;overflow-wrap:anywhere}pre{font-size:12px;max-width:240px;overflow:auto;direction:ltr}
.scroll{max-width:100%;overflow:auto;overscroll-behavior-inline:contain}.scroll table{min-width:40rem}.scroll :is(td,th){min-width:8rem;vertical-align:top}.scroll th{white-space:nowrap}
input,select,textarea{max-width:100%;background:var(--standalone-panel,#fbfdfc);color:inherit}
label{display:block;margin:12px 0}
input,select,textarea,button{font:inherit;padding:8px;border:1px solid var(--standalone-line,#cbd8d0);border-radius:5px;margin:4px}
input:not([type=checkbox]):not([type=radio]):not([type=hidden]),select,button,summary{min-height:44px}
textarea{display:block;width:100%;line-height:1.65;resize:vertical}
summary{cursor:pointer;padding-block:8px}
button{background:var(--standalone-accent,#315f9b);color:var(--standalone-on-accent,#fbfdfc);cursor:pointer}
button:disabled{cursor:not-allowed;opacity:.65}
:is(a,button,input,select,textarea,summary,.scroll):focus-visible{outline:2px solid var(--standalone-ink,#1e293b);outline-offset:3px}
[aria-invalid=true]{border:2px solid currentColor}
[role=alert],[role=status]{padding:12px 16px;border:1px solid var(--standalone-line,#d9e2dc);border-radius:5px}
@media(max-width:600px){main{padding:20px 16px}h1{font-size:24px}dl{grid-template-columns:minmax(0,1fr)}dd{margin-block-end:12px}}
`

// AccountSignInUnavailable explains why no public account sign-in is offered.
// Staff authentication remains separate and must not accept customer accounts.
func AccountSignInUnavailable(w http.ResponseWriter, r *http.Request) {
	contextLocale, _ := i18n.RequestLocale(r.Context())
	locale := i18n.LocaleFromRequest(r, contextLocale)
	direction, title := "rtl", "الدخول غير متاح"
	if locale == "en" {
		direction, title = "ltr", "Sign-in unavailable"
	}
	w.Header().Add("Vary", "Accept-Language")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = accountSignInUnavailableTemplate.Execute(w, struct {
		Locale, Direction, Title, Nonce string
		CSS                             template.CSS
	}{Locale: locale, Direction: direction, Title: title, Nonce: Nonce(r.Context()), CSS: template.CSS(AccountPageCSS)})
}

var accountSignInUnavailableTemplate = template.Must(template.New("account-sign-in-unavailable").Parse(`<!doctype html><html lang="{{.Locale}}" dir="{{.Direction}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}}</title><style nonce="{{.Nonce}}">{{.CSS}}</style></head><body><main><h1 lang="ar" dir="rtl">الدخول إلى حساب العميل غير متاح</h1><p lang="ar" dir="rtl">تواصل مع مسؤول الموقع لتفعيل إضافة الحسابات Commerce. بيانات العضويات والاشتراكات محفوظة.</p><p lang="en" dir="ltr">Customer sign-in is unavailable. Contact the site operator to enable Commerce account sign-in. Membership and subscription data are preserved.</p><p><a href="/">الموقع · Home</a> · <a href="/admin/login">دخول الموظفين · Staff sign-in</a></p><p><code>account_sign_in_unavailable</code></p></main></body></html>`))
