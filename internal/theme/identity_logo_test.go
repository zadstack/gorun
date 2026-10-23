package theme

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"reflect"
	"sync"
	"testing"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/media"
)

func logoEnv(t *testing.T) (*env, context.Context, string) {
	t.Helper()
	e, ctx := identityEnv(t)
	if err := db.Migrate(ctx, e.app.Deps.DB, "media", media.New().Migrations()); err != nil {
		t.Fatal(err)
	}
	ms := media.NewService(e.app.Deps, media.NewLocalStorage(t.TempDir()))
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	m, err := ms.Upload(ctx, "synthetic-logo.png", &b, media.UploadMeta{})
	if err != nil {
		t.Fatal(err)
	}
	return e, ctx, m.ID
}
func logoRevision(t *testing.T, e *env, ctx context.Context) string {
	t.Helper()
	r, err := e.svc.IdentityLogoRevision(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestIdentityLogoCASPreservesDraftAndLive(t *testing.T) {
	e, ctx, id := logoEnv(t)
	e.setting("site.title", "Live title")
	e.setting("site.tagline", "Live tagline")
	e.setting("theme.default.cta_url", "/live")
	e.setting("theme.default.cta_label", "Live CTA")
	// Store an actual draft row, including header links. Warm the read cache.
	draft := IdentityDraft{Snapshot: IdentitySnapshot{Theme: "default", Values: map[string]string{idAccent: "#c2410c", idCustomCSS: ".keep{}"}, Site: map[string]string{"site.title": "Draft title", "site.tagline": "Draft tagline", "site.logo_dark": "existing-dark"}, Links: map[string]string{"cta_label": "Draft CTA", "cta_url": "/draft"}}, Base: map[string]string{"site.title": "Live title", "site.tagline": "Live tagline"}}
	if err := e.app.Deps.Settings.Set(ctx, identityDraftKey("default"), draft); err != nil {
		t.Fatal(err)
	}
	before, _ := e.svc.IdentityDraft(ctx, "default")
	rev := logoRevision(t, e, ctx)
	if err := e.svc.SaveIdentityLogo(ctx, "default", rev, "site.logo_light", id); err != nil {
		t.Fatal(err)
	}
	after, ok := e.svc.IdentityDraft(ctx, "default")
	if !ok {
		t.Fatal("draft missing")
	}
	want := before.Snapshot
	want.Site["site.logo_light"] = id
	if !reflect.DeepEqual(after.Snapshot, want) {
		t.Fatalf("draft lost fields: got=%+v want=%+v", after.Snapshot, want)
	}
	if e.app.Deps.Settings.GetString(ctx, "site.title", "") != "Live title" || e.app.Deps.Settings.GetString(ctx, "site.logo_light", "") != "" || e.app.Deps.Settings.GetString(ctx, "theme.default.cta_url", "") != "/live" {
		t.Fatal("logo selection changed live identity")
	}
	if got := logoRevision(t, e, ctx); got == rev {
		t.Fatal("revision did not advance")
	}
	if err := e.svc.SaveIdentityLogo(ctx, "default", rev, "site.icon", id); !errors.Is(err, ErrIdentityLogoConflict) {
		t.Fatal("stale review accepted", err)
	}
}

func TestIdentityLogoRevisionIncludesAbsentDraftAndLiveChanges(t *testing.T) {
	for _, key := range []string{"site.title", "site.logo", "theme.default.identity_accent", "theme.default.cta_url", SettingTheme} {
		t.Run(key, func(t *testing.T) {
			e, ctx, id := logoEnv(t)
			if _, ok := e.svc.IdentityDraft(ctx, "default"); ok {
				t.Fatal("unexpected draft")
			}
			rev := logoRevision(t, e, ctx)
			value := "changed"
			if key == SettingTheme {
				value = "business"
			}
			e.setting(key, value)
			if err := e.svc.SaveIdentityLogo(ctx, "default", rev, "site.logo", id); !errors.Is(err, ErrIdentityLogoConflict) {
				t.Fatalf("%s change ignored: %v", key, err)
			}
			if _, ok := e.svc.IdentityDraft(ctx, "default"); ok {
				t.Fatal("conflict wrote draft")
			}
		})
	}
}

func TestIdentityLogoMissingDraftCacheAndLiveRebase(t *testing.T) {
	e, ctx, id := logoEnv(t)
	e.setting("site.title", "Old live")
	if _, ok := e.svc.IdentityDraft(ctx, "default"); ok {
		t.Fatal("draft exists")
	}
	rev := logoRevision(t, e, ctx)
	if err := e.svc.SaveIdentityLogo(ctx, "default", rev, "site.icon", id); err != nil {
		t.Fatal(err)
	}
	draft, ok := e.svc.IdentityDraft(ctx, "default")
	if !ok || draft.Snapshot.Site["site.icon"] != id {
		t.Fatal("negative cache hid committed draft")
	}
	e.setting("site.title", "New live")
	rev = logoRevision(t, e, ctx)
	if err := e.svc.SaveIdentityLogo(ctx, "default", rev, "site.logo", id); err != nil {
		t.Fatal(err)
	}
	draft, ok = e.svc.IdentityDraft(ctx, "default")
	if !ok || draft.Snapshot.Site["site.title"] != "New live" || draft.Base["site.title"] != "New live" || draft.Snapshot.Site["site.icon"] != id {
		t.Fatalf("live rebase lost: %+v", draft)
	}
}

func TestIdentityLogoConcurrentCASOneWinner(t *testing.T) {
	e, ctx, id := logoEnv(t)
	rev := logoRevision(t, e, ctx)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, slot := range []string{"site.logo", "site.icon"} {
		wg.Add(1)
		go func(slot string) {
			defer wg.Done()
			<-start
			results <- e.svc.SaveIdentityLogo(ctx, "default", rev, slot, id)
		}(slot)
	}
	close(start)
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrIdentityLogoConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflicts=%d", success, conflict)
	}
}

func TestIdentityLogoRejectsConcurrentHumanDraft(t *testing.T) {
	e, ctx, id := logoEnv(t)
	rev := logoRevision(t, e, ctx)
	human := snap("default", map[string]string{idAccent: "#c2410c"}, map[string]string{"site.title": "Human edit"})
	if err := e.svc.SaveIdentityDraft(ctx, human); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SaveIdentityLogo(ctx, "default", rev, "site.logo", id); !errors.Is(err, ErrIdentityLogoConflict) {
		t.Fatal(err)
	}
	got, _ := e.svc.IdentityDraft(ctx, "default")
	if got.Snapshot.Site["site.title"] != "Human edit" || got.Snapshot.Values[idAccent] != "#c2410c" || got.Snapshot.Site["site.logo"] != "" {
		t.Fatal("concurrent edit overwritten")
	}
}

func TestIdentityLogoCASWaitsForTransactionThenReadsCurrentState(t *testing.T) {
	e, ctx, id := logoEnv(t)
	rev := logoRevision(t, e, ctx)
	tx, err := e.app.Deps.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := e.app.Deps.Settings.SetTx(ctx, tx, "site.title", "Concurrent transaction"); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- e.svc.SaveIdentityLogo(ctx, "default", rev, "site.logo", id) }()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrIdentityLogoConflict) {
		t.Fatalf("CAS did not observe committed writer: %v", err)
	}
	var count int
	if err := e.app.Deps.DB.QueryRowContext(ctx, `SELECT count(*) FROM settings WHERE key=?`, identityDraftKey("default")).Scan(&count); err != nil || count != 0 {
		t.Fatalf("conflict wrote draft: count=%d err=%v", count, err)
	}
}

func TestIdentityLogoRequiresExistingPNG(t *testing.T) {
	e, ctx, id := logoEnv(t)
	rev := logoRevision(t, e, ctx)
	for _, tc := range []struct{ slot, id string }{{"site.title", id}, {"site.logo", "missing"}} {
		if err := e.svc.SaveIdentityLogo(ctx, "default", rev, tc.slot, tc.id); !errors.Is(err, ErrIdentityLogoMedia) {
			t.Fatal(err)
		}
	}
	if _, err := e.app.Deps.DB.ExecContext(ctx, `UPDATE media SET mime='image/gif' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SaveIdentityLogo(ctx, "default", rev, "site.logo", id); !errors.Is(err, ErrIdentityLogoMedia) {
		t.Fatal(err)
	}
	if _, ok := e.svc.IdentityDraft(ctx, "default"); ok {
		t.Fatal("invalid media wrote draft")
	}
}
