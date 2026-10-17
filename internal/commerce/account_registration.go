package commerce

import (
	"context"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
)

// Registration creates an unentitled customer only. Email ownership and
// paid access are never inferred from completing this form.
func (h *publicHandlers) accountRegister(w http.ResponseWriter, r *http.Request) {
	if !h.svc.registrationEnabled(r.Context()) {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if r.ParseForm() != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	if !h.limiter.allow("register:"+httpx.ClientIP(r), 5, limitWindow) || r.PostForm.Get(honeypotField) != "" {
		http.Redirect(w, r, "/account?error=registration", http.StatusSeeOther)
		return
	}
	users, ok := h.svc.users()
	name := strings.TrimSpace(r.PostForm.Get("name"))
	if !ok || len(name) > 200 || utf8.RuneCountInString(r.PostForm.Get("password")) < 10 {
		http.Redirect(w, r, "/account?error=registration", http.StatusSeeOther)
		return
	}
	u, err := users.Create(r.Context(), r.PostForm.Get("email"), name, r.PostForm.Get("password"), auth.RoleCustomer)
	if err != nil {
		http.Redirect(w, r, "/account?error=registration", http.StatusSeeOther)
		return
	}
	if err = auth.StartUserSession(h.svc.d, w, r, u); err != nil {
		http.Error(w, "account unavailable", http.StatusServiceUnavailable)
		return
	}
	http.Redirect(w, r, accountNext(r.PostForm.Get("next")), http.StatusSeeOther)
}

func (s *service) registrationEnabled(ctx context.Context) bool {
	var enabled bool
	return s.d.Settings.Get(ctx, "commerce.registration_enabled", &enabled) == nil && enabled
}
