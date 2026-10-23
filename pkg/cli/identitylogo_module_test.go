package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/theme"
	"image"
	"image/png"
	"testing"

	"github.com/zadstack/gorun/internal/identitylogo"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestIdentityLogoOptionalRegistration(t *testing.T) {
	data, ctx := t.TempDir(), context.Background()
	app := bootData(t, data, "{}")
	library, ok := kernel.Get[media.Service](app.Deps, kernel.ServiceMedia)
	if !ok {
		t.Fatal("media unavailable")
	}
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewNRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	asset, err := library.Upload(ctx, "synthetic-retained-logo.png", &pngBytes, media.UploadMeta{Title: "Retained synthetic logo"})
	if err != nil {
		t.Fatal(err)
	}
	bindingKey := "identitylogo.binding.synthetic-retention"
	if err := app.Deps.Settings.Set(ctx, bindingKey, map[string]any{"protocol": "images-png-v1", "model": "synthetic-image-model", "source": "https://estimate.example.test", "estimate_micros": 50000}); err != nil {
		t.Fatal(err)
	}
	if err := app.Deps.Settings.Set(ctx, "site.logo", asset.ID); err != nil {
		t.Fatal(err)
	}
	store, ok := kernel.Get[theme.IdentityLogoStore](app.Deps, kernel.ServiceTheme)
	if !ok {
		t.Fatal("theme logo store unavailable")
	}
	revision, err := store.IdentityLogoRevision(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveIdentityLogo(ctx, "default", revision, "site.logo_dark", asset.ID); err != nil {
		t.Fatal(err)
	}
	keys := []string{bindingKey, "site.logo", "theme.default.identity_draft"}
	before := map[string]string{}
	for _, key := range keys {
		var raw string
		if err := app.Deps.DB.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key=?`, key).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		before[key] = raw
	}
	app.Close()
	for index, disabled := range []bool{true, false} {
		mode := "{}"
		if disabled {
			mode = `{"identitylogo":"disabled"}`
		}
		app := bootData(t, data, mode)
		for key, want := range before {
			var raw string
			if err := app.Deps.DB.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key=?`, key).Scan(&raw); err != nil || raw != want {
				t.Fatalf("disabled=%v persisted %s changed: %q err=%v", disabled, key, raw, err)
			}
		}
		library, ok := kernel.Get[media.Service](app.Deps, kernel.ServiceMedia)
		if !ok {
			t.Fatal("media unavailable after reboot")
		}
		retained, err := library.Get(ctx, asset.ID)
		if err != nil || retained.Checksum != asset.Checksum || retained.URL != asset.URL || retained.Size != asset.Size {
			t.Fatalf("disabled=%v media changed: %+v err=%v", disabled, retained, err)
		}
		status, body := serveStatus(app.Handler, "GET", asset.URL, nil)
		sum := sha256.Sum256([]byte(body))
		if status != 200 || hex.EncodeToString(sum[:]) != asset.Checksum {
			t.Fatalf("disabled=%v imported media bytes changed, status=%d", disabled, status)
		}
		state := moduleStates(app)["identitylogo"]
		if !state.Optional || !state.Available || state.Enabled == disabled {
			t.Fatalf("disabled=%v state=%+v", disabled, state)
		}
		_, service := kernel.Get[*identitylogo.Service](app.Deps, "identitylogo")
		if service == disabled {
			t.Fatalf("disabled=%v service registered=%v", disabled, service)
		}
		cat := catalogOf(t, app.Handler, freshKey(t, app, index+1))
		logo := entry(t, cat, "identitylogo")
		if (logo.Registered == nil) != disabled {
			t.Fatalf("disabled=%v registrations=%+v", disabled, logo.Registered)
		}
		if disabled && len(logo.Permissions) != 0 {
			t.Fatalf("disabled permissions=%v", logo.Permissions)
		}
		if !disabled {
			found := false
			for _, route := range logo.Registered.Nav {
				if route == "appearance.identity_logo" {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing logo navigation registration: %+v", logo.Registered)
			}
		}
		app.Close()
	}
}
