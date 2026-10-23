package commerce

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/webhook"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/commerce/fakebilling"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"github.com/zadstack/gorun/internal/site"
	"github.com/zadstack/gorun/internal/theme"
	"github.com/zadstack/gorun/internal/theme/fake"
)

func TestMain(m *testing.M) {
	kernel.LogOutput = io.Discard
	os.Exit(m.Run())
}

const (
	testWebhookSecret = "whsec_test_secret"
	testPassword      = "correct horse battery"
	testClientID      = "gorun-test"
	testServerSecret  = "server-secret"
)

// fakeStripe records checkout requests and returns predictable sessions.
type fakeStripe struct {
	mu   sync.Mutex
	reqs []checkoutRequest
	keys []string
	err  error
}

func (f *fakeStripe) Create(_ context.Context, key string, r checkoutRequest) (checkoutSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return checkoutSession{}, f.err
	}
	f.reqs = append(f.reqs, r)
	f.keys = append(f.keys, key)
	return checkoutSession{ID: "cs_test_" + r.OrderID, URL: "https://checkout.stripe.test/pay/cs_test_" + r.OrderID}, nil
}

func (f *fakeStripe) last(t *testing.T) checkoutRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) == 0 {
		t.Fatal("no checkout session created")
	}
	return f.reqs[len(f.reqs)-1]
}

type sentMail struct{ To, Subject, HTML, Text string }

type mailbox struct {
	mu   sync.Mutex
	sent []sentMail
}

func (m *mailbox) send(_ context.Context, to, subject, html, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, sentMail{to, subject, html, text})
	return nil
}

func (m *mailbox) all() []sentMail {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]sentMail(nil), m.sent...)
}

type env struct {
	t       *testing.T
	app     *kernel.App
	svc     *service
	srv     *httptest.Server
	c       *http.Client
	billing *fakebilling.Server
	stripe  *fakeStripe
	mail    *mailbox
}

// boot starts settings + auth + fake content services + theme (repo
// themes/) + site + api + admin + commerce, with Stripe and mail faked and
// a fake billing server. Job workers are stopped right after boot so tests
// run job handlers deterministically (drain).
func boot(t *testing.T) *env { return bootCfg(t, nil) }

// bootCfg is boot with a config tweak applied before the kernel starts.
func bootCfg(t *testing.T, tweak func(*config.Config)) *env {
	t.Helper()
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: []byte("0123456789abcdef0123456789abcdef"),
		BaseURL: "http://shop.test", Theme: "default", AllowPrivateNet: true}
	cfg.Stripe.SecretKey = "sk_test_config"
	cfg.Stripe.WebhookSecret = testWebhookSecret
	if tweak != nil {
		tweak(&cfg)
	}
	fs, mb := &fakeStripe{}, &mailbox{}
	mod := New()
	mod.checkout, mod.sendMail = fs, mb.send
	ctx, stopWorkers := context.WithCancel(context.Background())
	app, err := kernel.Boot(ctx, cfg, []kernel.Module{settingsmod.New(), auth.New(), fake.NewModule(),
		theme.New(theme.WithThemesDir("../../themes")), site.New(), api.New(), admin.New(), mod})
	if err != nil {
		t.Fatal(err)
	}
	stopWorkers()
	billing := fakebilling.New(testClientID, testServerSecret)
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(func() { srv.Close(); billing.Close(); app.Close() })
	return &env{t: t, app: app, svc: mod.svc, srv: srv, c: newClient(), billing: billing, stripe: fs, mail: mb}
}

func newClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (e *env) do(method, path string, form url.Values, headers ...string) (*http.Response, string) {
	e.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := e.c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

var csrfFieldRe = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)

// csrfFrom GETs path and returns the first _csrf form value on it.
func (e *env) csrfFrom(path string) string {
	e.t.Helper()
	_, page := e.do("GET", path, nil)
	m := csrfFieldRe.FindStringSubmatch(page)
	if m == nil {
		e.t.Fatalf("no _csrf on %s:\n%s", path, page)
	}
	return m[1]
}

// server registers the fake billing server.
func (e *env) server() *Server {
	e.t.Helper()
	s, err := e.svc.saveServer(context.Background(), Server{Name: "Fake", BaseURL: e.billing.URL, ClientID: testClientID, Secret: testServerSecret})
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

// product creates an active product on server srv.
func (e *env) product(slug string, srv *Server) *Product {
	e.t.Helper()
	p, err := e.svc.SaveProduct(context.Background(), Product{Name: "Plan " + slug, Slug: slug, PriceMinor: 4900, Currency: "AED", Active: true,
		GroupName: "MCP Servers", PlanCode: "pro", ServerID: srv.ID, AccessMonths: 1, Features: []string{"Unlimited tools"}})
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}

// checkout runs StartCheckout and returns the order id.
func (e *env) checkout(p *Product, email string) string {
	e.t.Helper()
	_, id, err := e.svc.StartCheckout(context.Background(), p.ID, email)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

// stripeEvent builds a signed Stripe event delivery.
func stripeEvent(id, typ string, object map[string]any) (body []byte, header string) {
	ev := map[string]any{"id": id, "object": "event", "api_version": stripe.APIVersion, "type": typ, "created": time.Now().Unix(),
		"data": map[string]any{"object": object}}
	body, _ = json.Marshal(ev)
	sp := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: body, Secret: testWebhookSecret, Timestamp: time.Now()})
	return body, sp.Header
}

// completedSession is a paid session matching an order created by
// e.product (4900 AED) through the fake Stripe (session cs_test_{order}).
func completedSession(orderID string) map[string]any {
	return map[string]any{"id": "cs_test_" + orderID, "object": "checkout.session", "client_reference_id": orderID,
		"payment_status": "paid", "payment_intent": "pi_" + orderID, "customer": "cus_1", "mode": "payment",
		"amount_total": 4900, "currency": "aed", "metadata": map[string]any{"order_id": orderID}}
}

// webhook posts a signed event to /webhooks/stripe.
func (e *env) webhook(body []byte, header string) *http.Response {
	e.t.Helper()
	req, _ := http.NewRequest("POST", e.srv.URL+"/webhooks/stripe", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Stripe-Signature", header)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

// pay delivers checkout.session.completed for orderID.
func (e *env) pay(orderID string) {
	e.t.Helper()
	body, h := stripeEvent("evt_paid_"+orderID, evCheckoutCompleted, completedSession(orderID))
	if r := e.webhook(body, h); r.StatusCode != 200 {
		e.t.Fatalf("webhook: %d", r.StatusCode)
	}
}

// drain runs queued jobs of kind with the registered handler (as the
// worker would), returning the handler errors.
func (e *env) drain(kind string) []error {
	e.t.Helper()
	ctx := context.Background()
	handler := map[string]func(context.Context, json.RawMessage) error{JobProvision: e.svc.provision, JobSuspend: e.svc.suspendJob,
		JobMagicMail: e.svc.magicMail, JobResume: e.svc.resumeJob, JobBillingMail: e.svc.billingMail}[kind]
	rows, err := e.app.Deps.DB.Query(`SELECT id, payload_json FROM jobs WHERE kind = ? AND status = 'queued' ORDER BY created_at, id`, kind)
	if err != nil {
		e.t.Fatal(err)
	}
	type job struct{ id, payload string }
	var list []job
	for rows.Next() {
		var j job
		rows.Scan(&j.id, &j.payload)
		list = append(list, j)
	}
	rows.Close()
	var errs []error
	for _, j := range list {
		err := handler(ctx, json.RawMessage(j.payload))
		status := "done"
		if err != nil {
			status = "failed"
			errs = append(errs, err)
		}
		if _, err := e.app.Deps.DB.Exec(`UPDATE jobs SET status = ? WHERE id = ?`, status, j.id); err != nil {
			e.t.Fatal(err)
		}
	}
	return errs
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.app.Deps.DB.QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	return n
}

func (e *env) countNonExampleProducts() int {
	return e.count(`SELECT count(*) FROM products WHERE slug NOT IN ('example-provisioning','example-service','example-digital')`)
}

func (e *env) str(q string, args ...any) string {
	e.t.Helper()
	var s string
	if err := e.app.Deps.DB.QueryRow(q, args...).Scan(&s); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	return s
}

func (e *env) users() auth.Service {
	u, _ := kernel.Get[auth.Service](e.app.Deps, kernel.ServiceAuth)
	return u
}

// signInCustomer logs the jar in as userID through a magic link.
func (e *env) signInCustomer(userID string) {
	e.t.Helper()
	token, err := auth.NewMagicLink(context.Background(), e.app.Deps, userID, 0)
	if err != nil {
		e.t.Fatal(err)
	}
	resp, _ := e.do("POST", "/auth/magic/"+token, url.Values{})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/account" {
		e.t.Fatalf("magic login: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
}

// signInAdmin creates a user with role and logs in at /admin/login.
func (e *env) signInAdmin(email, role string) *auth.User {
	e.t.Helper()
	u, err := e.users().Create(context.Background(), email, "Admin", testPassword, role)
	if err != nil {
		e.t.Fatal(err)
	}
	e.c = newClient()
	_, page := e.do("GET", "/admin/login", nil)
	m := csrfFieldRe.FindStringSubmatch(page)
	if m == nil {
		e.t.Fatalf("no csrf on login page")
	}
	resp, _ := e.do("POST", "/admin/login", url.Values{"_csrf": {m[1]}, "email": {email}, "password": {testPassword}})
	if resp.StatusCode != http.StatusSeeOther {
		e.t.Fatalf("login %s: %d", email, resp.StatusCode)
	}
	return u
}

var csrfMetaRe = regexp.MustCompile(`<meta name="csrf-token" content="([^"]+)"`)

// adminPost posts form with the admin session's CSRF token.
func (e *env) adminPost(path string, form url.Values) (*http.Response, string) {
	e.t.Helper()
	_, page := e.do("GET", "/admin/dashboard", nil)
	m := csrfMetaRe.FindStringSubmatch(page)
	if m == nil {
		e.t.Fatalf("no csrf meta")
	}
	if form == nil {
		form = url.Values{}
	}
	form.Set("_csrf", m[1])
	return e.do("POST", path, form)
}

func mustContain(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Fatalf("body missing %q:\n%s", w, body)
		}
	}
}

func mustNotContain(t *testing.T, body string, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if strings.Contains(body, w) {
			t.Fatalf("body must not contain %q:\n%s", w, body)
		}
	}
}

func auditCount(e *env, action string) int {
	return e.count(`SELECT count(*) FROM audit_log WHERE action = ?`, action)
}

// newFake starts a fake billing server closed with the test.
func newFake(t *testing.T) *fakebilling.Server {
	t.Helper()
	b := fakebilling.New(testClientID, testServerSecret)
	t.Cleanup(b.Close)
	return b
}
