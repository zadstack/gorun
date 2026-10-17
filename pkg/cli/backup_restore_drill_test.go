package cli

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	gpbackup "github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/theme"
)

func TestBackupIsolatedFullAppRestoreDrill(t *testing.T) {
	for _, env := range os.Environ() {
		name, _, _ := strings.Cut(env, "=")
		if strings.HasPrefix(name, "GORUN_") || strings.HasPrefix(name, "STRIPE_") {
			t.Setenv(name, "")
		}
	}
	t.Setenv(theme.EnvThemesDir, "../../themes")
	kernel.LogOutput = io.Discard
	ctx := context.Background()
	source := t.TempDir()
	cfg, err := config.Load([]string{"--data", source})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "gorun.toml"), []byte("env = \"dev\"\ntheme = \"default\"\nbase_url = \"http://restore-fixture.test\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	app, err := kernel.BootWithOptions(ctx, cfg, Modules(), kernel.BootOptions{StartJobs: false})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	u, err := users.Create(ctx, "restore-fixture@example.test", "كاتب التجربة", "fixture-only password", auth.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := kernel.Get[media.Service](app.Deps, kernel.ServiceMedia)
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 3, 3))); err != nil {
		t.Fatal(err)
	}
	m, err := ms.Upload(ctx, "fixture.png", bytes.NewReader(imageBytes.Bytes()), media.UploadMeta{Alt: "صورة الاستعادة"})
	if err != nil {
		t.Fatal(err)
	}
	cs, _ := kernel.Get[content.Service](app.Deps, kernel.ServiceContent)
	item, err := cs.Create(ctx, content.Input{Type: "post", Slug: "restore-fixture", Title: "محتوى الاستعادة", Status: "published", AuthorID: u.ID, FeaturedMediaID: m.ID, Body: `<p>drill-body</p>`, SEO: map[string]any{"title": "fixture SEO"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Deps.Settings.Set(ctx, "site.title", "هوية الاستعادة"); err != nil {
		t.Fatal(err)
	}
	encrypted, err := db.Encrypt(cfg.SecretKey, []byte("fixture-credential"), "fixture.credential")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Deps.Settings.Set(ctx, "fixture.credential", encrypted); err != nil {
		t.Fatal(err)
	}
	th, _ := kernel.Get[theme.Service](app.Deps, kernel.ServiceTheme)
	template := `{{part "header" .}}<p>restored-template-marker</p><p>{{.Site.Title}}</p>{{with .Content}}<h1>{{.Title}}</h1>{{safe .Body}}{{with media .FeaturedMediaID}}{{img . "" "fixture"}}{{end}}{{end}}{{part "footer" .}}`
	if err := th.SaveOverride(ctx, "single.html", template, false); err != nil {
		t.Fatal(err)
	}
	app.Close() // Writers and jobs are stopped only in this synthetic fixture.
	res, err := gpbackup.Create(source, cfg.DBPath(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "isolated-restore")
	rep, err := gpbackup.Restore(res.Manifest, destination)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.IntegrityOK || rep.ForeignKeyViolations != 0 || !rep.RecoveryKeyArchived || rep.Remote.ReadBackVerified {
		t.Fatalf("incorrect preflight: %+v", rep)
	}
	restoredConfig, err := config.LoadReadOnly([]string{"--data", destination})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restoredConfig.SecretKey, cfg.SecretKey) {
		t.Fatal("original recovery key not restored")
	}
	restored, err := kernel.BootWithOptions(ctx, restoredConfig, Modules(), kernel.BootOptions{StartJobs: false})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	rcs, _ := kernel.Get[content.Service](restored.Deps, kernel.ServiceContent)
	got, err := rcs.Get(ctx, item.ID)
	if err != nil || got.Title != item.Title || got.AuthorID != u.ID || got.FeaturedMediaID != m.ID || got.SEO["title"] != "fixture SEO" {
		t.Fatal("content relationships or SEO not restored", err)
	}
	var saved string
	if err := restored.Deps.Settings.Get(ctx, "fixture.credential", &saved); err != nil {
		t.Fatal(err)
	}
	secret, err := db.Decrypt(restoredConfig.SecretKey, saved, "fixture.credential")
	if err != nil || string(secret) != "fixture-credential" {
		t.Fatal("restored credential is not decryptable", err)
	}
	srv := httptest.NewServer(restored.Handler)
	defer srv.Close()
	for _, p := range []string{"/post/restore-fixture", m.URL} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || readErr != nil {
			t.Fatalf("isolated HTTP %s: %d %v", p, resp.StatusCode, readErr)
		}
		if p == m.URL {
			if !bytes.Equal(body, imageBytes.Bytes()) {
				t.Fatal("media bytes changed")
			}
		} else {
			for _, want := range []string{item.Title, "drill-body", "هوية الاستعادة", "restored-template-marker", m.URL} {
				if !strings.Contains(string(body), want) {
					t.Fatalf("rendered restore omits %q", want)
				}
			}
		}
	}
	t.Log("isolated fixture restore: checksum-bound local pair; SQLite integrity and FK checks; original key decrypts stored credential; content/author/SEO/featured image/identity/template restored; GET page and PNG both 200; jobs disabled; no production or remote provider")
}
