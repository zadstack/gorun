package ext_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/ext"
)

func TestInstallMethodsPersistAndReinstallRecordsCurrentMethod(t *testing.T) {
	e := boot(t)
	dir := writeManifest(t, manifest("directory-method", "http://127.0.0.1:1/hook"))
	directory, err := e.svc.InstallDir(e.ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if directory.Extension.InstallSource != ext.InstallSourceDirectory {
		t.Fatal("arbitrary folder incorrectly treated as shipped package")
	}
	raw := `{"slug":"upload-method","name":{"en":"Uploaded example","ar":"مثال مرفوع"},"version":"1.0.0","endpoint":{"url":"http://127.0.0.1:1/hook","auth":"hmac"}}`
	archive := zipOf(t, map[string]string{"manifest.json": raw})
	uploaded, err := e.svc.InstallZip(e.ctx, bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	if uploaded.Extension.InstallSource != ext.InstallSourceUpload {
		t.Fatal("ZIP method missing")
	}
	for _, x := range []*ext.Extension{directory.Extension, uploaded.Extension} {
		stored, err := e.svc.Status(e.ctx, x.Slug)
		if err != nil || stored.InstallSource != x.InstallSource {
			t.Fatal("method not persisted", err)
		}
	}
	legacy := e.install(manifest("legacy-method", "http://127.0.0.1:1/legacy"))
	if _, err = e.app.Deps.DB.Exec(`UPDATE extensions SET install_source='legacy_unknown' WHERE slug=?`, legacy.Extension.Slug); err != nil {
		t.Fatal(err)
	}
	b := login(t, e, "admin@example.com", "admin")
	_, initial := b.do("GET", "/admin/extensions", nil, "")
	for source, slug := range map[string]string{"directory": "directory-method", "upload": "upload-method", "legacy_unknown": "legacy-method"} {
		start := strings.Index(initial, `data-install-source="`+source+`"`)
		if start < 0 {
			t.Fatal("missing method group", source)
		}
		section := initial[start:]
		if end := strings.Index(section, "</section>"); end >= 0 {
			section = section[:end]
		}
		if !strings.Contains(section, `data-ext="`+slug+`"`) {
			t.Fatal("misclassified extension", source, slug)
		}
	}
	if err = e.svc.Uninstall(e.ctx, "upload-method", false); err != nil {
		t.Fatal(err)
	}
	reinstalled, err := e.svc.InstallDir(e.ctx, filepath.Join(e.app.Deps.Config.DataDir, "extensions", "upload-method"))
	if err != nil {
		t.Fatal(err)
	}
	if reinstalled.Extension.InstallSource != ext.InstallSourceDirectory {
		t.Fatal("reinstall must describe current method, not infer original package provenance")
	}
	for _, loc := range []string{"en", "ar"} {
		if _, err = e.app.Deps.DB.Exec(`UPDATE users SET locale=? WHERE id=?`, loc, e.admin.ID); err != nil {
			t.Fatal(err)
		}
		response, page := b.do("GET", "/admin/extensions", nil, "")
		if response.StatusCode != 200 || !strings.Contains(page, `data-install-source="directory"`) || !strings.Contains(page, `data-source="directory"`) {
			t.Fatal("group or method missing", response.StatusCode)
		}
		expected := "Server directory"
		if loc == "ar" {
			expected = "من مجلد الخادم"
		}
		if !strings.Contains(page, expected) || strings.Contains(page, "ext.source.") {
			t.Fatal("source translation missing", loc)
		}
		response, page = b.do("GET", "/admin/extensions/upload-method", nil, "")
		if response.StatusCode != 200 || !strings.Contains(page, expected) || strings.Contains(page, "data-reveal-secret") {
			t.Fatal("detail provenance or private reveal boundary", loc, response.StatusCode)
		}
	}
}
