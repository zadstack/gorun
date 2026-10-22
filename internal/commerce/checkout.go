package commerce

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/media"
)

const (
	orderCookie    = "gp_order"
	orderCookieTTL = 7 * 24 * time.Hour
	maxEmailLen    = 254
	honeypotField  = "website"
	pollStopStatus = 286 // htmx stops polling on 286
)

// normalizeEmail lowercases and validates an email address.
func normalizeEmail(raw string) (string, bool) {
	e := strings.ToLower(strings.TrimSpace(raw))
	if e == "" || len(e) > maxEmailLen {
		return "", false
	}
	a, err := mail.ParseAddress(e)
	if err != nil || a.Address != e || !strings.Contains(e[strings.LastIndexByte(e, '@')+1:], ".") {
		return "", false
	}
	return e, true
}

// StartCheckout creates the customer (user role customer + customers row)
// if needed, a pending order with a product snapshot, and a Stripe Checkout
// Session (client_reference_id = order id). It returns the Stripe URL.
func (s *service) StartCheckout(ctx context.Context, productID, email string) (string, string, error) {
	email, ok := normalizeEmail(email)
	if !ok {
		return "", "", &ValidationError{Fields: map[string]string{"email": "commerce.error.email_invalid"}}
	}
	p, err := s.productByID(ctx, productID)
	if err != nil {
		return "", "", err
	}
	// Configured intake must go through the validated cart request flow.
	// The single-product payment endpoint has no intake payload.
	amount, currency, priceID := p.priceFor(s.currency(ctx))
	if len(p.Intake) > 0 || !p.Active || (p.Kind == ProductDigital && !s.digitalFileExists(p.DigitalFile)) || (p.Kind == ProductProvisioning && (p.PlanCode == "" || p.ServerID == "")) || (amount <= 0 && priceID == "") {
		return "", "", &ValidationError{Fields: map[string]string{"product": "commerce.error.product_unavailable"}}
	}
	stripeMode := s.stripeMode(ctx)
	key := s.stripeSecretFor(ctx, stripeMode)
	if key == "" {
		return "", "", ErrStripeNotConfigured
	}
	customerID, user, err := s.ensureCustomer(ctx, email)
	if err != nil {
		return "", "", err
	}
	stripeCustomer, err := s.stripeCustomerID(ctx, customerID)
	if err != nil {
		return "", "", err
	}
	snap := snapshotOf(p)
	snap.PriceMinor, snap.Currency, snap.StripePriceID = amount, currency, priceID
	orderID, now := db.NewID(), s.nowString()
	var number string
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		var err error
		if number, err = nextOrderNumber(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO orders (id, number, customer_id, email, status, currency, total_minor, product_id,
			product_snapshot_json, stripe_mode, created_at, updated_at) VALUES (?, ?, ?, ?, 'pending', ?, ?, ?, ?, ?, ?, ?)`,
			orderID, number, customerID, email, currency, amount, p.ID, jsonText(snap, "{}"), stripeMode, now, now); err != nil {
			return fmt.Errorf("commerce: create order: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO order_items(id,order_id,product_id,quantity,unit_minor,snapshot_json) VALUES(?,?,?,1,?,?)`, db.NewID(), orderID, p.ID, amount, jsonText(snap, "{}")); err != nil {
			return err
		}
		s.audit.RecordTx(ctx, tx, "commerce.order.create", "order", orderID, map[string]any{"number": number, "product": p.Slug, "email": email, "stripe_mode": stripeMode})
		return nil
	})
	if err != nil {
		return "", "", err
	}
	base := s.baseURL(ctx)
	siteID, siteErr := media.StorageSiteID(ctx, s.db)
	if siteErr != nil {
		s.log.Error("commerce: site id", "err", siteErr)
		siteID = ""
	}
	plan := p.PlanCode
	if plan == "" {
		plan = p.Slug
	}
	mode := "payment"
	if p.BillingMode == "subscription" {
		mode = "subscription"
	}
	sess, err := s.checkout.Create(ctx, key, checkoutRequest{
		OrderID: orderID, OrderNumber: number, Email: email, ProductName: p.Name, Currency: currency, AmountMinor: amount,
		Mode: mode, CustomerID: stripeCustomer, PriceID: priceID, Interval: p.Interval, TrialDays: p.TrialDays,
		AccountID: user.ID, Plan: plan, SiteID: siteID, AutomaticTax: s.automaticTax(ctx),
		// {CHECKOUT_SESSION_ID} is a Stripe template variable: keep the braces literal.
		SuccessURL: base + "/checkout/success?o=" + url.QueryEscape(orderID) + "&s={CHECKOUT_SESSION_ID}",
		CancelURL:  base + "/product/" + url.PathEscape(p.Slug),
	})
	if err != nil || sess.URL == "" {
		if _, uerr := s.db.ExecContext(ctx, `UPDATE orders SET status = 'cancelled', updated_at = ? WHERE id = ?`, s.nowString(), orderID); uerr != nil {
			s.log.Error("commerce: cancel order", "order", orderID, "err", uerr)
		}
		if err == nil {
			err = errors.New("commerce: Stripe returned no checkout URL")
		}
		return "", "", err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE orders SET stripe_session_id = ?, updated_at = ? WHERE id = ?`, sess.ID, s.nowString(), orderID); err != nil {
		return "", "", fmt.Errorf("commerce: store session: %w", err)
	}
	return sess.URL, orderID, nil
}

// orderToken signs an order id for the gp_order cookie.
func (s *service) orderToken(orderID string) string {
	mac := hmac.New(sha256.New, s.d.Config.SecretKey)
	mac.Write([]byte("commerce.order:" + orderID))
	return orderID + "." + hex.EncodeToString(mac.Sum(nil))
}

// canViewOrder reports whether the request may see order o: the signed
// gp_order cookie for it, the Stripe session id from the success redirect,
// or a logged-in user owning the order's customer record.
func (s *service) canViewOrder(r *http.Request, o *Order, sessionID string) bool {
	if c, err := r.Cookie(orderCookie); err == nil && hmac.Equal([]byte(c.Value), []byte(s.orderToken(o.ID))) {
		return true
	}
	if sessionID != "" && o.StripeSessionID != "" && hmac.Equal([]byte(sessionID), []byte(o.StripeSessionID)) {
		return true
	}
	if u := auth.UserFromContext(r.Context()); u != nil {
		if cid, err := s.customerIDForUser(r.Context(), u.ID); err == nil && cid == o.CustomerID {
			return true
		}
		return auth.Can(r.Context(), PermViewOrders)
	}
	return false
}

// handleCheckout is POST /checkout/{slug} (form: email, honeypot website).
func (h *publicHandlers) handleCheckout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := h.svc.Product(ctx, slugParam(r))
	if err != nil || !p.Active {
		h.notFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	if r.PostForm.Get(honeypotField) != "" { // bots: pretend nothing happened
		http.Redirect(w, r, "/product/"+url.PathEscape(p.Slug), http.StatusSeeOther)
		return
	}
	email := r.PostForm.Get("email")
	if !h.limiter.allow("checkout:"+httpx.ClientIP(r), checkoutLimit, limitWindow) {
		h.renderProduct(w, r, p, http.StatusTooManyRequests, "commerce.error.rate_limited", email)
		return
	}
	redirect, orderID, err := h.svc.StartCheckout(ctx, p.ID, email)
	var ve *ValidationError
	switch {
	case errors.As(err, &ve):
		key := ve.Fields["email"]
		if key == "" {
			key = "commerce.error.product_unavailable"
		}
		h.renderProduct(w, r, p, http.StatusUnprocessableEntity, key, email)
		return
	case errors.Is(err, ErrStripeNotConfigured):
		h.renderProduct(w, r, p, http.StatusServiceUnavailable, "commerce.checkout.unavailable", email)
		return
	case err != nil:
		h.svc.log.Error("commerce: checkout", "product", p.Slug, "err", err)
		h.renderProduct(w, r, p, http.StatusBadGateway, "commerce.checkout.failed", email)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: orderCookie, Value: h.svc.orderToken(orderID), Path: "/checkout", MaxAge: int(orderCookieTTL.Seconds()),
		HttpOnly: true, Secure: h.secure(r), SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

// orderView is the order as shown on the public result page.
type orderView struct {
	ManualRequest                                    bool
	RequestStatus                                    string
	FulfillmentNote                                  string
	ID, Number, Status, ProductName, Currency, Email string
	TotalMinor                                       int64
	Final, Active, Failed                            bool
}

func viewOf(o *Order) orderView {
	v := orderView{RequestStatus: o.RequestStatus, FulfillmentNote: o.FulfillmentNote, ID: o.ID, Number: o.Number, Status: o.Status, ProductName: o.ProductName, Currency: o.Currency, Email: o.Email, TotalMinor: o.TotalMinor}
	v.Active = o.Status == OrderActive
	v.Failed = o.Status == OrderFailed || o.Status == OrderRefunded || o.Status == OrderCancelled
	v.ManualRequest = o.ProductSnapshot["payment_method"] == "manual_request"
	v.Final = v.Active || v.Failed || v.ManualRequest || (o.Status == OrderPaid && o.snapshot().Kind != "" && o.snapshot().Kind != ProductProvisioning)
	return v
}

// handleSuccess is GET /checkout/success?o={order}&s={session}.
func (h *publicHandlers) handleSuccess(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	o, err := h.svc.Order(r.Context(), q.Get("o"))
	if err != nil || !h.svc.canViewOrder(r, o, q.Get("s")) {
		h.notFound(w, r)
		return
	}
	if q.Get("s") != "" { // remember the order so the status poll needs no session id
		http.SetCookie(w, &http.Cookie{Name: orderCookie, Value: h.svc.orderToken(o.ID), Path: "/checkout", MaxAge: int(orderCookieTTL.Seconds()),
			HttpOnly: true, Secure: h.secure(r), SameSite: http.SameSiteLaxMode})
	}
	v := viewOf(o)
	w.Header().Set("Cache-Control", "private, no-store")
	h.render(w, r, kindCheckoutResult, "", http.StatusOK, map[string]any{
		"order": v, "manual_request": o.ProductSnapshot["payment_method"] == "manual_request", "poll": !v.Final && o.ProductSnapshot["payment_method"] != "manual_request", "status_html": h.svc.statusFragment(r.Context(), v),
		"title": h.svc.d.I18n.T(i18n.Locale(r.Context()), "commerce.checkout.title"),
	})
}

// handleStatus is the HTMX poll target GET /checkout/status/{id}.
func (h *publicHandlers) handleStatus(w http.ResponseWriter, r *http.Request) {
	o, err := h.svc.Order(r.Context(), chi.URLParam(r, "id"))
	if err != nil || !h.svc.canViewOrder(r, o, "") {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}
	v := viewOf(o)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	status := http.StatusOK
	if v.Final {
		status = pollStopStatus
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(h.svc.statusFragment(r.Context(), v)))
}
