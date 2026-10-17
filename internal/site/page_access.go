package site

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/theme"
)

type PageRule struct {
	ContentID    string `json:"content_id"`
	Mode         string `json:"mode"`
	Message      string `json:"message"`
	PasswordHash string `json:"password_hash,omitempty"`
	Revision     int64  `json:"revision"`
}

func validPageRules(rules []PageRule) bool {
	if len(rules) > 200 {
		return false
	}
	seen := map[string]bool{}
	for _, p := range rules {
		id := strings.ToLower(p.ContentID)
		if !policyID.MatchString(id) || seen[id] || p.Revision < 0 || len(p.Message) > 8000 || len([]rune(p.Message)) > 2000 || strings.ContainsRune(p.Message, 0) {
			return false
		}
		seen[id] = true
		if p.Mode != "public" && p.Mode != "blocked" && p.Mode != "password" {
			return false
		}
		if p.Mode == "password" {
			if !passwordPHC.MatchString(p.PasswordHash) {
				return false
			}
		} else if p.PasswordHash != "" {
			return false
		}
	}
	return true
}
func (h *handlers) effectivePage(ctx context.Context, id, host string) (content.PageAccess, string, error) {
	svc, ok := h.content()
	if !ok {
		return content.PageAccess{}, "", errors.New("content unavailable")
	}
	local := content.PageAccess{ContentID: id, Mode: "public"}
	if ps, ok := svc.(content.PageAccessService); ok {
		var err error
		local, err = ps.PageAccess(ctx, id)
		if err != nil {
			return local, "", err
		}
	}
	platform, present := ctx.Value(platformPolicyContextKey{}).(Availability)
	var err error
	if !present {
		platform, err = platformPolicy()
	}
	if err != nil {
		return local, "", err
	}
	chosen := choosePageAccess(local, platform.PageRules)
	sum := sha256.Sum256([]byte(host + "\x00" + platform.SiteID + "\x00" + id + "\x00" + strconv.FormatInt(platform.Revision, 10) + "\x00" + strconv.FormatInt(local.Revision, 10) + "\x00" + strconv.FormatInt(chosen.Revision, 10) + "\x00" + chosen.Mode + "\x00" + chosen.PasswordHash))
	return chosen, hex.EncodeToString(sum[:]), nil
}

type platformPolicyContextKey struct{}

func choosePageAccess(local content.PageAccess, rules []PageRule) content.PageAccess {
	chosen := local
	id := local.ContentID
	for _, p := range rules {
		if strings.EqualFold(p.ContentID, id) && p.Mode != "public" {
			chosen = content.PageAccess{ContentID: id, Mode: p.Mode, Message: p.Message, PasswordHash: p.PasswordHash, Revision: p.Revision}
			break
		}
	}
	return chosen
}
func pageCookie(id string) string { return "gorun_page_" + strings.ToLower(id) }
func pageUnlocked(r *http.Request, key []byte, id, scope string) bool {
	c, e := r.Cookie(pageCookie(id))
	if e != nil {
		return false
	}
	copy := r.Clone(r.Context())
	copy.Header = r.Header.Clone()
	copy.Header.Del("Cookie")
	copy.AddCookie(&http.Cookie{Name: unlockCookie, Value: c.Value})
	return validUnlock(copy, key, scope)
}
func (h *handlers) pageGate(w http.ResponseWriter, r *http.Request, it *content.Item) (http.ResponseWriter, bool) {
	p, scope, e := h.effectivePage(r.Context(), it.ID, r.Host)
	if e != nil {
		h.pageNotice(w, r, content.PageAccess{Mode: "blocked"}, 503)
		return w, false
	}
	if p.Mode == "public" {
		return w, true
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	if p.Mode == "password" && pageUnlocked(r, h.d.Config.SecretKey, it.ID, scope) {
		return &privateWriter{w}, true
	}
	status := 503
	if p.Mode == "password" {
		status = 401
	}
	auth.CSRFMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.pageNotice(w, r, p, status) })).ServeHTTP(w, r)
	return w, false
}
func (h *handlers) pageUnlock(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	svc, ok := h.content()
	if !ok {
		http.NotFound(w, r)
		return
	}
	it, e := svc.Get(r.Context(), id)
	if e != nil || it.Status != "published" || !h.visible(r.Context(), it) {
		http.NotFound(w, r)
		return
	}
	locale := it.Locale
	if locale == "" {
		locale = h.siteLocaleOf(r.Context())
	}
	r = r.WithContext(i18n.WithLocale(r.Context(), locale))
	p, scope, e := h.effectivePage(r.Context(), id, r.Host)
	if e != nil || p.Mode != "password" {
		http.NotFound(w, r)
		return
	}
	if !h.unlock.allow(httpx.ClientIP(r)) || !h.unlock.begin() {
		h.pageNotice(w, r, p, 429)
		return
	}
	defer h.unlock.end()
	password := r.FormValue("password")
	if len(password) > 256 || !auth.VerifyPassword(p.PasswordHash, password) {
		h.pageNotice(w, r, p, 401)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: pageCookie(id), Value: signUnlock(h.d.Config.SecretKey, scope, time.Now().Add(time.Hour).Unix()), Path: "/", HttpOnly: true, Secure: httpx.SecureCookie(r, h.d.Config.BaseURL), SameSite: http.SameSiteLaxMode, MaxAge: 3600})
	w.Header().Set("Cache-Control", "private, no-store")
	http.Redirect(w, r, theme.NewLinker(h.d).Item(r.Context(), it), 303)
}

var pageNoticeTemplate = template.Must(template.New("page-access").Parse(`<!doctype html><html lang="{{if .AR}}ar{{else}}en{{end}}" dir="{{if .AR}}rtl{{else}}ltr{{end}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex,nofollow"><title>{{if .AR}}حماية الصفحة{{else}}Page access{{end}}</title><style>body{font:18px/1.7 system-ui;max-width:38rem;margin:10vh auto;padding:1.5rem;background:#111816;color:#eef4f0}input,button{font:inherit;box-sizing:border-box;width:100%;padding:.8rem;margin:.5rem 0}p{white-space:pre-wrap;overflow-wrap:anywhere}</style></head><body><main><h1>{{if .Password}}{{if .AR}}هذه الصفحة محمية بكلمة مرور{{else}}This page is password protected{{end}}{{else}}{{if .AR}}هذه الصفحة غير متاحة حاليًا{{else}}This page is currently unavailable{{end}}{{end}}</h1><p>{{.Message}}</p>{{if .Password}}<form method="post" action="/_page/{{.ID}}/unlock"><input type="hidden" name="_csrf" value="{{.CSRF}}"><label for="password">{{if .AR}}كلمة المرور{{else}}Page password{{end}}</label><input id="password" name="password" type="password" required maxlength="256" autocomplete="current-password"><button>{{if .AR}}فتح الصفحة{{else}}Unlock page{{end}}</button></form>{{end}}</main></body></html>`))

func (h *handlers) pageNotice(w http.ResponseWriter, r *http.Request, p content.PageAccess, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_ = pageNoticeTemplate.Execute(w, struct {
			AR, Password      bool
			Message, ID, CSRF string
		}{i18n.Locale(r.Context()) == "ar", p.Mode == "password", p.Message, p.ContentID, auth.CSRFToken(r.Context())})
	}
}
