package commerce

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
)

const cartCookie = "gp_cart"
const maxCartItems = 30

type CartItem struct {
	ProductID string `json:"product_id"`
	Quantity  int    `json:"quantity"`
}
type CartLine struct {
	Product    Product `json:"product"`
	Quantity   int     `json:"quantity"`
	TotalMinor int64   `json:"total_minor"`
}
type Cart struct {
	Lines      []CartLine `json:"lines"`
	TotalMinor int64      `json:"total_minor"`
	Currency   string     `json:"currency"`
	Key        string     `json:"key"`
}
type cartState struct {
	Items []CartItem `json:"items"`
	Key   string     `json:"key"`
}
type CartCheckout struct {
	Intake         map[string]map[string]string `json:"intake,omitempty"`
	Items          []CartItem                   `json:"items"`
	Email          string                       `json:"email"`
	Name           string                       `json:"name"`
	Notes          string                       `json:"notes"`
	IdempotencyKey string                       `json:"idempotency_key"`
}

func cartError(field string) error {
	return &ValidationError{Fields: map[string]string{field: "commerce.error.cart_invalid"}}
}
func quoteCart(ctx context.Context, q queryer, items []CartItem) (*Cart, error) {
	if len(items) == 0 || len(items) > maxCartItems {
		return nil, cartError("items")
	}
	c := &Cart{Lines: []CartLine{}}
	seen := map[string]bool{}
	for _, i := range items {
		if i.Quantity < 1 || i.Quantity > 99 || seen[i.ProductID] {
			return nil, cartError("quantity")
		}
		seen[i.ProductID] = true
		p, e := scanProduct(q.QueryRowContext(ctx, `SELECT `+productColumns+` FROM products WHERE id=?`, i.ProductID))
		if errors.Is(e, ErrNotFound) {
			return nil, cartError("product")
		}
		if e != nil {
			return nil, e
		}
		// Provisioning retains its dedicated, single-product Stripe workflow.
		if !p.Active || p.Kind == ProductProvisioning {
			return nil, cartError("product")
		}
		if c.Currency != "" && c.Currency != p.Currency {
			return nil, cartError("currency")
		}
		c.Currency = p.Currency
		if p.PriceMinor > math.MaxInt64/int64(i.Quantity) {
			return nil, cartError("total")
		}
		amount := p.PriceMinor * int64(i.Quantity)
		if c.TotalMinor > math.MaxInt64-amount {
			return nil, cartError("total")
		}
		c.TotalMinor += amount
		c.Lines = append(c.Lines, CartLine{Product: *p, Quantity: i.Quantity, TotalMinor: amount})
	}
	return c, nil
}

// SubmitCart records a manual request, never an assertion that payment occurred.
// Price, currency and product snapshots are read under the same transaction.
func (s *service) SubmitCart(ctx context.Context, req CartCheckout) (*Order, error) {
	email, ok := normalizeEmail(req.Email)
	if !ok {
		return nil, cartError("email")
	}
	req.Email = email
	req.Name = strings.TrimSpace(req.Name)
	req.Notes = strings.TrimSpace(req.Notes)
	if len(req.Name) > 200 || len(req.Notes) > 4000 || len(req.IdempotencyKey) < 16 || len(req.IdempotencyKey) > 100 {
		return nil, cartError("request")
	}
	initial, e := quoteCart(ctx, s.db, req.Items)
	if e != nil {
		return nil, e
	}
	for _, line := range initial.Lines {
		if line.Product.Kind == ProductDigital && !s.digitalFileExists(line.Product.DigitalFile) {
			return nil, cartError("product")
		}
	}
	if e = validateIntake(initial, req.Intake); e != nil {
		return nil, e
	}
	customerID, _, e := s.ensureCustomer(ctx, email)
	if e != nil {
		return nil, e
	}
	locale := s.siteLocale(ctx)
	baseURL := s.baseURL(ctx)
	var id string
	e = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		cart, e := quoteCart(ctx, tx, req.Items)
		if e != nil {
			return e
		}
		if e = validateIntake(cart, req.Intake); e != nil {
			return e
		}
		payload := struct {
			Intake             map[string]map[string]string `json:"Intake,omitempty"`
			Cart               *Cart
			Email, Name, Notes string
		}{req.Intake, cart, email, req.Name, req.Notes}
		b, _ := json.Marshal(payload)
		hash := sha256.Sum256(b)
		fingerprint := hex.EncodeToString(hash[:])
		var oldHash string
		e = tx.QueryRowContext(ctx, `SELECT id,checkout_hash FROM orders WHERE checkout_key=?`, req.IdempotencyKey).Scan(&id, &oldHash)
		if e == nil {
			if oldHash != fingerprint {
				return cartError("idempotency_key")
			}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		id = db.NewID()
		number, e := nextOrderNumber(ctx, tx)
		if e != nil {
			return e
		}
		now := s.nowString()
		snap := map[string]any{"kind": ProductService, "name": "Service request", "items": cart.Lines, "customer_name": req.Name, "notes": req.Notes, "payment_method": "manual_request"}
		_, e = tx.ExecContext(ctx, `INSERT INTO orders(id,number,customer_id,email,status,currency,total_minor,product_id,product_snapshot_json,created_at,updated_at,checkout_key,checkout_hash) VALUES(?,?,?,?,'pending',?,?,?,?,?,?,?,?)`, id, number, customerID, email, cart.Currency, cart.TotalMinor, cart.Lines[0].Product.ID, jsonText(snap, "{}"), now, now, req.IdempotencyKey, fingerprint)
		if e != nil {
			return e
		}
		for _, line := range cart.Lines {
			itemSnapshot := snapshotOf(&line.Product)
			itemSnapshot.Intake = req.Intake[line.Product.ID]
			itemSnapshot.IntakeSchema = line.Product.Intake
			if _, e = tx.ExecContext(ctx, `INSERT INTO order_items(id,order_id,product_id,quantity,unit_minor,snapshot_json) VALUES(?,?,?,?,?,?)`, db.NewID(), id, line.Product.ID, line.Quantity, line.Product.PriceMinor, jsonText(itemSnapshot, "{}")); e != nil {
				return e
			}
		}
		subject, html, text := requestReceiptEmail(s.d.I18n, locale, number, baseURL)
		if _, e = tx.ExecContext(ctx, `INSERT INTO commerce_request_receipts(order_id,recipient,subject,html_body,text_body,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, id, email, subject, html, text, now, now); e != nil {
			return e
		}
		s.audit.RecordTx(ctx, tx, "commerce.order.request", "order", id, map[string]any{"number": number, "total_minor": cart.TotalMinor, "currency": cart.Currency, "items": len(cart.Lines)})
		return nil
	})
	if e != nil {
		return nil, e
	}
	return s.Order(ctx, id)
}

func (s *service) cartMAC(raw string) string {
	m := hmac.New(sha256.New, s.d.Config.SecretKey)
	m.Write([]byte("commerce.cart:" + raw))
	return hex.EncodeToString(m.Sum(nil))
}
func (h *publicHandlers) readCart(r *http.Request) cartState {
	empty := cartState{Items: []CartItem{}, Key: db.NewID()}
	cookie, e := r.Cookie(cartCookie)
	if e != nil || len(cookie.Value) > 3800 {
		return empty
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 || !hmac.Equal([]byte(parts[1]), []byte(h.svc.cartMAC(parts[0]))) {
		return empty
	}
	b, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return empty
	}
	var c cartState
	if json.Unmarshal(b, &c) != nil || len(c.Items) > maxCartItems || len(c.Key) < 16 {
		return empty
	}
	return c
}
func (h *publicHandlers) writeCart(w http.ResponseWriter, r *http.Request, c cartState) {
	b, _ := json.Marshal(c)
	raw := base64.RawURLEncoding.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{Name: cartCookie, Value: raw + "." + h.svc.cartMAC(raw), Path: "/", MaxAge: 604800, HttpOnly: true, Secure: h.secure(r), SameSite: http.SameSiteLaxMode})
}
func (h *publicHandlers) cartPage(w http.ResponseWriter, r *http.Request) {
	h.renderCart(w, r, http.StatusOK, "")
}
func (h *publicHandlers) renderCart(w http.ResponseWriter, r *http.Request, status int, err string) {
	state := h.readCart(r)
	h.writeCart(w, r, state)
	c := &Cart{Lines: []CartLine{}, Key: state.Key, Currency: h.svc.currency(r.Context())}
	if len(state.Items) > 0 {
		var e error
		c, e = quoteCart(r.Context(), h.svc.db, state.Items)
		if e != nil {
			status = http.StatusUnprocessableEntity
			err = "commerce.error.cart_invalid"
			c = &Cart{Lines: []CartLine{}, Currency: h.svc.currency(r.Context())}
		}
	}
	c.Key = state.Key
	kind := kindCart
	if strings.TrimRight(r.URL.Path, "/") == "/checkout" {
		kind = kindCheckout
	}
	w.Header().Set("Cache-Control", "private, no-store")
	h.render(w, r, kind, "", status, map[string]any{"cart": c, "cart_error": err, "title": "المتجر — طلب خدمة", "idempotency_key": state.Key, "name": r.PostForm.Get("name"), "email": r.PostForm.Get("email"), "notes": r.PostForm.Get("notes"), "intake": intakeFromForm(r.PostForm)})
}
func (h *publicHandlers) cartAdd(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if r.ParseForm() != nil {
		h.renderCart(w, r, 400, "commerce.error.cart_invalid")
		return
	}
	c := h.readCart(r)
	id := r.PostForm.Get("product_id")
	n, e := strconv.Atoi(r.PostForm.Get("quantity"))
	if e != nil || n < 1 || n > 99 {
		h.renderCart(w, r, 422, "commerce.error.cart_invalid")
		return
	}
	found := false
	for i := range c.Items {
		if c.Items[i].ProductID == id {
			c.Items[i].Quantity = n
			found = true
		}
	}
	if !found {
		c.Items = append(c.Items, CartItem{ProductID: id, Quantity: n})
	}
	if _, e = quoteCart(r.Context(), h.svc.db, c.Items); e != nil {
		h.renderCart(w, r, 422, "commerce.error.cart_invalid")
		return
	}
	c.Key = db.NewID()
	h.writeCart(w, r, c)
	http.Redirect(w, r, "/cart", 303)
}
func (h *publicHandlers) cartRemove(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if r.ParseForm() != nil {
		http.Error(w, "invalid form", 400)
		return
	}
	c := h.readCart(r)
	items := []CartItem{}
	for _, i := range c.Items {
		if i.ProductID != r.PostForm.Get("product_id") {
			items = append(items, i)
		}
	}
	c.Items = items
	c.Key = db.NewID()
	h.writeCart(w, r, c)
	http.Redirect(w, r, "/cart", 303)
}
func (h *publicHandlers) cartCheckout(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if r.ParseForm() != nil {
		h.renderCart(w, r, 400, "commerce.error.cart_invalid")
		return
	}
	c := h.readCart(r)
	if r.PostForm.Get(honeypotField) != "" {
		http.Redirect(w, r, "/cart", 303)
		return
	}
	if !h.limiter.allow("checkout:"+httpx.ClientIP(r), checkoutLimit, limitWindow) {
		h.renderCart(w, r, 429, "commerce.error.rate_limited")
		return
	}
	if !hmac.Equal([]byte(c.Key), []byte(r.PostForm.Get("idempotency_key"))) {
		h.renderCart(w, r, 422, "commerce.error.cart_invalid")
		return
	}
	o, e := h.svc.SubmitCart(r.Context(), CartCheckout{Intake: intakeFromForm(r.PostForm), Items: c.Items, Email: r.PostForm.Get("email"), Name: r.PostForm.Get("name"), Notes: r.PostForm.Get("notes"), IdempotencyKey: c.Key})
	if e != nil {
		var ve *ValidationError
		if errors.As(e, &ve) {
			h.renderCart(w, r, 422, "commerce.error.cart_invalid")
		} else {
			h.svc.log.Error("commerce: cart checkout", "err", e)
			http.Error(w, "checkout failed", 500)
		}
		return
	}
	http.SetCookie(w, &http.Cookie{Name: orderCookie, Value: h.svc.orderToken(o.ID), Path: "/checkout", MaxAge: int(orderCookieTTL.Seconds()), HttpOnly: true, Secure: h.secure(r), SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/checkout/success?o="+o.ID, 303)
}

func (h *publicHandlers) cartClear(w http.ResponseWriter, r *http.Request) {
	h.writeCart(w, r, cartState{Items: []CartItem{}, Key: db.NewID()})
	http.Redirect(w, r, "/cart", http.StatusSeeOther)
}
