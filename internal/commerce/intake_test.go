package commerce

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestServiceIntakeValidationSnapshotAndOwner(t *testing.T) {
	e := boot(t)
	ctx := context.Background()
	p := serviceProduct(t, e, "comparison", 1900)
	p.Intake = []IntakeField{{Key: "tool_a", Label: "First tool", Required: true, MaxLength: 120}, {Key: "context", Label: "Use case", Required: true, MaxLength: 1200}}
	p, err := e.svc.SaveProduct(ctx, *p)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = e.svc.StartCheckout(ctx, p.ID, "buyer@example.test"); err == nil {
		t.Fatal("payment endpoint bypassed required intake")
	}
	req := CartCheckout{Items: []CartItem{{p.ID, 1}}, Email: "buyer@example.test", IdempotencyKey: "comparison-intake-request-key"}
	if _, err = e.svc.SubmitCart(ctx, req); err == nil {
		t.Fatal("missing required intake accepted")
	}
	if e.count(`SELECT count(*) FROM orders`) != 0 {
		t.Fatal("invalid intake wrote an order")
	}
	req.Intake = map[string]map[string]string{p.ID: {"tool_a": strings.Repeat("أ", 121), "context": "Use case"}}
	if _, err = e.svc.SubmitCart(ctx, req); err == nil {
		t.Fatal("length overflow accepted")
	}
	req.Intake[p.ID]["tool_a"] = "Tool A"
	req.Intake[p.ID]["unexpected"] = "secret"
	if _, err = e.svc.SubmitCart(ctx, req); err == nil {
		t.Fatal("unknown key accepted")
	}
	delete(req.Intake[p.ID], "unexpected")
	o, err := e.svc.SubmitCart(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	again, err := e.svc.SubmitCart(ctx, req)
	if err != nil || again.ID != o.ID {
		t.Fatal("replay failed", err)
	}
	req.Intake[p.ID]["tool_a"] = "Another tool"
	if _, err = e.svc.SubmitCart(ctx, req); err == nil {
		t.Fatal("changed intake replay accepted")
	}
	p.Intake[0].Label = "Changed label"
	if _, err = e.svc.SaveProduct(ctx, *p); err != nil {
		t.Fatal(err)
	}
	items, err := e.svc.orderItems(ctx, o)
	if err != nil || len(items) != 1 || items[0].Intake["tool_a"] != "Tool A" || items[0].IntakeSchema[0].Label != "First tool" {
		t.Fatalf("frozen contract changed: %+v %v", items, err)
	}
	var uid string
	if err = e.svc.db.QueryRowContext(ctx, `SELECT user_id FROM customers WHERE id=?`, o.CustomerID).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	e.signInCustomer(uid)
	resp, body := e.do("GET", "/account", nil)
	if resp.StatusCode != 200 || !strings.Contains(body, "First tool") || !strings.Contains(body, "Tool A") {
		t.Fatal("owner cannot view intake")
	}
	_, other, err := e.svc.ensureCustomer(ctx, "other@example.test")
	if err != nil {
		t.Fatal(err)
	}
	e.signInCustomer(other.ID)
	_, body = e.do("GET", "/account", nil)
	if strings.Contains(body, "Tool A") {
		t.Fatal("intake exposed to another customer")
	}
	e.signInAdmin("manager@example.test", "admin")
	resp, body = e.do("GET", "/admin/commerce/orders/"+o.ID, nil)
	if resp.StatusCode != 200 || !strings.Contains(body, "First tool") || !strings.Contains(body, "Tool A") {
		t.Fatal("operator cannot read frozen brief")
	}
	if len(e.mail.all()) != 0 || len(e.stripe.reqs) != 0 {
		t.Fatal("intake sent mail or payment")
	}
}

func TestServiceIntakeHTMLAndSchema(t *testing.T) {
	e := boot(t)
	p := serviceProduct(t, e, "intake", 100)
	p.Intake = []IntakeField{{Key: "needs", Label: "Your needs", Required: true, MaxLength: 120}}
	var err error
	p, err = e.svc.SaveProduct(context.Background(), *p)
	if err != nil {
		t.Fatal(err)
	}
	csrf := e.csrfFrom("/product/" + p.Slug)
	resp, _ := e.do("POST", "/cart/items", url.Values{"_csrf": {csrf}, "product_id": {p.ID}, "quantity": {"1"}})
	if resp.StatusCode != 303 {
		t.Fatal("cart add failed")
	}
	resp, body := e.do("GET", "/checkout", nil)
	if resp.StatusCode != 200 || !strings.Contains(body, "intake."+p.ID+".needs") || !strings.Contains(body, "Your needs") {
		t.Fatal("intake form absent")
	}

	state := (&publicHandlers{svc: e.svc}).readCart(func() *http.Request {
		r, _ := http.NewRequest("GET", e.srv.URL+"/checkout", nil)
		for _, c := range e.c.Jar.Cookies(r.URL) {
			r.AddCookie(c)
		}
		return r
	}())
	form := url.Values{"_csrf": {csrf}, "idempotency_key": {state.Key}, "email": {"buyer@example.test"}, "name": {"Buyer"}}
	resp, body = e.do("POST", "/checkout", form)
	if resp.StatusCode != 422 || !strings.Contains(body, "buyer@example.test") {
		t.Fatal("invalid intake accepted or contact lost")
	}
	form.Set("intake."+p.ID+".needs", "A real submitted brief")
	resp, _ = e.do("POST", "/checkout", form)
	if resp.StatusCode != 303 {
		t.Fatal("valid HTML intake rejected")
	}
	var snapshotJSON string
	if err = e.svc.db.QueryRowContext(context.Background(), `SELECT snapshot_json FROM order_items`).Scan(&snapshotJSON); err != nil || !strings.Contains(snapshotJSON, "A real submitted brief") {
		t.Fatal("HTML intake not persisted", err)
	}
	if validIntakeSchema([]IntakeField{{Key: "../../bad", Label: "Bad", MaxLength: 120}}) {
		t.Fatal("bad key accepted")
	}
	if validIntakeSchema([]IntakeField{{Key: "needs", Label: "A", MaxLength: 120}, {Key: "needs", Label: "B", MaxLength: 120}}) {
		t.Fatal("duplicate key accepted")
	}
}

func TestIntakeAdditionPreservesLegacyRequestReplay(t *testing.T) {
	e := boot(t)
	ctx := context.Background()
	p := serviceProduct(t, e, "legacy-request", 100)
	req := CartCheckout{Items: []CartItem{{p.ID, 1}}, Email: "buyer@example.test", IdempotencyKey: "legacy-request-replay-key"}
	o, err := e.svc.SubmitCart(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	cart, err := quoteCart(ctx, e.svc.db, req.Items)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(struct {
		Cart               *Cart
		Email, Name, Notes string
	}{cart, req.Email, "", ""})
	hash := sha256.Sum256(raw)
	if _, err = e.svc.db.ExecContext(ctx, `UPDATE orders SET checkout_hash=? WHERE id=?`, hex.EncodeToString(hash[:]), o.ID); err != nil {
		t.Fatal(err)
	}
	again, err := e.svc.SubmitCart(ctx, req)
	if err != nil || again.ID != o.ID {
		t.Fatalf("legacy replay broken: %v", err)
	}
}

func TestServiceIntakeRejectsStoredWhitespaceOverflow(t *testing.T) {
	e := boot(t)
	p := serviceProduct(t, e, "padded-intake", 100)
	p.Intake = []IntakeField{{Key: "needs", Label: "Needs", Required: true, MaxLength: 5}}
	p, err := e.svc.SaveProduct(context.Background(), *p)
	if err != nil {
		t.Fatal(err)
	}
	req := CartCheckout{Items: []CartItem{{p.ID, 1}}, Email: "padded@example.test", IdempotencyKey: "padded-intake-request", Intake: map[string]map[string]string{p.ID: {"needs": "  ok  "}}}
	if _, err = e.svc.SubmitCart(context.Background(), req); err == nil {
		t.Fatal("stored value exceeded configured length")
	}
	if e.count(`SELECT count(*) FROM orders`) != 0 {
		t.Fatal("overflow wrote an order")
	}
	req.Intake[p.ID]["needs"] = " ok "
	order, err := e.svc.SubmitCart(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	items, err := e.svc.orderItems(context.Background(), order)
	if err != nil || len(items) != 1 || items[0].Intake["needs"] != " ok " {
		t.Fatal("valid input was normalized", items, err)
	}
	replay, err := e.svc.SubmitCart(context.Background(), req)
	if err != nil || replay.ID != order.ID {
		t.Fatal("same raw input failed replay", err)
	}
	req.Intake[p.ID]["needs"] = "ok"
	if _, err = e.svc.SubmitCart(context.Background(), req); err == nil {
		t.Fatal("changed raw fingerprint replayed")
	}
}
