package commerce

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/theme/fake"
)

// urlMedia is the fake media library with FromURL support.
type urlMedia struct {
	*fake.Media
	urls []string
}

func (m *urlMedia) FromURL(_ context.Context, u string, meta media.UploadMeta) (*media.Media, error) {
	m.urls = append(m.urls, u)
	return &media.Media{ID: "m_" + strings.TrimPrefix(filepath.Base(u), "/"), Alt: meta.Alt}, nil
}

func TestUpsertFromWPMapsMCBBMeta(t *testing.T) {
	e := boot(t)
	ctx := context.Background()
	srv := e.server()
	lib := &urlMedia{Media: fake.NewMedia()}
	e.app.Deps.Services.Set(kernel.ServiceMedia, media.Service(lib))

	// PHP plugin meta names (mcp-billing-bridge 0.3.1), server referenced by name.
	wp := WPProduct{ID: 42, Slug: "egypt-research-pro", Name: "Egypt Research — Pro", Description: "<p>Full <b>access</b><script>x</script></p>",
		ShortDescription: "Research MCP", PriceMinor: 9900, Currency: "aed", Categories: []string{"MCP Servers", "Research"},
		ImageURLs: []string{"https://example.org/wp-content/uploads/pro.png"},
		Meta:      map[string]string{"_mcbb_server_id": "Fake", "_mcbb_plan_code": "pro", "_mcbb_months": "3", "_mcbb_usage_limit": "100"}}
	p, err := e.svc.UpsertFromWP(ctx, wp)
	if err != nil {
		t.Fatal(err)
	}
	if p.WPProductID != "42" || p.Slug != "egypt-research-pro" || p.PriceMinor != 9900 || p.Currency != "AED" || p.PlanCode != "pro" ||
		p.AccessMonths != 3 || p.ServerID != srv.ID || p.GroupName != "MCP Servers" || p.GroupSlug != "mcp-servers" || !p.Active {
		t.Fatalf("product = %+v", p)
	}
	if strings.Contains(p.Description, "script") || !strings.Contains(p.Description, "<b>access</b>") {
		t.Fatalf("description = %q", p.Description)
	}
	if p.ImageMediaID != "m_pro.png" || len(lib.urls) != 1 || lib.urls[0] != wp.ImageURLs[0] {
		t.Fatalf("image not imported through media.FromURL: %q %v", p.ImageMediaID, lib.urls)
	}

	// Re-import updates in place and keeps GoRun-side edits (active, sort, SEO, features).
	p.Active, p.Sort, p.Features, p.SEO = false, 7, []string{"Kept"}, map[string]any{"title": "Kept SEO"}
	if _, err := e.svc.SaveProduct(ctx, *p); err != nil {
		t.Fatal(err)
	}
	wp.Name, wp.PriceMinor = "Egypt Research Pro", 12900
	p2, err := e.svc.UpsertFromWP(ctx, wp)
	if err != nil {
		t.Fatal(err)
	}
	if p2.ID != p.ID || p2.Name != "Egypt Research Pro" || p2.PriceMinor != 12900 || p2.Active || p2.Sort != 7 || p2.Features[0] != "Kept" || p2.SEO["title"] != "Kept SEO" {
		t.Fatalf("re-import = %+v", p2)
	}
	if n := e.countNonExampleProducts(); n != 1 {
		t.Fatalf("products = %d", n)
	}

	// Spec meta names, server id given directly, months clamped.
	p3, err := e.svc.UpsertFromWP(ctx, WPProduct{ID: 43, Slug: "team", Name: "Team", PriceMinor: 100, Currency: "AED",
		Meta: map[string]string{"_mcbb_server": srv.ID, "_mcbb_plan": "team", "_mcbb_months": "500"}})
	if err != nil {
		t.Fatal(err)
	}
	if p3.ServerID != srv.ID || p3.PlanCode != "team" || p3.AccessMonths != 120 || !p3.Active {
		t.Fatalf("spec meta = %+v", p3)
	}
	// An unknown WordPress server id cannot map: imported inactive, needs manual mapping.
	p4, err := e.svc.UpsertFromWP(ctx, WPProduct{ID: 44, Name: "Mystery Box", PriceMinor: 100, Currency: "AED",
		Meta: map[string]string{"_mcbb_server_id": "8", "_mcbb_plan_code": "pro"}})
	if err != nil {
		t.Fatal(err)
	}
	if p4.ServerID != "" || p4.Active || p4.Slug != "mystery-box" || p4.AccessMonths != 1 {
		t.Fatalf("unmapped = %+v", p4)
	}
	// An existing hand-made product with the same slug is adopted, not duplicated.
	hand, _ := e.svc.SaveProduct(ctx, Product{Name: "Hand", Slug: "hand-made", PriceMinor: 1, Currency: "AED"})
	p5, err := e.svc.UpsertFromWP(ctx, WPProduct{ID: 45, Slug: "hand-made", Name: "Hand Made", PriceMinor: 2, Currency: "AED"})
	if err != nil || p5.ID != hand.ID || p5.WPProductID != "45" {
		t.Fatalf("adopt = %+v %v", p5, err)
	}
	if _, err := e.svc.UpsertFromWP(ctx, WPProduct{}); err == nil {
		t.Fatal("WP product without id accepted")
	}
}

func TestThemeFuncsAndShopRender(t *testing.T) {
	e := boot(t)
	srv := e.server()
	ctx := context.Background()
	mk := func(name, group string, sort int, active bool) {
		_, err := e.svc.SaveProduct(ctx, Product{Name: name, PriceMinor: 100, Currency: "AED", Active: active, GroupName: group, Sort: sort, PlanCode: "pro", ServerID: srv.ID})
		if err != nil {
			t.Fatal(err)
		}
	}
	mk("Zeta", "Second", 5, true)
	mk("Alpha", "First", 1, true)
	mk("Beta", "First", 2, true)
	mk("Hidden", "First", 0, false)
	fns := e.app.Deps.Theme.Funcs()
	groups := fns["products"].(func() []ProductGroup)()
	if len(groups) != 2 || groups[0].Name != "First" || len(groups[0].Products) != 2 || groups[0].Products[0].Name != "Alpha" || groups[1].Name != "Second" {
		t.Fatalf("groups = %+v", groups)
	}
	product := fns["product"].(func(string) *Product)
	if product("alpha") == nil || product("hidden") != nil || product("nope") != nil {
		t.Fatal("product func must return only active products")
	}
	_, page := e.do("GET", "/shop", nil)
	if strings.Index(page, "Alpha") > strings.Index(page, "Zeta") || strings.Contains(page, "Hidden") {
		t.Fatal("shop order or visibility wrong")
	}
	mustContain(t, page, `data-kind="shop"`, `dir="rtl"`)
	if r, _ := e.do("GET", "/product/hidden", nil); r.StatusCode != 404 {
		t.Fatalf("inactive product page: %d", r.StatusCode)
	}
	// Arabic slugs from WordPress arrive percent-encoded (lowercase hex),
	// in old links and in the WP REST payload.
	encoded := strings.ToLower(url.PathEscape("خادم-بحث"))
	ar, err := e.svc.UpsertFromWP(ctx, WPProduct{ID: 9, Slug: encoded, Name: "خادم بحث", PriceMinor: 100, Currency: "AED",
		Meta: map[string]string{"_mcbb_server_id": srv.ID, "_mcbb_plan_code": "pro"}})
	if err != nil || ar.Slug != "خادم-بحث" {
		t.Fatalf("arabic slug = %+v %v", ar, err)
	}
	for _, path := range []string{"/product/" + encoded, "/product/" + url.PathEscape("خادم-بحث")} {
		r, body := e.do("GET", path, nil)
		if r.StatusCode != 200 || !strings.Contains(body, "خادم بحث") {
			t.Fatalf("%s: %d", path, r.StatusCode)
		}
	}
	// WooCommerce trailing-slash permalinks redirect permanently.
	for from, to := range map[string]string{"/shop/": "/shop", "/product/alpha/": "/product/alpha", "/product/" + encoded + "/?x=1": "/product/" + encoded + "?x=1"} {
		r, _ := e.do("GET", from, nil)
		if r.StatusCode != http.StatusMovedPermanently || r.Header.Get("Location") != to {
			t.Fatalf("%s: %d %s", from, r.StatusCode, r.Header.Get("Location"))
		}
	}
}

func TestPricesMonthsSlugs(t *testing.T) {
	for in, want := range map[string]int64{"49": 4900, "49.5": 4950, "49,50": 4950, "0.01": 1, "": 0} {
		if got, err := ParsePrice(in, "AED"); err != nil || got != want {
			t.Errorf("ParsePrice(%q) = %d, %v", in, got, err)
		}
	}
	for _, bad := range []string{"-1", "1.234", "abc", "1e3x"} {
		if _, err := ParsePrice(bad, "AED"); err == nil {
			t.Errorf("ParsePrice(%q) accepted", bad)
		}
	}
	if got, _ := ParsePrice("1.234", "KWD"); got != 1234 {
		t.Errorf("KWD = %d", got)
	}
	if got, _ := ParsePrice("500", "JPY"); got != 500 {
		t.Errorf("JPY = %d", got)
	}
	if FormatPrice(4950, "AED") != "49.50" || FormatPrice(500, "JPY") != "500" {
		t.Error("FormatPrice")
	}
	jan31 := time.Date(2026, 1, 31, 10, 0, 0, 0, time.UTC)
	if got := addMonths(jan31, 1); !got.Equal(time.Date(2026, 2, 28, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("Jan 31 + 1 = %s", got)
	}
	if got := addMonths(jan31, 13); !got.Equal(time.Date(2027, 2, 28, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("Jan 31 + 13 = %s", got)
	}
	if Slugify("  Egypt Research — Pro!! ") != "egypt-research-pro" || Slugify("خادم بحث") != "خادم-بحث" {
		t.Errorf("Slugify = %q / %q", Slugify("  Egypt Research — Pro!! "), Slugify("خادم بحث"))
	}
}

func TestCommerceAPI(t *testing.T) {
	billing := newFake(t)
	mod := New()
	app := apitest.Boot(t, fake.NewModule(), mod)
	// apitest's config forbids private networks; the fake runs on 127.0.0.1.
	app.Deps.Config.AllowPrivateNet = true
	mod.svc.d.Config.AllowPrivateNet = true
	c := apitest.NewClient(t, app)

	st, body := c.Post("/v1/commerce/servers", map[string]any{"name": "Fake", "base_url": billing.URL, "client_id": testClientID, "secret": testServerSecret})
	if st != 200 {
		t.Fatalf("create server: %d %v", st, body)
	}
	srv := apitest.Data(body)
	if _, leaked := srv["secret"]; leaked || srv["secret_set"] != true {
		t.Fatalf("server = %v", srv)
	}
	id, _ := srv["id"].(string)
	st, body = c.Post("/v1/commerce/servers/"+id+"/test", nil)
	if st != 200 || apitest.Data(body)["ok"] != true {
		t.Fatalf("test: %d %v", st, body)
	}
	st, body = c.Post("/v1/commerce/products", map[string]any{"name": "API Plan", "price_minor": 1000, "currency": "AED", "active": true, "plan_code": "pro", "server_id": id})
	if st != 200 || apitest.Data(body)["slug"] != "api-plan" {
		t.Fatalf("create product: %d %v", st, body)
	}
	if st, body = c.Post("/v1/commerce/products", map[string]any{"name": ""}); st != 422 || apitest.ErrorCode(body) != "validation_failed" {
		t.Fatalf("invalid product: %d %v", st, body)
	}
	if st, _ = c.Get("/v1/commerce/products"); st != 200 {
		t.Fatalf("list: %d", st)
	}
	for _, p := range []string{"/v1/commerce/orders", "/v1/commerce/entitlements", "/v1/commerce/servers"} {
		if st, body = c.Get(p); st != 200 {
			t.Fatalf("%s: %d %v", p, st, body)
		}
	}
	if st, _ = c.Post("/v1/commerce/entitlements/nope/suspend", nil); st != 404 {
		t.Fatalf("unknown entitlement: %d", st)
	}
	// Scopes: a key with only commerce.view_orders reads orders but not products.
	viewer := apitest.NewClient(t, app, "commerce.view_orders")
	if st, _ = viewer.Get("/v1/commerce/orders"); st != 200 {
		t.Fatalf("viewer orders: %d", st)
	}
	if st, _ = viewer.Get("/v1/commerce/products"); st != 403 {
		t.Fatalf("viewer products: %d", st)
	}
	if st, _ = c.Anonymous().Get("/v1/commerce/orders"); st != 401 {
		t.Fatalf("anonymous: %d", st)
	}
	// The operations appear in the OpenAPI document.
	w := c.Do("GET", "/api/v1/openapi.json", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "/v1/commerce/entitlements/{id}/reconcile") {
		t.Fatalf("openapi: %d", w.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
}

// TestCatalogCoversAllKeys checks ar/en parity and that every commerce key
// used by the commerce templates (admin and default theme) and Go code exists.
func TestCatalogCoversAllKeys(t *testing.T) {
	load := func(loc string) map[string]string {
		b, err := os.ReadFile("catalog/commerce." + loc + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	en, ar := load("en"), load("ar")
	for k := range en {
		if _, ok := ar[k]; !ok {
			t.Errorf("ar missing %s", k)
		}
	}
	for k := range ar {
		if _, ok := en[k]; !ok {
			t.Errorf("en missing %s", k)
		}
	}
	keyRe := regexp.MustCompile(`"commerce\.([a-z_]+(?:\.[a-z_]+)+)"`)
	notCatalog := regexp.MustCompile(`^(product\.(save|delete)|product\.media_replaced|order\.(create|paid|paid_closed|refunded|provision_failed|retry|cancelled|payment_failed|mismatch)|entitlement\.(activated|failed|suspend|suspend_failed|suspended_remote|expire|resume|resume_failed|reconcile|login_link)|subscription\.save|server\.(save|delete|test)|settings\.save|stripe_secret_key|stripe_webhook_secret|stripe_test_secret_key|stripe_test_webhook_secret|stripe_mode|grace_days|automatic_tax|order\.paid|entitlement\.activated)$`)
	var files []string
	for _, glob := range []string{"*.go", "../../web/admin/templates/pages/commerce/*.html"} {
		m, _ := filepath.Glob(glob)
		files = append(files, m...)
	}
	for _, f := range []string{"shop", "product", "account", "checkout-result"} {
		files = append(files, "../../themes/default/templates/"+f+".html")
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, m := range keyRe.FindAllStringSubmatch(string(b), -1) {
			if notCatalog.MatchString(m[1]) {
				continue
			}
			if _, ok := en[m[1]]; !ok {
				t.Errorf("%s: missing catalog key commerce.%s", f, m[1])
			}
		}
	}
	dynamic := map[string][]string{"order.status.": orderStatuses, "order.help.": orderStatuses, "entitlement.status.": entitlementStatuses,
		"server.status.": {ServerUnknown, ServerOK, ServerError}, "nav.": {"products", "orders", "entitlements", "servers", "settings"}}
	for prefix, vals := range dynamic {
		for _, v := range vals {
			if _, ok := en[prefix+v]; !ok {
				t.Errorf("missing catalog key commerce.%s%s", prefix, v)
			}
		}
	}
}
