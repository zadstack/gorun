package commerce

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/theme"
)

// Theme kinds added by commerce (templates in parentheses, then index.html):
// shop (shop.html), product (product-{slug}.html, product.html), account
// (account.html), checkout-result (checkout-result.html).
const (
	kindCart           = "cart"
	kindCheckout       = "checkout"
	kindShop           = "shop"
	kindProduct        = "product"
	kindAccount        = "account"
	kindCheckoutResult = "checkout-result"
)

// registerTheme adds the kinds and the template funcs `products` (active
// products grouped) and `product slug` (an active product or nil).
func registerTheme(d *kernel.Deps, s *service) {
	d.Theme.AddKind(kindCart, func(kernel.KindRequest) []string { return []string{"cart.html"} })
	d.Theme.AddKind(kindCheckout, func(kernel.KindRequest) []string { return []string{"checkout.html"} })
	d.Theme.AddKind(kindShop, func(kernel.KindRequest) []string { return []string{"shop.html"} })
	d.Theme.AddKind(kindProduct, func(req kernel.KindRequest) []string {
		var c []string
		if req.Slug != "" {
			c = append(c, "product-"+req.Slug+".html")
		}
		return append(c, "product.html")
	})
	d.Theme.AddKind(kindAccount, func(kernel.KindRequest) []string { return []string{"account.html"} })
	d.Theme.AddKind(kindCheckoutResult, func(kernel.KindRequest) []string { return []string{"checkout-result.html"} })

	d.Theme.AddFunc("products", func() []ProductGroup {
		groups, err := s.Groups(context.Background())
		if err != nil {
			s.log.Error("commerce: theme products", "err", err)
		}
		return groups
	})
	d.Theme.AddFunc("product", func(slug string) *Product {
		p, err := s.Product(context.Background(), slug)
		if err != nil || !p.Active {
			return nil
		}
		return p
	})
}

// publicHandlers serve the shop, checkout and account pages.
type publicHandlers struct {
	svc     *service
	limiter *rateLimiter
}

// secure is the Secure flag of cookies set while handling r (the request
// is https, else GORUN_BASE_URL is https; never the DB setting).
func (h *publicHandlers) secure(r *http.Request) bool {
	return httpx.SecureCookie(r, h.svc.d.Config.BaseURL)
}

// siteLocale puts the public site locale (site.locale, default ar) into the
// request context, like the site module does for its pages.
func (h *publicHandlers) siteLocale(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		loc := h.svc.d.Settings.GetString(r.Context(), theme.SettingLocale, "ar")
		next.ServeHTTP(w, r.WithContext(i18n.WithLocale(r.Context(), loc)))
	})
}

// render renders kind through the active theme with extra merged into
// Context.Extra (Extra["commerce"] is always true).
func (h *publicHandlers) render(w http.ResponseWriter, r *http.Request, kind, slug string, status int, extra map[string]any) {
	svc, ok := kernel.Get[theme.Service](h.svc.d, kernel.ServiceTheme)
	if !ok {
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}
	q := map[string]string{}
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			q[k] = v[0]
		}
	}
	tc := &theme.Context{Request: r, Query: q, Locale: i18n.Locale(r.Context()), Status: status, Extra: map[string]any{"commerce": true}}
	for k, v := range extra {
		tc.Extra[k] = v
	}
	_ = svc.Render(r.Context(), w, kernel.KindRequest{Kind: kind, Slug: slug}, tc)
}

func (h *publicHandlers) notFound(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, theme.Kind404, "", http.StatusNotFound, nil)
}

// shop is GET /shop.
func (h *publicHandlers) shop(w http.ResponseWriter, r *http.Request) {
	groups, err := h.svc.Groups(r.Context())
	if err != nil {
		h.svc.log.Error("commerce: shop", "err", err)
		h.render(w, r, theme.Kind500, "", http.StatusInternalServerError, nil)
		return
	}
	h.render(w, r, kindShop, "", http.StatusOK, map[string]any{"groups": groups, "title": h.svc.d.I18n.T(i18n.Locale(r.Context()), "commerce.shop.title")})
}

// product is GET /product/{slug}.
func (h *publicHandlers) product(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.Product(r.Context(), slugParam(r))
	if err != nil || !p.Active {
		h.notFound(w, r)
		return
	}
	h.renderProduct(w, r, p, http.StatusOK, "", "")
}

// slugParam is the {slug} URL parameter, percent-decoded: WordPress links
// carry non-Latin slugs encoded (often lowercase hex), which chi may hand
// over still encoded.
func slugParam(r *http.Request) string { return decodeSlug(chi.URLParam(r, "slug")) }

func decodeSlug(s string) string {
	if u, err := url.PathUnescape(s); err == nil {
		return u
	}
	return s
}

// slashless redirects WooCommerce-style trailing-slash URLs (/shop/,
// /product/x/) to the canonical path.
func slashless(w http.ResponseWriter, r *http.Request) {
	target := strings.TrimRight(r.URL.EscapedPath(), "/")
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

// renderProduct renders the product page, optionally with a checkout error
// (an i18n key) and the email the visitor typed.
func (h *publicHandlers) renderProduct(w http.ResponseWriter, r *http.Request, p *Product, status int, errKey, email string) {
	extra := map[string]any{"product": p, "title": p.Name, "checkout_action": "/checkout/" + p.Slug, "email": strings.TrimSpace(email)}
	if errKey != "" {
		extra["checkout_error"] = errKey
	}
	if u := auth.UserFromContext(r.Context()); u != nil && email == "" {
		extra["email"] = u.Email
	}
	if m, ok := h.svc.media(); ok && p.ImageMediaID != "" {
		if item, err := m.Get(r.Context(), p.ImageMediaID); err == nil {
			extra["image_url"] = m.URL(item, "large")
		}
	}
	h.render(w, r, kindProduct, p.Slug, status, extra)
}
