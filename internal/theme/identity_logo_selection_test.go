package theme

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/zadstack/gorun/internal/media"
	"image"
	"image/png"
	"maps"
	"reflect"
	"sync"
	"testing"

	"github.com/zadstack/gorun/internal/kernel"
)

func storeLogoDraft(t *testing.T, e *env, ctx context.Context, draft IdentityDraft) {
	t.Helper()
	if err := e.app.Deps.Settings.Set(ctx, identityDraftKey("default"), draft); err != nil {
		t.Fatal(err)
	}
}
func rawLogoDraft(t *testing.T, e *env, ctx context.Context) IdentityDraft {
	t.Helper()
	var raw string
	if err := e.app.Deps.DB.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key=?`, identityDraftKey("default")).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var draft IdentityDraft
	if err := json.Unmarshal([]byte(raw), &draft); err != nil {
		t.Fatal(err)
	}
	return draft
}
func selectionDraft(e *env, ctx context.Context) IdentityDraft {
	live := e.svc.LiveIdentity(ctx, "default")
	draft := IdentityDraft{Snapshot: live, Base: maps.Clone(live.Site), SavedAt: "before", Author: "original-author"}
	draft.Snapshot.Site["site.title"] = "Pending title"
	draft.Snapshot.Site["site.tagline"] = "Pending tagline"
	draft.Snapshot.Values[idAccent] = "#224466"
	draft.Snapshot.Links["cta_label"] = "Pending CTA"
	draft.Snapshot.Links["cta_url"] = "/pending"
	return draft
}

func TestSlotSelectionPreservesLatestUnrelatedRawDraftAndLive(t *testing.T) {
	for _, change := range []string{"draft-tagline", "live-title", "identity-ai-fields", "other-logo-slot", "metadata-only", "no-draft-to-identical-draft"} {
		t.Run(change, func(t *testing.T) {
			e, ctx, id := logoEnv(t)
			e.setting("site.title", "Original live title")
			draft := selectionDraft(e, ctx)
			if change != "no-draft-to-identical-draft" {
				storeLogoDraft(t, e, ctx, draft)
			}
			revision, baselines, err := e.svc.IdentityLogoReview(ctx, "default")
			if err != nil || len(baselines) != 4 {
				t.Fatalf("review %v %v", baselines, err)
			}
			switch change {
			case "draft-tagline":
				draft.Snapshot.Site["site.tagline"] = "Latest human tagline"
			case "live-title":
				e.setting("site.title", "Latest live title")
			case "identity-ai-fields":
				draft.Snapshot.Values[idAccent] = "#c2410c"
				draft.Snapshot.Site["site.tagline"] = "Latest suggestion"
				draft.Snapshot.Links["cta_label"] = "Latest AI CTA"
			case "other-logo-slot":
				draft.Snapshot.Site["site.logo_dark"] = id
			case "metadata-only":
				draft.Author = "another-editor"
				draft.SavedAt = "later"
			case "no-draft-to-identical-draft":
				live := e.svc.LiveIdentity(ctx, "default")
				draft = IdentityDraft{Snapshot: live, Base: maps.Clone(live.Site), Author: "new-editor", SavedAt: "later"}
			}
			storeLogoDraft(t, e, ctx, draft)
			latest := rawLogoDraft(t, e, ctx)
			current, currentSlots, err := e.svc.IdentityLogoReview(ctx, "default")
			if err != nil || current == revision || currentSlots["site.logo"] != baselines["site.logo"] {
				t.Fatalf("unrelated edit changed target: full=%v slots=%v err=%v", current == revision, currentSlots, err)
			}
			if err := e.svc.SaveIdentityLogoSlot(ctx, "default", baselines["site.logo"], "site.logo", id); err != nil {
				t.Fatal(err)
			}
			after := rawLogoDraft(t, e, ctx)
			latest.Snapshot.Site["site.logo"] = id
			if latest.Base != nil {
				latest.Base["site.logo"] = e.app.Deps.Settings.GetString(ctx, "site.logo", "")
			}
			if !reflect.DeepEqual(after.Snapshot, latest.Snapshot) || !reflect.DeepEqual(after.Base, latest.Base) {
				t.Fatalf("selection rewrote unrelated raw draft\ngot=%+v\nwant=%+v", after, latest)
			}
			wantTitle := "Original live title"
			if change == "live-title" {
				wantTitle = "Latest live title"
			}
			if got := e.app.Deps.Settings.GetString(ctx, "site.title", ""); got != wantTitle {
				t.Fatalf("live title overwritten: %q", got)
			}
			if got := e.app.Deps.Settings.GetString(ctx, "site.logo", ""); got != "" {
				t.Fatalf("selected logo applied live: %q", got)
			}
		})
	}
}

func TestSlotSelectionRejectsTargetThemeAndDisabledModuleChanges(t *testing.T) {
	for _, change := range []string{"pending-target", "live-target", "masked-live-target", "theme", "module-setting", "module-config"} {
		t.Run(change, func(t *testing.T) {
			e, ctx, id := logoEnv(t)
			draft := selectionDraft(e, ctx)
			if change == "masked-live-target" {
				e.setting("site.logo", "live-before")
				draft.Base["site.logo"] = "live-before"
				draft.Snapshot.Site["site.logo"] = "pending-kept"
			}
			storeLogoDraft(t, e, ctx, draft)
			_, baselines, err := e.svc.IdentityLogoReview(ctx, "default")
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "pending-target":
				draft.Snapshot.Site["site.logo"] = "human-logo"
				storeLogoDraft(t, e, ctx, draft)
			case "live-target":
				e.setting("site.logo", "human-live-logo")
			case "masked-live-target":
				e.setting("site.logo", "live-after")
				draft.Base["site.logo"] = "live-after"
				storeLogoDraft(t, e, ctx, draft)
			case "theme":
				e.setting(SettingTheme, "business")
			case "module-setting":
				e.setting(kernel.AddonSettingKey("identitylogo"), false)
			case "module-config":
				e.app.Deps.Config.Addons = map[string]string{"identitylogo": kernel.AddonModeDisabled}
			}
			before := rawLogoDraft(t, e, ctx)
			err = e.svc.SaveIdentityLogoSlot(ctx, "default", baselines["site.logo"], "site.logo", id)
			want := ErrIdentityLogoConflict
			if change == "module-setting" || change == "module-config" {
				want = ErrIdentityLogoDisabled
			}
			if !errors.Is(err, want) {
				t.Fatalf("%s accepted: %v", change, err)
			}
			if after := rawLogoDraft(t, e, ctx); !reflect.DeepEqual(before, after) {
				t.Fatal("rejected selection changed latest draft")
			}
		})
	}
}

func TestConcurrentSlotSelectionsHaveOneCASWinner(t *testing.T) {
	e, ctx, id := logoEnv(t)
	storeLogoDraft(t, e, ctx, selectionDraft(e, ctx))
	_, baselines, err := e.svc.IdentityLogoReview(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	// Import a second candidate before the competing transactions begin.
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewNRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	library := media.NewService(e.app.Deps, media.NewLocalStorage(t.TempDir()))
	second, err := library.Upload(ctx, "second-logo.png", &imageBytes, media.UploadMeta{})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, mediaID := range []string{id, second.ID} {
		wg.Add(1)
		go func(mediaID string) {
			defer wg.Done()
			results <- e.svc.SaveIdentityLogoSlot(ctx, "default", baselines["site.logo"], "site.logo", mediaID)
		}(mediaID)
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrIdentityLogoConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("winners=%d conflicts=%d", successes, conflicts)
	}
}
