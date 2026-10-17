package api_test

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/media"
)

func TestMediaREST413And415ErrorEnvelopes(t *testing.T) {
	app := apitest.Boot(t, content.New(), media.New())
	if err := app.Deps.Settings.Set(context.Background(), "media.max_upload_mb", 1); err != nil {
		t.Fatal(err)
	}
	c := apitest.NewClient(t, app)
	for _, tc := range []struct {
		name, content, code string
		status              int
	}{
		{"too large", strings.Repeat("x", (1<<20)+1), "too_large", 413},
		{"unsupported", "<html>Not a PNG</html>", "unsupported_media_type", 415},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			writer := multipart.NewWriter(&buf)
			file, err := writer.CreateFormFile("file", "fixture.png")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = file.Write([]byte(tc.content)); err != nil {
				t.Fatal(err)
			}
			if err = writer.Close(); err != nil {
				t.Fatal(err)
			}
			w := c.Do("POST", "/media", buf.Bytes(), http.Header{"Content-Type": {writer.FormDataContentType()}})
			if w.Code != tc.status || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) {
				t.Fatalf("upload: %d %s", w.Code, w.Body.String())
			}
			var count int
			if err := app.Deps.DB.QueryRow("SELECT COUNT(*) FROM media").Scan(&count); err != nil || count != 0 {
				t.Fatalf("failed upload persisted media: %d %v", count, err)
			}
		})
	}
}
