package wpmigrate

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An inline image the export references but the source cannot serve keeps
// the export's own scheme in the imported body, and the validation report
// ties the failure to the article that still shows it.
func TestWXRFailedInlineImageKeepsSchemeAndBlocksArticle(t *testing.T) {
	e := newEnv(t)
	sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing.png" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(tinyPNG())
	}))
	defer sourceServer.Close()
	raw, err := os.ReadFile(writeSample(t, sourceServer.URL))
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte(`<figure class="wp-block-image size-large">`), []byte(`<img src="/missing.png" alt="lost"><figure class="wp-block-image size-large">`), 1)
	file := filepath.Join(t.TempDir(), "failed-inline.xml")
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	res, err := ImportWXRFile(e.ctx(), e.app.Deps, file, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Counts["media"][ItemFailed] == 0 {
		t.Fatalf("the missing image should fail to import: %+v", res.Counts)
	}
	var id, body string
	if err := e.svc.db.QueryRow(`SELECT id, body FROM content WHERE body LIKE '%alt="lost"%'`).Scan(&id, &body); err != nil {
		t.Fatal(err)
	}
	want := sourceServer.URL + "/missing.png"
	if !strings.Contains(body, `src="`+want+`"`) {
		t.Fatalf("retained image should keep the export's scheme %q: %s", want, body)
	}

	e.svc.cfg.BaseURL = e.srv.URL
	e.svc.check = e.svc.httpCheck
	rep, err := e.svc.Validate(e.ctx(), res.MigrationID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Readiness != "blocked" {
		t.Fatalf("readiness %q", rep.Readiness)
	}
	linked := false
	for _, issue := range rep.Errors {
		if issue.Kind == "media" && issue.Code == "retained_source_media" && issue.ContentID == id {
			linked = true
		}
	}
	if !linked {
		t.Fatalf("no media error tied to article %s: %+v", id, rep.Errors)
	}
	listed := false
	for _, m := range rep.MissingMedia {
		if m.ContentID == id {
			listed = true
		}
	}
	if !listed {
		t.Fatalf("article %s missing from missing_media: %+v", id, rep.MissingMedia)
	}
}
