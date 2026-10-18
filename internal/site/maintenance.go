package site

import (
	"html/template"
	"net/http"
	"strings"

	"github.com/zadstack/gorun/internal/httpx"
)

// SettingMaintenance (bool) puts the public site in maintenance: every
// public page answers 503 with a short notice, while the admin, the API, MCP,
// /healthz, the sitemap, robots.txt, media and theme assets keep working.
const SettingMaintenance = "site.maintenance"

var maintenancePage = template.Must(template.New("m").Parse(`<!doctype html>
<html lang="{{.Lang}}" dir="{{.Dir}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex"><title>{{.Title}}</title>
<style>body{margin:0;min-height:100vh;display:grid;place-items:center;background:#f3f5f2;color:#141816;font:16px/1.7 system-ui,-apple-system,"Segoe UI",Tahoma,sans-serif}
main{max-width:34rem;padding:2rem;text-align:center}h1{font-size:22px;margin:0 0 .5rem}p{margin:0;color:#637069}</style></head>
<body><main><h1>{{.Title}}</h1><p>{{.Text}}</p></main></body></html>`))

// maintenance answers public pages with the notice while the setting is on.
func (h *handlers) legacyMaintenance(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if maintenanceExempt(r) || h.d.Settings == nil || !h.boolSetting(r.Context(), SettingMaintenance, false) {
			next.ServeHTTP(w, r)
			return
		}
		locale := h.d.Settings.GetString(r.Context(), "site.locale", "ar")
		data := map[string]string{"Lang": "ar", "Dir": "rtl", "Title": h.d.Settings.GetString(r.Context(), "site.title", ""),
			"Text": "الموقع تحت الصيانة دلوقتي، وهيرجع قريب."}
		if !strings.HasPrefix(locale, "ar") {
			data["Lang"], data["Dir"], data["Text"] = "en", "ltr", "This site is under maintenance and will be back soon."
		}
		if data["Title"] == "" {
			data["Title"] = "…"
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Retry-After", "3600")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusServiceUnavailable)
		if r.Method != http.MethodHead {
			_ = maintenancePage.Execute(w, data)
		}
	})
}

func maintenanceExempt(r *http.Request) bool {
	if r.URL.Path == "/mcp" {
		return true
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	p := r.URL.Path
	if p == "/sitemap.xml" || p == "/robots.txt" || p == httpx.LaunchCSSPath || p == httpx.LaunchPolishPath ||
		strings.HasPrefix(p, "/media/") || strings.HasPrefix(p, httpx.StaticPrefix) {
		return true
	}
	parts := strings.SplitN(strings.TrimPrefix(p, "/"), "/", 4)
	return len(parts) == 4 && parts[0] == "themes" && parts[1] != "" && parts[2] == "assets"
}
