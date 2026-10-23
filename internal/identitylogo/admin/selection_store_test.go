package identitylogoadmin

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/identitylogo"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"github.com/zadstack/gorun/internal/theme"
)

type interleavedLogoMedia struct {
	media.Service
	after    func() error
	imported *media.Media
}

func (m *interleavedLogoMedia) Upload(ctx context.Context, name string, reader io.Reader, meta media.UploadMeta) (*media.Media, error) {
	asset, err := m.Service.Upload(ctx, name, reader, meta)
	if err != nil {
		return nil, err
	}
	m.imported = asset
	if err := m.after(); err != nil {
		return nil, err
	}
	return asset, nil
}

func TestSelectionImportInterleavePreservesLatestStateOrReportsRetainedMedia(t *testing.T) {
	for _, change := range []string{"unrelated-live-title", "selected-slot", "active-theme", "module-setting", "module-config"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			root, err := filepath.Abs("../../../themes")
			if err != nil {
				t.Fatal(err)
			}
			cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://example.test", Theme: "default"}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			app, err := kernel.BootWithOptions(ctx, cfg, []kernel.Module{settingsmod.New(), auth.New(), content.New(), media.New(), theme.New(theme.WithThemesDir(root), theme.WithLogger(log))}, kernel.BootOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer app.Close()
			users, ok := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
			if !ok {
				t.Fatal("auth missing")
			}
			user, err := users.Create(ctx, "selection@example.test", "Owner", "selection-password-0001", auth.RoleAdmin)
			if err != nil {
				t.Fatal(err)
			}
			ctx = auth.WithUser(ctx, user)
			if err := app.Deps.Settings.Set(ctx, "site.title", "Live before"); err != nil {
				t.Fatal(err)
			}
			draft := theme.IdentityDraft{Snapshot: theme.IdentitySnapshot{Theme: "default", Values: map[string]string{"identity_accent": "#224466"}, Site: map[string]string{"site.title": "Pending title", "site.tagline": "Pending tagline"}, Links: map[string]string{"cta_label": "Keep CTA", "cta_url": "/keep"}}, Base: map[string]string{"site.title": "Live before"}}
			if err := app.Deps.Settings.Set(ctx, "theme.default.identity_draft", draft); err != nil {
				t.Fatal(err)
			}
			library, ok := kernel.Get[media.Service](app.Deps, kernel.ServiceMedia)
			if !ok {
				t.Fatal("media missing")
			}
			var source bytes.Buffer
			if err := png.Encode(&source, image.NewNRGBA(image.Rect(0, 0, 1024, 1024))); err != nil {
				t.Fatal(err)
			}
			shared, err := library.Upload(ctx, "shared-existing.png", bytes.NewReader(source.Bytes()), media.UploadMeta{AuthorID: user.ID})
			if err != nil {
				t.Fatal(err)
			}
			store := &draftStore{d: app.Deps}
			_, baselines, err := store.Review(ctx, "default")
			if err != nil {
				t.Fatal(err)
			}
			wrapper := &interleavedLogoMedia{Service: library}
			wrapper.after = func() error {
				switch change {
				case "unrelated-live-title":
					return app.Deps.Settings.Set(ctx, "site.title", "Latest title during import")
				case "selected-slot":
					draft.Snapshot.Site["site.logo"] = shared.ID
					return app.Deps.Settings.Set(ctx, "theme.default.identity_draft", draft)
				case "active-theme":
					return app.Deps.Settings.Set(ctx, theme.SettingTheme, "business")
				case "module-setting":
					return app.Deps.Settings.Set(ctx, kernel.AddonSettingKey("identitylogo"), false)
				case "module-config":
					app.Deps.Config.Addons = map[string]string{"identitylogo": kernel.AddonModeDisabled}
				}
				return nil
			}
			app.Deps.Services.Set(kernel.ServiceMedia, media.Service(wrapper))
			err = store.SelectPNG(ctx, "default", baselines["site.logo"], "site.logo", source.Bytes())
			if wrapper.imported == nil {
				t.Fatalf("did not reach controlled import boundary: %v", err)
			}
			var after theme.IdentityDraft
			if e := app.Deps.Settings.Get(ctx, "theme.default.identity_draft", &after); e != nil {
				t.Fatal(e)
			}
			if change == "unrelated-live-title" {
				if err != nil {
					t.Fatal(err)
				}
				draft.Snapshot.Site["site.logo"] = wrapper.imported.ID
				draft.Base["site.logo"] = ""
				if !reflect.DeepEqual(after.Snapshot, draft.Snapshot) || !reflect.DeepEqual(after.Base, draft.Base) || app.Deps.Settings.GetString(ctx, "site.title", "") != "Latest title during import" {
					t.Fatal("unrelated import edit lost")
				}
			} else {
				var conflict *importedConflict
				want := identitylogo.ErrChanged
				if change == "module-setting" || change == "module-config" {
					want = identitylogo.ErrDisabled
				}
				if !errors.Is(err, want) {
					t.Fatalf("selection lost typed failure: got=%v want=%v", err, want)
				}
				if !errors.As(err, &conflict) || conflict.MediaID != wrapper.imported.ID {
					t.Fatalf("import conflict did not report its media ID: %v", err)
				}
				if !reflect.DeepEqual(after.Snapshot, draft.Snapshot) || !reflect.DeepEqual(after.Base, draft.Base) {
					t.Fatal("failed import overwrote latest pending fields")
				}
			}
			for _, id := range []string{shared.ID, wrapper.imported.ID} {
				if _, err := library.Get(ctx, id); err != nil {
					t.Fatalf("media was deleted after import: %s %v", id, err)
				}
			}
		})
	}
}
