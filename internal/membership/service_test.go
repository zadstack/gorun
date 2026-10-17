package membership

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

func setup(t *testing.T) (*Service, content.Service, context.Context, context.Context, context.Context, *kernel.App) {
	t.Helper()
	m := New()
	app, e := kernel.BootWithOptions(context.Background(), config.Config{Env: "dev", DataDir: t.TempDir(), BaseURL: "http://localhost", SecretKey: make([]byte, 32)}, []kernel.Module{settingsmod.New(), auth.New(), content.New(), api.New(), admin.New(), m}, kernel.BootOptions{})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(app.Close)
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	makeUser := func(email, role string) context.Context {
		u, e := users.Create(context.Background(), email, email, "correct horse battery", role)
		if e != nil {
			t.Fatal(e)
		}
		return auth.WithUser(context.Background(), u)
	}
	staff := makeUser("staff@example.test", auth.RoleAdmin)
	alice := makeUser("alice@example.test", "author")
	bob := makeUser("bob@example.test", "author")
	cs, _ := kernel.Get[content.Service](app.Deps, kernel.ServiceContent)
	for _, typ := range []string{"ai-tool", "app-project"} {
		if e = cs.SaveType(staff, content.Type{Slug: typ, Name: map[string]string{"en": typ}, Public: true, Supports: []string{"title", "body", "author"}}); e != nil {
			t.Fatal(e)
		}
	}
	_, e = app.Deps.DB.Exec(`INSERT INTO membership_policies VALUES('ai-tool',3,'membership.unlimited'),('app-project',1,'membership.unlimited')`)
	if e != nil {
		t.Fatal(e)
	}
	m.Service.Now = func() time.Time { return time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC) }
	if e = m.Service.SavePlan(staff, Plan{ID: "paid", Name: "Paid", Limits: map[string]int64{"ai-tool": 50, "app-project": 20}}); e != nil {
		t.Fatal(e)
	}
	return m.Service, cs, staff, alice, bob, app
}
func TestMembershipStatePeriodsAndExplicitCancellation(t *testing.T) {
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ status, start, end, grace, want string }{
		{"active", "", "2026-10-15T00:00:00Z", "", "active"},
		{"active", "2026-10-15T00:00:00Z", "2026-11-15T00:00:00Z", "", "pending"},
		{"active", "", "2026-10-13T00:00:00Z", "2026-10-16T00:00:00Z", "grace"},
		{"pending-renewal", "", "2026-10-13T00:00:00Z", "2026-10-16T00:00:00Z", "grace"},
		{"cancelled", "", "2026-10-15T00:00:00Z", "2026-10-18T00:00:00Z", "active"},
		{"cancelled", "", "2026-10-13T00:00:00Z", "2026-10-16T00:00:00Z", "grace"},
		{"cancelled", "", "2026-10-10T00:00:00Z", "2026-10-13T00:00:00Z", "expired"},
		{"suspended", "", "2026-10-15T00:00:00Z", "", "suspended"},
		{"expired", "", "2027-10-15T00:00:00Z", "", "expired"},
		{"active", "", "broken", "", "invalid"},
	} {
		g := Grant{Status: tc.status, PeriodStart: tc.start, PeriodEnd: tc.end, GraceUntil: tc.grace}
		if got := g.Effective(now); got != tc.want {
			t.Errorf("%+v got %s", tc, got)
		}
	}
}
func TestMembershipOwnershipManagementCASAndExpiredQuota(t *testing.T) {
	s, _, staff, alice, bob, _ := setup(t)
	q := GrantInput{Grant: Grant{OwnerID: actor(alice), PlanID: "paid", Status: "active", PeriodEnd: "2027-07-13T00:00:00Z"}}
	if _, e := s.SaveGrant(alice, q); !errors.Is(e, content.ErrForbidden) {
		t.Fatal(e)
	}
	g, e := s.SaveGrant(staff, q)
	if e != nil {
		t.Fatal(e)
	}
	o, e := s.Overview(alice, false)
	if e != nil || len(o.Grants) != 1 || o.Grants[0].PeriodEnd != q.PeriodEnd {
		t.Fatal(o, e)
	}
	for _, quota := range o.Quotas {
		if quota.Type == "ai-tool" && quota.Limit != 50 {
			t.Fatal(quota)
		}
	}
	o, e = s.Overview(bob, false)
	if e != nil || len(o.Grants) != 0 {
		t.Fatal(o, e)
	}
	if _, e = s.Overview(bob, true); !errors.Is(e, content.ErrForbidden) {
		t.Fatal(e)
	}
	disabled := auth.WithUser(context.Background(), &auth.User{ID: actor(alice), Role: "author", Status: "disabled"})
	if _, e = s.Overview(disabled, false); !errors.Is(e, content.ErrForbidden) {
		t.Fatal(e)
	}
	q = GrantInput{Grant: g, ExpectedVersion: "wrong"}
	q.Status = "suspended"
	if _, e = s.SaveGrant(staff, q); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	q.ExpectedVersion = g.Version
	q.OwnerID = actor(bob)
	if _, e = s.SaveGrant(staff, q); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	q.OwnerID = g.OwnerID
	g, e = s.SaveGrant(staff, q)
	if e != nil {
		t.Fatal(e)
	}
	o, e = s.Overview(alice, false)
	if e != nil {
		t.Fatal(e)
	}
	for _, quota := range o.Quotas {
		if quota.Type == "ai-tool" && quota.Limit != 3 {
			t.Fatal(quota)
		}
	}
	if g.Version == q.ExpectedVersion {
		t.Fatal("version unchanged")
	}
}
func TestContentQuotaAtomicConcurrentRestoreAndAdminBypass(t *testing.T) {
	_, cs, staff, alice, _, app := setup(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	created := []*content.Item{}
	denied := 0
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			it, e := cs.Create(alice, content.Input{Type: "ai-tool", Title: fmt.Sprint("Tool ", i), Status: "draft"})
			mu.Lock()
			defer mu.Unlock()
			if e == nil {
				created = append(created, it)
			} else {
				var validation *content.ValidationError
				if errors.As(e, &validation) {
					denied++
				} else {
					t.Errorf("unexpected %v", e)
				}
			}
		}(i)
	}
	wg.Wait()
	if len(created) != 3 || denied != 9 {
		t.Fatalf("created %d denied %d", len(created), denied)
	}
	// Existing content can still be edited after reaching its quota.
	in := content.Input{Type: "ai-tool", Title: created[0].Title, Status: "draft", AuthorID: actor(alice)}
	in.Title = "Edited at quota"
	if _, e := cs.Update(alice, created[0].ID, in); e != nil {
		t.Fatal(e)
	}
	if e := cs.Delete(staff, created[0].ID, false); e != nil {
		t.Fatal(e)
	}
	if _, e := cs.Create(alice, content.Input{Type: "ai-tool", Title: "Replacement", Status: "pending"}); e != nil {
		t.Fatal(e)
	}
	// Restore through the same native write boundary cannot exceed the quota.
	if _, e := app.Deps.DB.Exec(`INSERT OR IGNORE INTO role_permissions VALUES('author','content.delete')`); e != nil {
		t.Fatal(e)
	}
	auth.ReloadRoles(context.Background(), app.Deps.DB)
	if _, e := cs.Update(alice, created[0].ID, in); e == nil {
		t.Fatal("restore bypassed quota")
	}
	if _, e := cs.Create(staff, content.Input{Type: "ai-tool", Title: "Explicit admin override", AuthorID: actor(alice), Status: "draft"}); e != nil {
		t.Fatal(e)
	}
	// Unconfigured content types retain existing behavior.
	if _, e := cs.Create(alice, content.Input{Type: "post", Title: "Unscoped post", Status: "draft"}); e != nil {
		t.Fatal(e)
	}
}
func TestMembershipPublicPageAnonymousAndEscaping(t *testing.T) {
	s, _, staff, alice, bob, app := setup(t)
	_, e := s.SaveGrant(staff, GrantInput{Grant: Grant{OwnerID: actor(alice), PlanID: "paid", Status: "active", PeriodEnd: "2027-01-01T00:00:00Z"}})
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		ctx  context.Context
		want int
		has  bool
	}{{context.Background(), 303, false}, {alice, 200, true}, {bob, 200, false}} {
		w := httptest.NewRecorder()
		app.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/membership", nil).WithContext(tc.ctx))
		if w.Code != tc.want {
			t.Fatal(w.Code, w.Body.String())
		}
		if tc.want == 200 && strings.Contains(w.Body.String(), "2027-01-01") != tc.has {
			t.Fatal("owner isolation")
		}
	}
}

func TestMembershipAPIReadScopesManageAndCSRF(t *testing.T) {
	s, _, staff, alice, bob, app := setup(t)
	if _, e := s.SaveGrant(staff, GrantInput{Grant: Grant{OwnerID: actor(alice), PlanID: "paid", Status: "active", PeriodEnd: "2027-01-01T00:00:00Z"}}); e != nil {
		t.Fatal(e)
	}
	keys, _ := kernel.Get[auth.KeyService](app.Deps, kernel.ServiceAPIKeys)
	token := func(ctx context.Context, scopes []string) string {
		k, _, e := keys.Create(staff, "membership acceptance", actor(ctx), scopes, 60, "")
		if e != nil {
			t.Fatal(e)
		}
		return k
	}
	read := token(alice, []string{"membership.read"})
	other := token(bob, []string{"membership.read"})
	limited := token(alice, []string{"content.view"})
	call := func(method, path, body, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		w := httptest.NewRecorder()
		app.Handler.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		key    string
		status int
		has    bool
	}{{read, 200, true}, {other, 200, false}, {limited, 403, false}, {"", 401, false}} {
		w := call("GET", "/api/v1/membership", "", tc.key)
		if w.Code != tc.status {
			t.Fatal(w.Code, w.Body.String())
		}
		if tc.status == 200 && strings.Contains(w.Body.String(), "2027-01-01") != tc.has {
			t.Fatal("API ownership")
		}
	}
	w := call("POST", "/api/v1/membership/grants", `{"owner_id":"other","plan_id":"paid","status":"active"}`, read)
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/admin/membership/grants", strings.NewReader("owner_id=ignored")).WithContext(staff)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	app.Handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("missing CSRF accepted", w.Code)
	}
}
func TestMembershipPaidGraceExpiredContentBoundary(t *testing.T) {
	s, cs, staff, alice, _, _ := setup(t)
	g, e := s.SaveGrant(staff, GrantInput{Grant: Grant{OwnerID: actor(alice), PlanID: "paid", Status: "pending-renewal", PeriodEnd: "2026-10-13T00:00:00Z", GraceUntil: "2026-10-16T00:00:00Z"}})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 4; i++ {
		if _, e = cs.Create(alice, content.Input{Type: "ai-tool", Title: fmt.Sprint(i), Status: "draft"}); e != nil {
			t.Fatal(e)
		}
	}
	q := GrantInput{Grant: g, ExpectedVersion: g.Version}
	q.Status = "expired"
	if _, e = s.SaveGrant(staff, q); e != nil {
		t.Fatal(e)
	}
	if _, e = cs.Create(alice, content.Input{Type: "ai-tool", Title: "Above free quota", Status: "draft"}); e == nil {
		t.Fatal("expired membership granted paid capacity")
	}
}

func TestScopedCustomerSubmissionsRejectSpoofAndRespectQuota(t *testing.T) {
	s, cs, staff, _, _, app := setup(t)
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	customer, e := users.Create(context.Background(), "submitter@example.test", "Submitter", "correct horse battery", auth.RoleCustomer)
	if e != nil {
		t.Fatal(e)
	}
	other, e := users.Create(context.Background(), "other-submitter@example.test", "Other", "correct horse battery", auth.RoleCustomer)
	if e != nil {
		t.Fatal(e)
	}
	ctx := auth.WithUser(context.Background(), customer)
	otherctx := auth.WithUser(context.Background(), other)
	if auth.Can(ctx, "content.create") || auth.Can(ctx, "content.publish") {
		t.Fatal("generic content permissions expanded")
	}
	if _, e = s.Submit(ctx, cs, SubmissionInput{Type: "post", Title: "Unconfigured type"}); !errors.Is(e, content.ErrForbidden) {
		t.Fatal(e)
	}
	v, e := s.Submit(ctx, cs, SubmissionInput{Type: "app-project", Title: "Owned pending project", Body: `<script>bad()</script><p>Safe</p>`})
	if e != nil {
		t.Fatal(e)
	}
	it, e := cs.Get(staff, v.ID)
	if e != nil || it.AuthorID != customer.ID || it.Status != "pending" || strings.Contains(it.Body, "<script") {
		t.Fatal(it, e)
	}
	if _, e = s.Submit(ctx, cs, SubmissionInput{Type: "app-project", Title: "Over quota"}); e == nil {
		t.Fatal("quota exceeded")
	}
	list, e := s.Submissions(otherctx)
	if e != nil || len(list) != 0 {
		t.Fatal(list, e)
	}
	list, e = s.Submissions(ctx)
	if e != nil || len(list) != 1 {
		t.Fatal(list, e)
	}
	keys, _ := kernel.Get[auth.KeyService](app.Deps, kernel.ServiceAPIKeys)
	key, _, e := keys.Create(staff, "Submission API", customer.ID, []string{"membership.submit"}, 60, "")
	if e != nil {
		t.Fatal(e)
	}
	for _, body := range []string{`{"type":"ai-tool","title":"Spoof","author_id":"other"}`, `{"type":"ai-tool","title":"Spoof","status":"published"}`} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/v1/membership/submissions", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("Content-Type", "application/json")
		app.Handler.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/membership/submissions", strings.NewReader(`{"type":"ai-tool","title":"API pending","body":"safe"}`))
	r.Header.Set("Authorization", "Bearer "+key)
	r.Header.Set("Content-Type", "application/json")
	app.Handler.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"pending"`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
