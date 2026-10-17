package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/db"
)

func TestAPIKeyHashScopeAndRateLimit(t *testing.T) {
	d := newDB(t)
	s := testService(d)
	u := mustUser(t, s, "bot@example.com", "editor")
	ks := testKeys(d)
	ctx := context.Background()
	plain, k, err := ks.Create(ctx, "ci", u.ID, []string{"content.view", "users.manage"}, 2, "")
	if err != nil || !strings.HasPrefix(plain, "gp_") || len(plain) != 43 || k.Prefix != plain[3:11] {
		t.Fatalf("%q %+v %v", plain, k, err)
	}
	var stored string
	d.QueryRow(`SELECT hash FROM api_keys WHERE id = ?`, k.ID).Scan(&stored)
	sum := sha256.Sum256([]byte(plain))
	if stored != hex.EncodeToString(sum[:]) {
		t.Fatal("key must be stored as sha256 hex")
	}

	var canView, canDelete, canUsers bool
	var actor audit.Actor
	h := APIKeyMiddleware(testDeps(d))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		canView = Can(r.Context(), "content.view")
		canDelete = Can(r.Context(), "content.delete") // role allows, scope does not
		canUsers = Can(r.Context(), "users.manage")    // scope allows, role does not
		actor = ActorFromContext(r.Context())
		w.Write([]byte("ok"))
	}))
	call := func(token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/v1/content", nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	if rec := call(plain); rec.Code != 200 || !canView || canDelete || canUsers {
		t.Fatalf("%d view=%v delete=%v users=%v", rec.Code, canView, canDelete, canUsers)
	}
	if actor.Kind != "api_key" || actor.ID != k.ID || actor.Name != "ci" {
		t.Fatalf("actor %+v", actor)
	}
	call(plain)
	rec := call(plain)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("want 429 after rate limit, got %d", rec.Code)
	}
	var env struct {
		OK    bool                  `json:"ok"`
		Error struct{ Code string } `json:"error"`
	}
	json.Unmarshal(rec.Body.Bytes(), &env)
	if env.OK || env.Error.Code != "rate_limited" {
		t.Fatalf("%s", rec.Body)
	}
	if rec := call(tamper(plain)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("tampered key: %d", rec.Code)
	}
	if rec := call(""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", rec.Code)
	}

	plain2, k2, _ := ks.Create(ctx, "old", u.ID, []string{"*"}, 100, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339))
	if rec := call(plain2); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired key: %d", rec.Code)
	}
	plain3, k3, _ := ks.Create(ctx, "revoked", u.ID, []string{"*"}, 100, "")
	if err := ks.Revoke(ctx, k3.ID); err != nil {
		t.Fatal(err)
	}
	if rec := call(plain3); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked key: %d", rec.Code)
	}
	keys, err := ks.List(ctx)
	if err != nil || len(keys) != 3 || keys[0].ID == "" {
		t.Fatalf("%v %v", keys, err)
	}
	_ = k2
	if _, _, err := ks.Create(ctx, "", u.ID, nil, 0, ""); err == nil {
		t.Fatal("want validation error for empty name/scopes")
	}
}

// tamper changes the key's last character, deterministically: replacing it
// with a fixed byte is a no-op when the key already ends in that byte.
func tamper(key string) string {
	last := byte('x')
	if key[len(key)-1] == last {
		last = 'y'
	}
	return key[:len(key)-1] + string(last)
}

func TestTamperAlwaysChangesKey(t *testing.T) {
	for _, k := range []string{"gp_abcx", "gp_abcy", "gp_abc1"} {
		if got := tamper(k); got == k || len(got) != len(k) {
			t.Errorf("tamper(%q) = %q", k, got)
		}
	}
}

func TestTokenBucket(t *testing.T) {
	c := newClock()
	b := newBucketLimiter()
	b.now = c.now
	for i := 0; i < 3; i++ {
		if ok, _ := b.allow("k", 3); !ok {
			t.Fatalf("request %d should pass", i)
		}
	}
	if ok, retry := b.allow("k", 3); ok || retry <= 0 {
		t.Fatal("4th request should be limited")
	}
	c.add(20 * time.Second) // 3/min → one token per 20s
	if ok, _ := b.allow("k", 3); !ok {
		t.Fatal("token should refill")
	}
}

func TestAPIKeyCreateValidatesAndAudits(t *testing.T) {
	d := newDB(t)
	u := mustUser(t, testService(d), "k@example.com", "admin")
	ks, ctx := testKeys(d), context.Background()
	var ve *ValidationError
	if _, _, err := ks.Create(ctx, "x", u.ID, []string{"content.view", "not.a.perm"}, 0, ""); !errors.As(err, &ve) || ve.Fields["scopes"] == "" {
		t.Fatalf("unknown scope: %v", err)
	}
	if _, _, err := ks.Create(ctx, "x", "01NOSUCHUSER", []string{"*"}, 0, ""); !errors.As(err, &ve) || ve.Fields["user_id"] == "" {
		t.Fatalf("unknown user: %v", err)
	}
	if _, _, err := ks.Create(ctx, "x", u.ID, []string{"*"}, 0, "tomorrow"); !errors.As(err, &ve) || ve.Fields["expires_at"] == "" {
		t.Fatalf("bad expiry: %v", err)
	}
	_, k, err := ks.Create(ctx, "ci", u.ID, []string{"*"}, 0, "2030-01-02T03:04:05+02:00")
	if err != nil || k.ExpiresAt != "2030-01-02T01:04:05Z" {
		t.Fatalf("expiry not normalised: %+v %v", k, err)
	}
	if err := ks.Revoke(ctx, k.ID); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(auditActions(t, d), ","); got != "user.create,api_key.create,api_key.revoke" {
		t.Fatalf("audit trail: %s", got)
	}
}

func TestBearerSchemeCaseInsensitive(t *testing.T) {
	d := newDB(t)
	u := mustUser(t, testService(d), "b@example.com", "editor")
	plain, _, err := testKeys(d).Create(context.Background(), "ci", u.ID, []string{"content.view"}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	h := APIKeyMiddleware(testDeps(d))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) }))
	r := httptest.NewRequest("GET", "/api/v1/x", nil)
	r.Header.Set("Authorization", "bearer "+plain)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 200 {
		t.Fatalf("lower-case bearer: %d", rec.Code)
	}
}

func TestAgentKeysOnlyOnMCPMiddleware(t *testing.T) {
	d := newDB(t)
	s := testService(d)
	u := mustUser(t, s, "agent-owner@example.com", "admin")
	ks := testKeys(d)
	ctx := context.Background()
	apiPlain, apiKey, err := ks.Create(ctx, "ci", u.ID, []string{"*"}, 100, "")
	if err != nil || apiKey.Kind != KeyKindAPI {
		t.Fatalf("Create must default to the api kind: %+v %v", apiKey, err)
	}
	agentPlain, agentKey, err := ks.CreateKind(ctx, KeyKindAgent, "claude", u.ID, []string{"*"}, 100, "")
	if err != nil || agentKey.Kind != KeyKindAgent {
		t.Fatalf("CreateKind agent: %+v %v", agentKey, err)
	}
	if _, _, err := ks.CreateKind(ctx, "robot", "x", u.ID, []string{"*"}, 0, ""); err == nil {
		t.Fatal("unknown kind must be a validation error")
	} else if ve, ok := err.(*ValidationError); !ok || ve.Fields["kind"] == "" {
		t.Fatalf("kind error: %v", err)
	}
	list, _ := ks.List(ctx)
	kinds := map[string]string{}
	for _, k := range list {
		kinds[k.Name] = k.Kind
	}
	if kinds["ci"] != KeyKindAPI || kinds["claude"] != KeyKindAgent {
		t.Fatalf("List kinds: %v", kinds)
	}

	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	call := func(h http.Handler, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/v1/me", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	api, mcp := APIKeyMiddleware(testDeps(d))(ok), MCPKeyMiddleware(testDeps(d))(ok)
	rec := call(api, agentPlain)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "agent_key_not_allowed") {
		t.Fatalf("agent key on /api: %d %s", rec.Code, rec.Body)
	}
	if rec := call(api, apiPlain); rec.Code != 200 {
		t.Fatalf("api key on /api: %d", rec.Code)
	}
	if rec := call(mcp, agentPlain); rec.Code != 200 {
		t.Fatalf("agent key on the MCP middleware: %d", rec.Code)
	}
	if rec := call(mcp, apiPlain); rec.Code != 200 {
		t.Fatalf("api key on the MCP middleware: %d", rec.Code)
	}
}

func TestWithAPIKeyActor(t *testing.T) {
	d := newDB(t)
	s := testService(d)
	deps := testDeps(d)
	ctx := context.Background()
	u := mustUser(t, s, "keyowner@example.com", "editor")
	ks := testKeys(d)
	_, k, err := ks.Create(ctx, "bot", u.ID, []string{"content.view", "users.manage"}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	actx, err := WithAPIKeyActor(ctx, deps, k.ID)
	if err != nil {
		t.Fatal(err)
	}
	if a := ActorFromContext(actx); a.Kind != "api_key" || a.ID != k.ID || a.Name != "bot" {
		t.Fatalf("actor %+v", a)
	}
	if UserFromContext(actx).ID != u.ID {
		t.Fatal("the key's owner must be the acting user")
	}
	if !Can(actx, "content.view") || Can(actx, "content.delete") || Can(actx, "users.manage") {
		t.Fatal("Can must apply the key scopes AND the owner's role")
	}

	if _, err := WithAPIKeyActor(ctx, deps, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing key: %v", err)
	}
	_, exp, _ := ks.Create(ctx, "old", u.ID, []string{"*"}, 0, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	d.Exec(`UPDATE api_keys SET expires_at = ? WHERE id = ?`, db.FormatTime(time.Now().Add(-time.Minute)), exp.ID)
	if _, err := WithAPIKeyActor(ctx, deps, exp.ID); !errors.Is(err, ErrKeyExpired) {
		t.Fatalf("expired key: %v", err)
	}
	if err := ks.Revoke(ctx, k.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := WithAPIKeyActor(ctx, deps, k.ID); !errors.Is(err, ErrKeyRevoked) {
		t.Fatalf("revoked key: %v", err)
	}
	_, k2, _ := ks.Create(ctx, "bot2", u.ID, []string{"*"}, 0, "")
	d.Exec(`UPDATE users SET status = 'disabled' WHERE id = ?`, u.ID)
	if _, err := WithAPIKeyActor(ctx, deps, k2.ID); !errors.Is(err, ErrKeyOwnerInactive) {
		t.Fatalf("inactive owner: %v", err)
	}
}
