package commerce

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func serviceProduct(t *testing.T, e *env, slug string, price int64) *Product {
	t.Helper()
	p, err := e.svc.SaveProduct(context.Background(), Product{Kind: ProductService, Name: slug, Slug: slug, Active: true, Currency: "AED", PriceMinor: price})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestGenericCartManualPricesIdempotency(t *testing.T) {
	e := boot(t)
	ctx := context.Background()
	p := serviceProduct(t, e, "design-service", 12345)
	free := serviceProduct(t, e, "free-onboarding", 0)
	req := CartCheckout{Items: []CartItem{{p.ID, 2}, {free.ID, 1}}, Email: "client@example.test", Name: "Customer", Notes: "Brief", IdempotencyKey: "abcdefghijklmnop-request-one"}
	o, err := e.svc.SubmitCart(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if o.TotalMinor != 24690 || o.Status != OrderPending || o.PaidAt != "" {
		t.Fatalf("wrong pending request: %+v", o)
	}
	repeat, err := e.svc.SubmitCart(ctx, req)
	if err != nil || repeat.ID != o.ID {
		t.Fatalf("idempotency: %+v %v", repeat, err)
	}
	req.Items[0].Quantity = 3
	if _, err = e.svc.SubmitCart(ctx, req); err == nil {
		t.Fatal("reused key with changed cart allowed")
	}
	if e.count(`SELECT count(*) FROM orders`) != 1 || e.count(`SELECT count(*) FROM order_items`) != 2 || e.count(`SELECT count(*) FROM jobs WHERE kind='commerce.provision'`) != 0 || len(e.stripe.reqs) != 0 || len(e.mail.all()) != 0 {
		t.Fatal("manual request caused payment, email, duplicate or provisioning")
	}
	payload, sig := stripeEvent("evt_manual", evCheckoutCompleted, completedSession(o.ID))
	e.webhook(payload, sig)
	after, _ := e.svc.Order(ctx, o.ID)
	if after.Status != OrderPending {
		t.Fatal("Stripe event converted unrelated manual request")
	}
}
func TestGenericCartRejectsInvalidProducts(t *testing.T) {
	e := boot(t)
	ctx := context.Background()
	p := serviceProduct(t, e, "service", 100)
	prov := e.product("provisioning", e.server())
	for _, items := range [][]CartItem{{{p.ID, 0}}, {{p.ID, -1}}, {{p.ID, 100}}, {{p.ID, 1}, {p.ID, 1}}, {{prov.ID, 1}}, {{"missing", 1}}} {
		if _, err := quoteCart(ctx, e.svc.db, items); err == nil {
			t.Fatalf("accepted invalid cart %+v", items)
		}
	}
	p.Active = false
	if _, err := e.svc.SaveProduct(ctx, *p); err != nil {
		t.Fatal(err)
	}
	if _, err := quoteCart(ctx, e.svc.db, []CartItem{{p.ID, 1}}); err == nil {
		t.Fatal("inactive product quoted")
	}
	if _, err := e.svc.SaveProduct(ctx, Product{Name: "Legacy", Slug: "legacy", Active: true}); err == nil {
		t.Fatal("legacy provisioning activation bypassed mapping")
	}
	if _, err := e.svc.SaveProduct(ctx, Product{Name: "Digital", Slug: "digital", Kind: ProductDigital, Active: true, DigitalFile: "../public.txt"}); err == nil {
		t.Fatal("digital path traversal")
	}
}
func TestGenericCartHTTPAndTampering(t *testing.T) {
	e := boot(t)
	p := serviceProduct(t, e, "consulting", 32100)
	// Use the existing product form to obtain a genuine CSRF token. Cart
	// mutation is exercised over the real HTTP router and SQLite database.
	csrf := e.csrfFrom("/product/" + p.Slug)
	form := url.Values{"_csrf": {csrf}, "product_id": {p.ID}, "quantity": {"2"}, "price_minor": {"1"}}
	resp, _ := e.do("POST", "/cart/items", form)
	if resp.StatusCode != 303 {
		t.Fatalf("cart add %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("GET", e.srv.URL+"/cart", nil)
	state := (&publicHandlers{svc: e.svc}).readCart(func() *http.Request {
		for _, c := range e.c.Jar.Cookies(req.URL) {
			req.AddCookie(c)
		}
		return req
	}())
	form = url.Values{"_csrf": {csrf}, "email": {"client@example.test"}, "idempotency_key": {state.Key}, "total_minor": {"1"}}
	resp, _ = e.do("POST", "/checkout", form)
	if resp.StatusCode != 303 || !strings.HasPrefix(resp.Header.Get("Location"), "/checkout/success?o=") {
		t.Fatalf("manual checkout %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	orders, _, _ := e.svc.Orders(context.Background(), ListQuery{})
	if len(orders) != 1 || orders[0].TotalMinor != 64200 || orders[0].Status != OrderPending {
		t.Fatalf("tampered total used %+v", orders)
	}
	resp, _ = e.do("POST", "/checkout", form)
	if resp.StatusCode != 303 || e.count(`SELECT count(*) FROM orders`) != 1 {
		t.Fatal("browser checkout replay duplicated")
	}
	resp, _ = e.do("POST", "/cart/items", url.Values{"product_id": {p.ID}, "quantity": {"1"}})
	if resp.StatusCode != 403 {
		t.Fatalf("missing CSRF allowed %d", resp.StatusCode)
	}
	forged := &http.Request{Header: http.Header{}}
	forged.AddCookie(&http.Cookie{Name: cartCookie, Value: "eyJpdGVtcyI6W119.forged"})
	if len((&publicHandlers{svc: e.svc}).readCart(forged).Items) != 0 {
		t.Fatal("unsigned cart accepted")
	}
}
func TestDigitalDeliveryOwnershipPaymentExpiry(t *testing.T) {
	e := boot(t)
	ctx := context.Background()
	dir := e.svc.digitalDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	key := "private-invoice.zip"
	if err := os.WriteFile(filepath.Join(dir, key), []byte("private-test-asset"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.SaveProduct(ctx, Product{Kind: ProductDigital, Name: "Digital", Slug: "digital", Active: true, PriceMinor: 4900, Currency: "AED", DigitalFile: key, DownloadDays: 1})
	if err != nil {
		t.Fatal(err)
	}
	id := e.checkout(p, "buyer@example.test")
	o, _ := e.svc.Order(ctx, id)
	items, _ := e.svc.orderItems(ctx, o)
	path := "/account/download/" + id + "/" + items[0].ID
	resp, _ := e.do("GET", path, nil)
	if resp.StatusCode != 404 {
		t.Fatal("anonymous delivery allowed")
	}
	var uid string
	if err = e.svc.db.QueryRowContext(ctx, `SELECT user_id FROM customers WHERE id=?`, o.CustomerID).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	e.signInCustomer(uid)
	resp, _ = e.do("GET", path, nil)
	if resp.StatusCode != 404 {
		t.Fatal("unpaid delivery allowed")
	}
	b, sig := stripeEvent("evt_digital_paid", evCheckoutCompleted, completedSession(id))
	if e.webhook(b, sig).StatusCode != 200 {
		t.Fatal("fake signed Stripe rejected")
	}
	if e.count(`SELECT count(*) FROM jobs WHERE kind='commerce.provision'`) != 0 {
		t.Fatal("digital purchase provisioned")
	}
	resp, body := e.do("GET", path, nil)
	if resp.StatusCode != 200 || body != "private-test-asset" || !strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("delivery %d %q", resp.StatusCode, body)
	}
	stranger := serviceProduct(t, e, "other-service", 0)
	other, err := e.svc.SubmitCart(ctx, CartCheckout{Items: []CartItem{{stranger.ID, 1}}, Email: "stranger@example.test", IdempotencyKey: "abcdefghijklmnop-stranger"})
	if err != nil {
		t.Fatal(err)
	}
	var otherID string
	_ = e.svc.db.QueryRowContext(ctx, `SELECT user_id FROM customers WHERE id=?`, other.CustomerID).Scan(&otherID)
	e.signInCustomer(otherID)
	resp, _ = e.do("GET", path, nil)
	if resp.StatusCode != 404 {
		t.Fatal("other customer downloaded")
	}
	e.signInCustomer(uid)
	e.svc.now = func() time.Time { return time.Now().Add(48 * time.Hour) }
	resp, _ = e.do("GET", path, nil)
	if resp.StatusCode != 404 {
		t.Fatal("expired delivery allowed")
	}
	e.svc.now = time.Now
	_, _ = e.svc.db.ExecContext(ctx, `UPDATE orders SET status='refunded' WHERE id=?`, id)
	resp, _ = e.do("GET", path, nil)
	if resp.StatusCode != 404 {
		t.Fatal("refunded delivery allowed")
	}
	resp, _ = e.do("GET", "/media/"+key, nil)
	if resp.StatusCode == 200 {
		t.Fatal("private asset public media exposure")
	}
}

func TestServiceFulfillmentDoesNotAssertPayment(t *testing.T) {
	e := boot(t)
	ctx := context.Background()
	p := serviceProduct(t, e, "manual-service", 0)
	o, err := e.svc.SubmitCart(ctx, CartCheckout{Items: []CartItem{{p.ID, 1}}, Email: "client@example.test", IdempotencyKey: "abcdefghijklmnop-fulfillment"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.svc.UpdateRequest(ctx, o.ID, RequestUpdate{Status: "fulfilled"}); err == nil {
		t.Fatal("pending directly fulfilled")
	}
	for _, state := range []string{"accepted", "in_progress", "fulfilled"} {
		o, err = e.svc.UpdateRequest(ctx, o.ID, RequestUpdate{Status: state, Note: "Operator note"})
		if err != nil {
			t.Fatal(err)
		}
	}
	if o.RequestStatus != "fulfilled" || o.Status != OrderPending || o.PaidAt != "" || e.count(`SELECT count(*) FROM jobs WHERE kind='commerce.provision'`) != 0 {
		t.Fatalf("fulfillment changed financial/provisioning state %+v", o)
	}
	if _, err = e.svc.UpdateRequest(ctx, o.ID, RequestUpdate{Status: "accepted"}); err == nil {
		t.Fatal("terminal request reopened")
	}
	dir := e.svc.digitalDir()
	_ = os.MkdirAll(dir, 0700)
	_ = os.WriteFile(filepath.Join(dir, "file.zip"), []byte("fixture"), 0600)
	digital, err := e.svc.SaveProduct(ctx, Product{Kind: ProductDigital, Name: "Download", Slug: "download", Currency: "AED", Active: true, DigitalFile: "file.zip"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := e.svc.SubmitCart(ctx, CartCheckout{Items: []CartItem{{digital.ID, 1}}, Email: "client@example.test", IdempotencyKey: "abcdefghijklmnop-digitalrequest"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.svc.UpdateRequest(ctx, d.ID, RequestUpdate{Status: "accepted"}); err != nil {
		t.Fatal(err)
	}
	if _, err = e.svc.UpdateRequest(ctx, d.ID, RequestUpdate{Status: "fulfilled"}); err == nil {
		t.Fatal("unpaid digital request fulfilled")
	}
}

func TestAdminGenericProductReadinessAndPrivateUpload(t *testing.T) {
	e := boot(t)
	ctx := context.Background()
	p := serviceProduct(t, e, "human-service", 0)
	missing, err := e.svc.SaveProduct(ctx, Product{Kind: ProductDigital, Name: "Missing file product", Slug: "missing-file", Currency: "AED"})
	if err != nil {
		t.Fatal(err)
	}
	e.signInAdmin("manager@example.test", "admin")
	resp, page := e.do("GET", "/admin/commerce/products", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("list rendering: %d", resp.StatusCode)
	}
	start := strings.Index(page, "/product/"+p.Slug)
	if start < 0 {
		t.Fatal("service absent")
	}
	row := page[start:]
	row = row[:strings.Index(row, "</tr>")]
	if strings.Contains(row, "1m") || strings.Contains(row, "Needs mapping") || strings.Contains(row, "غير مربوط") {
		t.Fatalf("service has provisioning hints: %s", row)
	}
	if !strings.Contains(page, "Missing file product") || (!strings.Contains(page, "الملف الخاص غير موجود") && !strings.Contains(page, "Private file missing")) {
		t.Fatal("missing private artifact not indicated")
	}
	path := "/admin/commerce/products/" + missing.ID
	resp, page = e.do("GET", path, nil)
	if resp.StatusCode != 200 || !strings.Contains(page, "digital-upload-file") {
		t.Fatalf("editor/upload rendering %d", resp.StatusCode)
	}
	csrf := csrfFieldRe.FindStringSubmatch(page)
	if len(csrf) != 2 {
		t.Fatal("editor missing CSRF")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("_csrf", csrf[1])
	file, err := writer.CreateFormFile("file", "invoice.zip")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte("private-delivery-fixture"))
	_ = writer.Close()
	req, _ := http.NewRequest("POST", e.srv.URL+path+"/digital-file", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "text/html")
	req.Header.Set("X-CSRF-Token", csrf[1])
	res, err := e.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), "digital_file") {
		t.Fatalf("upload HTML %d %s", res.StatusCode, string(b))
	}
	re := regexp.MustCompile(`id="p-digital" name="digital_file" value="([^"]+)"`)
	matches := re.FindStringSubmatch(string(b))
	if len(matches) != 2 || !e.svc.digitalFileExists(matches[1]) {
		t.Fatalf("uploaded private key not reflected: %s", string(b))
	}
	stored, err := e.svc.Product(ctx, missing.Slug)
	if err != nil || stored.DigitalFile != "" || stored.Active {
		t.Fatal("upload implicitly modified/activated product")
	}
	st, err := os.Stat(filepath.Join(e.svc.digitalDir(), matches[1]))
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("private file permissions")
	}
	resp, _ = e.do("GET", "/media/"+matches[1], nil)
	if resp.StatusCode == 200 {
		t.Fatal("uploaded private artifact publicly served")
	}
	resp, page = e.do("POST", path+"/digital-file", url.Values{"_csrf": {csrf[1]}}, "Accept", "text/html")
	if resp.StatusCode != 422 || (!strings.Contains(page, "اختر ملف تسليم") && !strings.Contains(page, "Choose a non-empty")) {
		t.Fatalf("native upload error %d %s", resp.StatusCode, page)
	}
}
