package wpmigrate

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWXRUncataloguedSrcsetImport(t *testing.T) {
	e := newEnv(t)
	sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(tinyPNG())
	}))
	defer sourceServer.Close()
	raw, err := os.ReadFile(writeSample(t, sourceServer.URL))
	if err != nil {
		t.Fatal(err)
	}
	source := sourceServer.URL + "/image.png"
	raw = bytes.Replace(raw, []byte(`alt="كوباباو"`), []byte(`srcset="`+source+` 2x" alt="كوباباو"`), 1)
	file := filepath.Join(t.TempDir(), "inline.xml")
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	before := e.count(`SELECT count(*) FROM media`)
	if _, err := PreviewWXR(e.ctx(), e.app.Deps, WXRFile{Path: file}, Options{}); err != nil {
		t.Fatal(err)
	}
	if e.count(`SELECT count(*) FROM media`) != before {
		t.Fatal("dry run wrote media")
	}
	for i := 0; i < 2; i++ {
		res, err := ImportWXRFile(e.ctx(), e.app.Deps, file, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if res.Counts["media"][ItemFailed] != 0 {
			t.Fatalf("failed media: %+v", res)
		}
		var body string
		if err := e.svc.db.QueryRow(`SELECT body FROM content WHERE type='post' LIMIT 1`).Scan(&body); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(body, source) || !strings.Contains(body, `srcset="/media/`) {
			t.Fatalf("unmapped responsive image: %s", body)
		}
		for _, ref := range inlineImageReferences(body) {
			resp, err := http.Get(e.srv.URL + ref)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || resp.StatusCode != 200 || !bytes.Equal(payload, tinyPNG()) {
				t.Fatalf("bad migrated file: %s HTTP %d", ref, resp.StatusCode)
			}
		}
		if n := e.count(`SELECT count(*) FROM media`); n != before+2 {
			t.Fatalf("import %d has %d media", i, n)
		}
	}
}

func TestValidationArbitraryLocalSrcset(t *testing.T) {
	e := newEnv(t)
	id := e.wxrMigration(Options{})
	e.run(id)
	it := e.item(id, "content", "post", "70")
	body := `<picture><source srcset="/image.png?secret=hidden 1x, missing.png?secret=hidden 2x"></picture>`
	if _, err := e.svc.db.Exec(`UPDATE content SET body=? WHERE id=?`, body, it.LocalID); err != nil {
		t.Fatal(err)
	}
	e.svc.cfg.BaseURL = e.srv.URL
	e.svc.check = e.svc.httpCheck
	rep, err := e.svc.Validate(e.ctx(), id)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Readiness != "blocked" || len(rep.MissingMedia) != 2 {
		t.Fatalf("missing image not blocked: %+v", rep)
	}
	count := 0
	for _, issue := range rep.Errors {
		if issue.Kind == "media" && issue.Status == http.StatusNotFound {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("404 evidence missing: %+v", rep.Errors)
	}
	encoded, _ := json.Marshal(rep)
	if strings.Contains(string(encoded), "hidden") {
		t.Fatal("query leaked")
	}
}

func TestSrcsetURLsPreserveCommaAndData(t *testing.T) {
	input := "data:image/png;base64,AAAA 1x, https://cdn.test/a,b.png 2x, /image.png 3x"
	want := []string{"data:image/png;base64,AAAA", "https://cdn.test/a,b.png", "/image.png"}
	if got := srcsetReferences(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
	rw := &rewriter{}
	if got, _ := rw.rewrite(context.Background(), `<img srcset="`+input+`">`); got != `<img srcset="`+input+`">` {
		t.Fatalf("changed unimported URLs: %s", got)
	}
}
