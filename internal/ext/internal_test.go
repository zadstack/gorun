package ext

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/hooks"
	"github.com/zadstack/gorun/internal/jobs"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

// bootInternal boots the full stack and returns the concrete service and an
// admin context.
func bootInternal(t *testing.T) (*service, context.Context, *kernel.App) {
	t.Helper()
	kernel.LogOutput = io.Discard
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://gorun.test", AllowPrivateNet: true}
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), auth.New(), content.New(), api.New(), admin.New(), New()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	u, err := users.Create(context.Background(), "a@example.com", "A", "correct horse battery staple", "admin")
	if err != nil {
		t.Fatal(err)
	}
	svc, _ := kernel.Get[Service](app.Deps, ServiceName)
	return svc.(*service), auth.WithUser(context.Background(), u), app
}

func writePkg(t *testing.T, raw string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, manifestFile), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

type failingKeys struct{ auth.KeyService }

func (failingKeys) Create(context.Context, string, string, []string, int, string) (string, *auth.APIKey, error) {
	return "", nil, errors.New("boom")
}

func TestFailedInstallRollsBack(t *testing.T) {
	s, ctx, app := bootInternal(t)
	s.keys = failingKeys{s.keys}
	raw := `{"slug":"broken","name":{"en":"Broken"},"version":"1.0.0","permissions":["content.view"],"hooks":[],
		"endpoint":{"url":"","auth":""},
		"content_types":[{"slug":"widget","name":{"en":"Widgets"}}],
		"settings":[{"key":"color","label":{"en":"Color"},"type":"text","default":"red"}]}`
	if _, err := s.InstallDir(ctx, writePkg(t, raw)); err == nil {
		t.Fatal("install must fail")
	}
	if _, err := s.get(ctx, "broken"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("row left behind: %v", err)
	}
	cs := s.content()
	if _, err := cs.GetType(ctx, "widget"); !errors.Is(err, content.ErrNotFound) {
		t.Fatalf("type left behind: %v", err)
	}
	if all, _ := app.Deps.Settings.All(ctx, "ext.broken."); len(all) != 0 {
		t.Fatalf("settings left behind: %v", all)
	}
	if _, err := os.Stat(s.extDir("broken")); !os.IsNotExist(err) {
		t.Fatal("package dir left behind")
	}
}

func TestFailedInstallKeepsPreexistingTypesAndDroppedDir(t *testing.T) {
	s, ctx, _ := bootInternal(t)
	// A package dropped in place is never deleted by a failed install, and
	// types from an earlier install survive.
	dir := s.extDir("broken")
	os.MkdirAll(dir, 0o755)
	raw := `{"slug":"broken","name":{"en":"Broken"},"version":"1.0.0","permissions":["content.view"],"hooks":[],"endpoint":{"url":"","auth":""},
		"content_types":[{"slug":"widget","name":{"en":"Widgets"}}]}`
	os.WriteFile(filepath.Join(dir, manifestFile), []byte(raw), 0o644)
	if _, err := s.InstallDir(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if err := s.Uninstall(ctx, "broken", false); err != nil {
		t.Fatal(err)
	}
	s.keys = failingKeys{s.keys}
	if _, err := s.InstallDir(ctx, dir); err == nil {
		t.Fatal("install must fail")
	}
	if _, err := s.content().GetType(ctx, "widget"); err != nil {
		t.Fatalf("pre-existing type removed by rollback: %v", err)
	}
	var types []string
	if err := s.settings.Get(ctx, "ext.broken.types", &types); err != nil || len(types) != 1 {
		t.Fatalf("types bookkeeping not restored: %v %v", types, err)
	}
	if _, err := os.Stat(filepath.Join(dir, manifestFile)); err != nil {
		t.Fatal("dropped package dir must survive a failed install")
	}
}

func TestAsyncDeliveryCountsOneFailurePerExhaustedDelivery(t *testing.T) {
	s, ctx, _ := bootInternal(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "x", 500) }))
	t.Cleanup(srv.Close)
	raw := `{"slug":"down","name":{"en":"Down"},"version":"1.0.0","permissions":[],"hooks":[{"name":"x.happened","kind":"event"}],
		"endpoint":{"url":"` + srv.URL + `","auth":"hmac"}}`
	if _, err := s.InstallDir(ctx, writePkg(t, raw)); err != nil {
		t.Fatal(err)
	}
	if err := s.Activate(ctx, "down"); err != nil {
		t.Fatal(err)
	}
	p, _ := json.Marshal(deliverPayload{Slug: "down", Hook: "x.happened", Kind: KindEvent, DeliveryID: "d-1", Payload: json.RawMessage(`{}`)})
	for i := 1; i <= deliverMaxAttempts; i++ {
		// the worker passes the attempt number; only the last one counts
		if err := s.handleDeliver(jobs.ContextWithAttempt(ctx, i, deliverMaxAttempts), p); err == nil {
			t.Fatal("failing delivery must return an error for retry")
		}
		x, _ := s.get(ctx, "down")
		want := 0
		if i == deliverMaxAttempts {
			want = 1
		}
		if x.Failures != want {
			t.Fatalf("attempt %d: failures = %d, want %d", i, x.Failures, want)
		}
	}
}

func TestOversizedEnvelopeIsSkippedNotCounted(t *testing.T) {
	s, ctx, _ := bootInternal(t)
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	t.Cleanup(srv.Close)
	raw := `{"slug":"big","name":{"en":"Big"},"version":"1.0.0","permissions":[],"hooks":[{"name":"x.happened","kind":"event"},{"name":"x.filter","kind":"filter"}],
		"endpoint":{"url":"` + srv.URL + `","auth":"hmac"}}`
	if _, err := s.InstallDir(ctx, writePkg(t, raw)); err != nil {
		t.Fatal(err)
	}
	if err := s.Activate(ctx, "big"); err != nil {
		t.Fatal(err)
	}
	huge := `"` + strings.Repeat("a", maxEnvelopeSize) + `"`
	p, _ := json.Marshal(deliverPayload{Slug: "big", Hook: "x.happened", Kind: KindEvent, DeliveryID: "d-big", Payload: json.RawMessage(huge)})
	if err := s.handleDeliver(ctx, p); err != nil {
		t.Fatalf("oversized delivery must be dropped, not retried: %v", err)
	}
	orig := strings.Repeat("b", maxEnvelopeSize)
	if got := s.applyFilter(ctx, "big", "x.filter", orig); got != orig {
		t.Fatal("oversized filter must keep the original value")
	}
	x, _ := s.get(ctx, "big")
	if calls != 0 || x.Failures != 0 {
		t.Fatalf("calls=%d failures=%d", calls, x.Failures)
	}
}

func TestBridgeUnregistersHooksOnDeactivateAndRemove(t *testing.T) {
	s := &service{hooks: hooks.New(), rt: newBridge()}
	x := &Extension{Slug: "demo", Manifest: Manifest{Hooks: []HookSpec{
		{Name: "demo.filter", Kind: KindFilter}, {Name: "demo.action", Kind: "action"}, {Name: "demo.event", Kind: "event"},
		{Name: "demo.action", Kind: "event"}, // same registry as the action: one subscription
	}}}
	s.rt.activate(s, x, nil)
	s.rt.activate(s, x, nil) // re-activation replaces, never duplicates
	if n := len(s.rt.tokens["demo"]); n != 3 {
		t.Fatalf("tokens after double activate = %d, want 3", n)
	}
	if got := strings.Join(s.hooks.Names(), ","); got != "demo.action,demo.event,demo.filter" {
		t.Fatalf("registered hooks: %s", got)
	}
	s.rt.deactivate(s, "demo")
	if len(s.hooks.Names()) != 0 || len(s.rt.tokens["demo"]) != 0 {
		t.Fatalf("deactivate must unregister every hook: %v", s.hooks.Names())
	}
	if _, _, ok := s.rt.lookup("demo", "demo.filter", KindFilter); ok {
		t.Fatal("deactivated state must not deliver")
	}
	s.rt.activate(s, x, nil)
	s.rt.remove(s, "demo")
	if len(s.hooks.Names()) != 0 || s.rt.states["demo"] != nil {
		t.Fatalf("remove must unregister and forget: %v", s.hooks.Names())
	}
}
