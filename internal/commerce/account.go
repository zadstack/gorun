package commerce

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
)

// account is GET /account: the sign-in form for visitors, the customer's
// entitlements once signed in (via a magic link).
func (h *publicHandlers) account(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	u := auth.UserFromContext(r.Context())
	if u == nil {
		h.render(w, r, kindAccount, "", http.StatusOK, map[string]any{
			"next": accountNext(r.URL.Query().Get("next")), "login_form": true, "sent": r.URL.Query().Get("sent") == "1", "account_error": accountError(r),
			"registration_enabled": h.svc.registrationEnabled(r.Context()),
			"title":                h.svc.d.I18n.T(i18n.Locale(r.Context()), "commerce.account.title"),
		})
		return
	}
	ents, err := h.svc.CustomerEntitlements(r.Context(), u.ID)
	if err != nil {
		h.svc.log.Error("commerce: account entitlements", "err", err)
		ents = []Entitlement{}
	}
	orders := []Order{}
	invoices := []Invoice{}
	portal := false
	if cid, err := h.svc.customerIDForUser(r.Context(), u.ID); err == nil {
		orders, _, _ = h.svc.Orders(r.Context(), ListQuery{CustomerID: cid, PerPage: 100})
		invoices, _ = h.svc.customerInvoices(r.Context(), cid)
		if stripeID, err := h.svc.stripeCustomerID(r.Context(), cid); err == nil && stripeID != "" {
			portal = true
		}
	}
	items := map[string][]OrderItem{}
	for _, o := range orders {
		items[o.ID], _ = h.svc.orderItems(r.Context(), &o)
	}
	h.render(w, r, kindAccount, "", http.StatusOK, map[string]any{
		"orders": orders, "order_items": items, "invoices": invoices, "billing_portal": portal,
		"entitlements": ents, "customer": u, "account_error": accountError(r),
		"title": h.svc.d.I18n.T(i18n.Locale(r.Context()), "commerce.account.title"),
	})
}

// billingPortal is POST /account/billing-portal. Stripe hosts upgrade,
// cancel and payment-method changes; proration is the portal default.
func (h *publicHandlers) billingPortal(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	if !h.limiter.allow("portal:"+u.ID, checkoutLimit, limitWindow) {
		http.Redirect(w, r, "/account?error=rate", http.StatusSeeOther)
		return
	}
	cid, err := h.svc.customerIDForUser(r.Context(), u.ID)
	if err != nil {
		http.Redirect(w, r, "/account?error=open", http.StatusSeeOther)
		return
	}
	stripeID, err := h.svc.stripeCustomerID(r.Context(), cid)
	if err != nil || stripeID == "" {
		http.Redirect(w, r, "/account?error=open", http.StatusSeeOther)
		return
	}
	key := h.svc.stripeSecretKey(r.Context())
	if key == "" {
		http.Redirect(w, r, "/account?error=open", http.StatusSeeOther)
		return
	}
	url, err := h.svc.portal.Create(r.Context(), key, stripeID, h.svc.baseURL(r.Context())+"/account")
	if err != nil || url == "" {
		h.svc.log.Error("commerce: billing portal", "err", err)
		http.Redirect(w, r, "/account?error=open", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, url, http.StatusSeeOther)
}

// accountError maps ?error= to a catalog key (only known values).
func accountError(r *http.Request) string {
	switch r.URL.Query().Get("error") {
	case "registration":
		return "commerce.account.registration_failed"
	case "credentials":
		return "commerce.account.credentials_invalid"
	case "open":
		return "commerce.account.open_failed"
	case "rate":
		return "commerce.error.rate_limited"
	case "email":
		return "commerce.error.email_invalid"
	}
	return ""
}

// accountLogin is POST /account/login (email, honeypot website): email a
// magic link to an existing customer. The answer never reveals whether the
// address is known; staff accounts never get links here.
func (h *publicHandlers) accountLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	if r.PostForm.Get(honeypotField) != "" {
		http.Redirect(w, r, "/account?sent=1", http.StatusSeeOther)
		return
	}
	email, ok := normalizeEmail(r.PostForm.Get("email"))
	if !ok {
		http.Redirect(w, r, "/account?error=email", http.StatusSeeOther)
		return
	}
	ip := httpx.ClientIP(r)
	if !h.limiter.allow("login-ip:"+ip, loginLimit*3, limitWindow) || !h.limiter.allow("login:"+email, loginLimit, limitWindow) {
		http.Redirect(w, r, "/account?error=rate", http.StatusSeeOther)
		return
	}
	// The lookup and the email happen in a job, for every address: the
	// response time never reveals whether the account exists.
	if _, err := h.svc.d.Jobs.Enqueue(r.Context(), JobMagicMail, magicMailPayload{Email: email, Locale: i18n.Locale(r.Context())}); err != nil {
		h.svc.log.Error("commerce: enqueue login email", "err", err)
	}
	http.Redirect(w, r, "/account?sent=1", http.StatusSeeOther)
}

type magicMailPayload struct {
	Email  string `json:"email"`
	Locale string `json:"locale"`
}

// magicMail is the commerce.magic_mail job: email a sign-in link to an
// active customer account with this address (nothing for unknown or staff
// addresses).
func (s *service) magicMail(ctx context.Context, raw json.RawMessage) error {
	var p magicMailPayload
	if err := json.Unmarshal(raw, &p); err != nil || p.Email == "" {
		return nil
	}
	users, ok := s.users()
	if !ok {
		return nil
	}
	u, err := users.GetByEmail(ctx, p.Email)
	if errors.Is(err, auth.ErrNotFound) || (err == nil && (u.Role != auth.RoleCustomer || u.Status != auth.StatusActive)) {
		return nil
	}
	if err != nil {
		return err
	}
	token, err := auth.NewMagicLink(ctx, s.d, u.ID, 0)
	if err != nil {
		return err
	}
	locale := u.Locale
	if locale == "" {
		locale = firstNonEmpty(p.Locale, s.siteLocale(ctx))
	}
	subject, html, text := loginEmail(s.d.I18n, locale, s.baseURL(ctx)+"/auth/magic/"+token)
	return s.sendMail(ctx, u.Email, subject, html, text)
}

// accountOpen is POST /account/open/{entitlement}: ask the billing server
// for a login link and redirect to it. The entitlement must belong to the
// signed-in customer.
func (h *publicHandlers) accountOpen(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := auth.UserFromContext(ctx)
	if u == nil {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	if !h.limiter.allow("open:"+u.ID, openLimit, limitWindow) {
		http.Redirect(w, r, "/account?error=rate", http.StatusSeeOther)
		return
	}
	e, err := h.svc.Entitlement(ctx, chi.URLParam(r, "entitlement"))
	cid, cerr := h.svc.customerIDForUser(ctx, u.ID)
	if err != nil || cerr != nil || e.CustomerID != cid {
		h.notFound(w, r)
		return
	}
	link, err := h.svc.LoginLink(ctx, e.ID)
	if err != nil {
		if !errors.Is(err, ErrNotActive) {
			h.svc.log.Error("commerce: account open", "entitlement", e.ID, "err", err)
		}
		http.Redirect(w, r, "/account?error=open", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, link, http.StatusSeeOther)
}
