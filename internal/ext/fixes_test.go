package ext_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/ext"
)

type zentry struct{ name, body string }

// zipEntries builds a zip with entries in order; names ending in "/" are
// directory entries (as written by `zip -r` and Finder).
func zipEntries(t *testing.T, entries ...zentry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(e.name, "/") {
			w.Write([]byte(e.body))
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const helloRaw = `{"slug":"hello","name":{"en":"Hello"},"version":"1.0.0","permissions":[],"hooks":[],"endpoint":{"url":"","auth":""}}`

func TestInstallZipWithDirectoryEntries(t *testing.T) {
	e := boot(t)
	data := zipEntries(t, zentry{name: "hello/"}, zentry{"hello/manifest.json", helloRaw},
		zentry{name: "hello/assets/"}, zentry{"hello/assets/a.txt", "a"})
	if _, err := e.svc.InstallZip(e.ctx, bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatalf("zip with directory entries: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.app.Deps.Config.DataDir, "extensions", "hello", "assets", "a.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestInstallZipDuplicateEntriesIsValidationError(t *testing.T) {
	e := boot(t)
	data := zipEntries(t, zentry{"manifest.json", helloRaw}, zentry{"assets/a.txt", "a"}, zentry{"assets/a.txt", "b"})
	_, err := e.svc.InstallZip(e.ctx, bytes.NewReader(data), int64(len(data)))
	var ve *ext.ValidationError
	if !errors.As(err, &ve) || ve.Fields["package"] != "ext.package.duplicate_entry" {
		t.Fatalf("duplicate entries: %v", err)
	}
}

func TestBadManifestDetailSeparateFromFields(t *testing.T) {
	e := boot(t)
	data := zipEntries(t, zentry{"manifest.json", `{"slug":"x","bogus":true}`})
	_, err := e.svc.InstallZip(e.ctx, bytes.NewReader(data), int64(len(data)))
	var ve *ext.ValidationError
	if !errors.As(err, &ve) || ve.Fields["package"] != "ext.package.bad_manifest" || len(ve.Fields) != 1 || !strings.Contains(ve.Detail, "bogus") {
		t.Fatalf("bad manifest: %#v", err)
	}
	b := login(t, e, "admin@example.com", "admin")
	resp, page := b.upload("/admin/extensions/install", zipEntries(t, zentry{"manifest.json", `{"slug":"<b>x</b>"`}))
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(page, "<pre") || strings.Contains(page, "<b>x</b>") {
		t.Fatalf("detail must be shown escaped in a <pre>: %d\n%s", resp.StatusCode, page)
	}
}

func TestPurgeSkipsMissingTypesAndLeavesOtherContent(t *testing.T) {
	e := boot(t)
	e.install(sampleManifest("http://127.0.0.1:1/hook"))
	post, err := e.content.Create(e.ctx, content.Input{Type: "post", Title: "Keep me", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.content.DeleteType(e.ctx, "greeting"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Uninstall(e.ctx, "sample", true); err != nil {
		t.Fatalf("purge with a missing type: %v", err)
	}
	if _, err := e.content.Get(context.Background(), post.ID); err != nil {
		t.Fatalf("purge touched other content: %v", err)
	}
	var audits int
	e.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'ext.uninstall'`).Scan(&audits)
	if audits != 1 {
		t.Fatalf("ext.uninstall audits = %d", audits)
	}
}

func TestReinstallKeepsAdminEditsToKeptTypes(t *testing.T) {
	e := boot(t)
	e.install(sampleManifest("http://127.0.0.1:1/hook"))
	typ, _ := e.content.GetType(context.Background(), "greeting")
	typ.Fields = append(typ.Fields, content.Field{Key: "extra", Label: map[string]string{"en": "Extra"}, Type: "text"})
	if err := e.content.SaveType(e.ctx, *typ); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Uninstall(e.ctx, "sample", false); err != nil {
		t.Fatal(err)
	}
	e.install(sampleManifest("http://127.0.0.1:1/hook"))
	typ, _ = e.content.GetType(context.Background(), "greeting")
	if len(typ.Fields) != 2 {
		t.Fatalf("reinstall overwrote the edited type: %+v", typ.Fields)
	}
}

func TestFilterPartialResponseMerges(t *testing.T) {
	e := boot(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"value":{"title":"Renamed"}}`))
	}))
	t.Cleanup(srv.Close)
	e.install(manifest("partial", srv.URL, hook("content.before_save", "filter", 2000)))
	if err := e.svc.Activate(e.ctx, "partial"); err != nil {
		t.Fatal(err)
	}
	it, err := e.content.Create(e.ctx, content.Input{Type: "post", Title: "Orig", Body: "<p>body</p>", Status: "draft"})
	if err != nil {
		t.Fatalf("partial filter response must not fail the save: %v", err)
	}
	if it.Title != "Renamed" || it.Type != "post" || !strings.Contains(it.Body, "body") {
		t.Fatalf("partial response must merge: %+v", it)
	}
}

func TestFilterSuccessResetsFailureCounter(t *testing.T) {
	e := boot(t)
	var fail atomic.Bool
	fail.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	e.install(manifest("flap", srv.URL, hook("content.before_save", "filter", 2000)))
	if err := e.svc.Activate(e.ctx, "flap"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		e.content.Create(e.ctx, content.Input{Type: "post", Title: "x", Status: "draft"})
	}
	if x := e.status("flap"); x.Failures != 3 {
		t.Fatalf("failures = %d", x.Failures)
	}
	fail.Store(false)
	e.content.Create(e.ctx, content.Input{Type: "post", Title: "y", Status: "draft"})
	if x := e.status("flap"); x.Failures != 0 || x.TotalFailures != 3 {
		t.Fatalf("success must reset the counter: %+v", x)
	}
}

func TestInstallRevealIsNotCached(t *testing.T) {
	e := boot(t)
	b := login(t, e, "admin@example.com", "admin")
	resp, _ := b.upload("/admin/extensions/install", zipEntries(t, zentry{"manifest.json", helloRaw}))
	if resp.StatusCode != http.StatusCreated || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("reveal: %d cache-control=%q", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
}
