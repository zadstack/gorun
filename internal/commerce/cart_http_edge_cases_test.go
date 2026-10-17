package commerce

import (
	"context"
	"encoding/base64"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// cartState returns the signed cart currently held in e's cookie jar.
func (e *env) cartState() cartState {
	e.t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+"/cart", nil)
	for _, c := range e.c.Jar.Cookies(req.URL) {
		req.AddCookie(c)
	}
	return (&publicHandlers{svc: e.svc}).readCart(req)
}

func TestCartAddUpdateRemoveAndClear(t *testing.T) {
	e := boot(t)
	a := serviceProduct(t, e, "cart-a", 1000)
	b := serviceProduct(t, e, "cart-b", 300)
	csrf := e.csrfFrom("/product/" + a.Slug)
	add := func(id, qty string) int {
		t.Helper()
		resp, _ := e.do("POST", "/cart/items", url.Values{"_csrf": {csrf}, "product_id": {id}, "quantity": {qty}})
		return resp.StatusCode
	}
	if st := add(a.ID, "1"); st != 303 {
		t.Fatalf("add a: %d", st)
	}
	if st := add(b.ID, "2"); st != 303 {
		t.Fatalf("add b: %d", st)
	}
	// Adding the same product again replaces its quantity.
	if st := add(a.ID, "3"); st != 303 {
		t.Fatalf("update a: %d", st)
	}
	items := e.cartState().Items
	if len(items) != 2 || items[0].ProductID != a.ID || items[0].Quantity != 3 || items[1].Quantity != 2 {
		t.Fatalf("cart = %+v", items)
	}
	for _, qty := range []string{"0", "100", "abc"} {
		if st := add(a.ID, qty); st != 422 {
			t.Fatalf("quantity %q: %d", qty, st)
		}
	}
	if st := add("missing-product", "1"); st != 422 {
		t.Fatalf("unknown product: %d", st)
	}
	if got := e.cartState().Items; len(got) != 2 || got[0].Quantity != 3 {
		t.Fatalf("rejected adds changed the cart: %+v", got)
	}
	_, page := e.do("GET", "/cart", nil)
	mustContain(t, page, "cart-a", "cart-b")

	keyBefore := e.cartState().Key
	resp, _ := e.do("POST", "/cart/remove", url.Values{"_csrf": {csrf}, "product_id": {a.ID}})
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/cart" {
		t.Fatalf("remove: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	after := e.cartState()
	if len(after.Items) != 1 || after.Items[0].ProductID != b.ID || after.Key == keyBefore {
		t.Fatalf("after remove = %+v (key rotated: %v)", after.Items, after.Key != keyBefore)
	}
	resp, _ = e.do("POST", "/cart/clear", url.Values{"_csrf": {csrf}})
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/cart" {
		t.Fatalf("clear: %d", resp.StatusCode)
	}
	if got := e.cartState().Items; len(got) != 0 {
		t.Fatalf("after clear = %+v", got)
	}
}

func TestCartOversizedBodiesRejected(t *testing.T) {
	e := boot(t)
	p := serviceProduct(t, e, "big-body", 100)
	csrf := e.csrfFrom("/product/" + p.Slug)
	huge := "product_id=" + strings.Repeat("x", 20000)
	for _, path := range []string{"/cart/items", "/cart/remove", "/checkout"} {
		resp, _ := e.rawPost(path, csrf, huge)
		if resp.StatusCode != 400 {
			t.Errorf("%s oversized body: %d, want 400", path, resp.StatusCode)
		}
	}
	if n := e.count(`SELECT count(*) FROM orders`); n != 0 {
		t.Fatalf("orders created: %d", n)
	}
}

// rawPost sends body verbatim (CSRF in the header).
func (e *env) rawPost(path, csrf, body string) (*http.Response, string) {
	e.t.Helper()
	req, _ := http.NewRequest("POST", e.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := e.c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestCartCheckoutGuards(t *testing.T) {
	e := boot(t)
	p := serviceProduct(t, e, "guarded", 700)
	csrf := e.csrfFrom("/product/" + p.Slug)
	if resp, _ := e.do("POST", "/cart/items", url.Values{"_csrf": {csrf}, "product_id": {p.ID}, "quantity": {"1"}}); resp.StatusCode != 303 {
		t.Fatalf("add: %d", resp.StatusCode)
	}
	key := e.cartState().Key

	// Honeypot: silently back to the cart, nothing recorded.
	resp, _ := e.do("POST", "/checkout", url.Values{"_csrf": {csrf}, honeypotField: {"spam"}, "email": {"bot@example.test"}, "idempotency_key": {key}})
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/cart" {
		t.Fatalf("honeypot: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	// Stale idempotency key (cart changed in another tab).
	if resp, _ = e.do("POST", "/checkout", url.Values{"_csrf": {csrf}, "email": {"a@example.test"}, "idempotency_key": {"stale-key-0000000000"}}); resp.StatusCode != 422 {
		t.Fatalf("stale key: %d", resp.StatusCode)
	}
	// Validation failure from SubmitCart re-renders the cart with 422.
	resp, page := e.do("POST", "/checkout", url.Values{"_csrf": {csrf}, "email": {"not-an-email"}, "idempotency_key": {key}, "name": {"Kept Name"}})
	if resp.StatusCode != 422 {
		t.Fatalf("invalid email: %d", resp.StatusCode)
	}
	mustContain(t, page, "Kept Name")
	if n := e.count(`SELECT count(*) FROM orders`); n != 0 {
		t.Fatalf("orders after rejected checkouts: %d", n)
	}
	// After checkoutLimit attempts from one IP the next is rate limited.
	limited := false
	for i := 0; i < checkoutLimit+1; i++ {
		resp, _ = e.do("POST", "/checkout", url.Values{"_csrf": {csrf}, "email": {"a@example.test"}, "idempotency_key": {"stale-key-0000000000"}})
		if resp.StatusCode == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("checkout never rate limited")
	}
}

func TestCartPageWithStaleProductIs422(t *testing.T) {
	e := boot(t)
	p := serviceProduct(t, e, "soon-gone", 100)
	csrf := e.csrfFrom("/product/" + p.Slug)
	if resp, _ := e.do("POST", "/cart/items", url.Values{"_csrf": {csrf}, "product_id": {p.ID}, "quantity": {"1"}}); resp.StatusCode != 303 {
		t.Fatalf("add: %d", resp.StatusCode)
	}
	p.Active = false
	if _, err := e.svc.SaveProduct(context.Background(), *p); err != nil {
		t.Fatal(err)
	}
	resp, page := e.do("GET", "/cart", nil)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("stale cart page: %d", resp.StatusCode)
	}
	mustNotContain(t, page, "soon-gone")
	if cc := resp.Header.Get("Cache-Control"); cc != "private, no-store" {
		t.Fatalf("cache-control = %q", cc)
	}
}

func TestReadCartRejectsSignedGarbage(t *testing.T) {
	e := boot(t)
	h := &publicHandlers{svc: e.svc}
	read := func(value string) cartState {
		r := &http.Request{Header: http.Header{}}
		r.AddCookie(&http.Cookie{Name: cartCookie, Value: value})
		return h.readCart(r)
	}
	sign := func(raw string) string { return raw + "." + e.svc.cartMAC(raw) }
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

	valid := read(sign(enc(`{"items":[{"product_id":"p1","quantity":2}],"key":"abcdefghijklmnopq"}`)))
	if len(valid.Items) != 1 || valid.Key != "abcdefghijklmnopq" {
		t.Fatalf("valid signed cart not read: %+v", valid)
	}
	for name, v := range map[string]string{
		"bad base64":   sign("!!!not-base64!!!"),
		"bad json":     sign(enc(`{"items":`)),
		"short key":    sign(enc(`{"items":[],"key":"short"}`)),
		"three parts":  "a.b.c",
		"oversized":    strings.Repeat("a", 3900),
		"mac mismatch": enc(`{"items":[],"key":"abcdefghijklmnopq"}`) + ".00",
	} {
		got := read(v)
		if len(got.Items) != 0 || len(got.Key) < 16 || got.Key == "abcdefghijklmnopq" {
			t.Errorf("%s: accepted %+v", name, got)
		}
	}
}

func TestQuoteCartRejectsMixedCurrencyOverflowAndEmpty(t *testing.T) {
	e := boot(t)
	ctx := context.Background()
	aed := serviceProduct(t, e, "aed", 100)
	usd, err := e.svc.SaveProduct(ctx, Product{Kind: ProductService, Name: "usd", Slug: "usd", Active: true, Currency: "USD", PriceMinor: 100})
	if err != nil {
		t.Fatal(err)
	}
	huge := serviceProduct(t, e, "huge", math.MaxInt64/2)
	hugeToo := serviceProduct(t, e, "huge-too", math.MaxInt64/2)
	cases := map[string][]CartItem{
		"empty":          {},
		"too many items": make([]CartItem, maxCartItems+1),
		"mixed currency": {{aed.ID, 1}, {usd.ID, 1}},
		"line overflow":  {{huge.ID, 3}},
		"total overflow": {{huge.ID, 1}, {hugeToo.ID, 1}, {aed.ID, 99}},
	}
	for name, items := range cases {
		if _, err := quoteCart(ctx, e.svc.db, items); err == nil {
			t.Errorf("%s: accepted", name)
		} else if _, ok := err.(*ValidationError); !ok {
			t.Errorf("%s: err = %T %v, want *ValidationError", name, err, err)
		}
	}
	c, err := quoteCart(ctx, e.svc.db, []CartItem{{huge.ID, 1}, {hugeToo.ID, 1}})
	if err != nil || c.TotalMinor != 2*(math.MaxInt64/2) {
		t.Fatalf("largest representable total: %+v %v", c, err)
	}
}

func TestSubmitCartRequestValidation(t *testing.T) {
	e := boot(t)
	ctx := context.Background()
	p := serviceProduct(t, e, "validated", 100)
	digital, err := e.svc.SaveProduct(ctx, Product{Kind: ProductDigital, Name: "Digital", Slug: "digital-missing", Currency: "AED", PriceMinor: 100, DownloadDays: 1})
	if err != nil {
		t.Fatal(err)
	}
	// Activate it directly with a file key that does not exist on disk.
	if _, err := e.app.Deps.DB.Exec(`UPDATE products SET active = 1, digital_file = 'gone.zip' WHERE id = ?`, digital.ID); err != nil {
		t.Fatal(err)
	}
	good := CartCheckout{Items: []CartItem{{p.ID, 1}}, Email: "ok@example.test", IdempotencyKey: "abcdefghijklmnop-valid"}
	mutate := func(f func(*CartCheckout)) CartCheckout {
		c := good
		c.Items = append([]CartItem(nil), good.Items...)
		f(&c)
		return c
	}
	cases := map[string]CartCheckout{
		"bad email":       mutate(func(c *CartCheckout) { c.Email = "nope" }),
		"short key":       mutate(func(c *CartCheckout) { c.IdempotencyKey = "short" }),
		"long key":        mutate(func(c *CartCheckout) { c.IdempotencyKey = strings.Repeat("k", 101) }),
		"long name":       mutate(func(c *CartCheckout) { c.Name = strings.Repeat("n", 201) }),
		"long notes":      mutate(func(c *CartCheckout) { c.Notes = strings.Repeat("n", 4001) }),
		"no items":        mutate(func(c *CartCheckout) { c.Items = nil }),
		"missing digital": mutate(func(c *CartCheckout) { c.Items = []CartItem{{digital.ID, 1}} }),
	}
	for name, req := range cases {
		if _, err := e.svc.SubmitCart(ctx, req); err == nil {
			t.Errorf("%s: accepted", name)
		} else if _, ok := err.(*ValidationError); !ok {
			t.Errorf("%s: err = %T %v", name, err, err)
		}
	}
	if n := e.count(`SELECT count(*) FROM orders`); n != 0 {
		t.Fatalf("invalid requests stored %d orders", n)
	}
	if _, err := e.svc.SubmitCart(ctx, good); err != nil {
		t.Fatalf("valid request: %v", err)
	}
}
