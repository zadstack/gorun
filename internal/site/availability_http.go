package site

import (
	"bytes"
	"context"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/httpx"
)

var availabilityPage = template.Must(template.New("availability").Parse(`<!doctype html><html lang="{{if .AR}}ar{{else}}en{{end}}" dir="{{if .AR}}rtl{{else}}ltr{{end}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex,nofollow"><title>{{if .Password}}{{if .AR}}موقع محمي{{else}}Protected website{{end}}{{else if .Construction}}{{if .AR}}الموقع تحت الإنشاء{{else}}Under construction{{end}}{{else}}{{if .AR}}الموقع غير متاح مؤقتًا{{else}}Website temporarily unavailable{{end}}{{end}}</title><style>body{margin:0;background:#111816;color:#eef4f0;font:17px/1.7 system-ui;min-height:100vh;display:grid;place-items:center}main{box-sizing:border-box;width:min(100%,38rem);padding:2rem}h1{font-size:clamp(1.7rem,6vw,2.6rem)}p{white-space:pre-wrap;overflow-wrap:anywhere}label,input,button{display:block;box-sizing:border-box;width:100%;font:inherit}input{background:#fff;border:1px solid #62746b;padding:.7rem;margin:.5rem 0;color:#111}button{padding:.8rem;background:#a4dec2;color:#10221a;border:0;cursor:pointer}a{color:#a4dec2}</style></head><body><main><h1>{{if .Password}}{{if .AR}}هذا الموقع محمي بكلمة مرور{{else}}This website is password protected{{end}}{{else if .Construction}}{{if .AR}}الموقع تحت الإنشاء{{else}}Under construction{{end}}{{else}}{{if .AR}}الموقع غير متاح مؤقتًا{{else}}Website temporarily unavailable{{end}}{{end}}</h1><p>{{.Message}}</p>{{if .Password}}<form method="post" action="/_site/unlock"><input type="hidden" name="_csrf" value="{{.CSRF}}"><label for="site-password">{{if .AR}}كلمة المرور{{else}}Website password{{end}}</label><input id="site-password" name="password" type="password" autocomplete="current-password" required maxlength="256"><button>{{if .AR}}فتح الموقع{{else}}Unlock website{{end}}</button></form>{{end}}</main></body></html>`))

func (h *handlers) maintenance(next http.Handler) http.Handler {
	legacy := h.legacyMaintenance(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Recovery and authenticated integrations live outside this public router.
		if r.URL.Path == "/mcp" || strings.HasPrefix(r.URL.Path, httpx.StaticPrefix) || strings.HasPrefix(r.URL.Path, "/auth/magic/") {
			next.ServeHTTP(w, r)
			return
		}
		platform, pe := platformPolicy()
		if pe == nil && platform.SiteID != "" {
			w.Header().Set("X-GoRun-Policy-Site", platform.SiteID)
			w.Header().Set("X-GoRun-Policy-Revision", strconv.FormatInt(platform.Revision, 10))
		}
		local, le := h.localAvailability(r.Context())
		if pe != nil || le != nil {
			h.availabilityNotice(w, r, Availability{Mode: "blocked"}, 503)
			return
		}
		var excluded []string
		for _, rule := range platform.PageRules {
			if rule.Mode != "public" {
				excluded = append(excluded, rule.ContentID)
			}
		}
		r = r.WithContext(content.WithPublicExclusions(context.WithValue(r.Context(), platformPolicyContextKey{}, platform), excluded))
		policy := effectiveAvailability(platform, local)
		scope := availabilityScope(platform, local, r.Host)
		// Editors can load private media in the CMS, but cannot bypass a platform block on public pages.
		editorMedia := strings.HasPrefix(r.URL.Path, "/media/") && auth.UserFromContext(r.Context()) != nil && (auth.Can(r.Context(), "media.upload") || auth.Can(r.Context(), "settings.manage"))
		if editorMedia {
			w.Header().Set("Cache-Control", "private, no-store")
			next.ServeHTTP(&privateWriter{ResponseWriter: w}, r)
			return
		}
		w = availabilityWriter(w, platform)
		if r.URL.Path == "/_site/unlock" {
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			auth.CSRFMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					http.Error(w, "Method not allowed", 405)
					return
				}
				if policy.Mode != "password" {
					h.availabilityNotice(w, r, policy, 503)
					return
				}
				if !h.unlock.allow(httpx.ClientIP(r)) {
					w.Header().Set("Retry-After", "60")
					h.availabilityNotice(w, r, policy, 429)
					return
				}
				if !h.unlock.begin() {
					w.Header().Set("Retry-After", "5")
					h.availabilityNotice(w, r, policy, 429)
					return
				}
				defer h.unlock.end()
				password := r.FormValue("password")
				if len(password) > 256 || !auth.VerifyPassword(policy.PasswordHash, password) {
					h.availabilityNotice(w, r, policy, 401)
					return
				}
				http.SetCookie(w, &http.Cookie{Name: unlockCookie, Value: signUnlock(h.d.Config.SecretKey, scope, time.Now().Add(time.Hour).Unix()), Path: "/", HttpOnly: true, Secure: httpx.SecureCookie(r, h.d.Config.BaseURL), SameSite: http.SameSiteLaxMode, MaxAge: 3600})
				w.Header().Set("Cache-Control", "private, no-store")
				http.Redirect(w, r, "/", 303)
			})).ServeHTTP(w, r)
			return
		}
		if policy.Mode == "password" {
			w.Header().Set("Cache-Control", "private, no-store")
			w.Header().Set("X-Robots-Tag", "noindex, nofollow")
			if !validUnlock(r, h.d.Config.SecretKey, scope) {
				auth.CSRFMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.availabilityNotice(w, r, policy, 401) })).ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(&privateWriter{ResponseWriter: w}, r)
			return
		}
		if policy.Mode == "blocked" || policy.Mode == "construction" {
			h.availabilityNotice(w, r, policy, 503)
			return
		}

		legacy.ServeHTTP(w, r)
	})
}
func (h *handlers) availabilityNotice(w http.ResponseWriter, r *http.Request, p Availability, status int) {
	ar := strings.HasPrefix(r.URL.Path, "/ar/") || h.d.Settings.GetString(r.Context(), "site.locale", "ar") == "ar"
	message := p.Message
	if message == "" {
		if p.Mode == "construction" {
			if ar {
				message = "نجهّز الموقع حاليًا. نراك قريبًا."
			} else {
				message = "We are getting this website ready. See you soon."
			}
		} else if ar {
			message = "الموقع غير متاح للزيارة حاليًا. يُرجى المحاولة لاحقًا."
		} else {
			message = "This website is not available to visitors right now. Please try again later."
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	if status == 503 {
		w.Header().Set("Retry-After", "3600")
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_ = availabilityPage.Execute(w, struct {
			AR, Password, Construction bool
			Message, CSRF              string
		}{ar, p.Mode == "password", p.Mode == "construction", message, auth.CSRFToken(r.Context())})
	}
}

type privateWriter struct{ http.ResponseWriter }

func (w *privateWriter) WriteHeader(code int) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.ResponseWriter.WriteHeader(code)
}
func (w *privateWriter) Write(b []byte) (int, error) {
	w.Header().Set("Cache-Control", "private, no-store")
	return w.ResponseWriter.Write(b)
}

func availabilityWriter(w http.ResponseWriter, platform Availability) http.ResponseWriter {
	if platform.Mode == "banner" && platform.Message != "" {
		// Compose the platform notice with both public pages and local access gates.
		return &bannerWriter{ResponseWriter: &privateWriter{ResponseWriter: w}, message: platform.Message}
	}
	return w
}

type bannerWriter struct {
	http.ResponseWriter
	message string
	done    bool
}

func (w *bannerWriter) WriteHeader(code int) {
	if strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		w.Header().Del("Content-Length")
		w.Header().Del("ETag")
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *bannerWriter) Write(b []byte) (int, error) {
	if !w.done && strings.Contains(w.Header().Get("Content-Type"), "text/html") && bytes.Contains(b, []byte("</body>")) {
		banner := `<aside role="status" dir="auto" style="position:relative;z-index:9999;padding:1rem;background:#18241e;color:#fff;border-top:2px solid #91cba9;font:16px/1.6 system-ui;white-space:pre-wrap;overflow-wrap:anywhere">` + template.HTMLEscapeString(w.message) + `</aside></body>`
		modified := bytes.Replace(b, []byte("</body>"), []byte(banner), 1)
		w.done = true
		_, err := w.ResponseWriter.Write(modified)
		return len(b), err
	}
	return w.ResponseWriter.Write(b)
}
