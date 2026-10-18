package httpx

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var bigText = strings.Repeat("GoRun compresses text responses. ", 200)

func serveCompressed(t *testing.T, method, path, accept string, h http.HandlerFunc, hdr ...string) *http.Response {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	if accept != "" {
		r.Header.Set("Accept-Encoding", accept)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	Compress(h).ServeHTTP(rec, r)
	return rec.Result()
}

func textHandler(ct string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Content-Length", "6600")
		_, _ = io.WriteString(w, bigText)
	}
}

func TestCompressGzipsTextResponses(t *testing.T) {
	for _, ct := range []string{"text/html; charset=utf-8", "text/css", "text/javascript; charset=utf-8", "application/json", "image/svg+xml"} {
		resp := serveCompressed(t, "GET", "/", "br, gzip;q=0.8", textHandler(ct))
		if resp.Header.Get("Content-Encoding") != "gzip" || resp.Header.Get("Content-Length") != "" {
			t.Fatalf("%s: headers %v", ct, resp.Header)
		}
		if !strings.Contains(resp.Header.Get("Vary"), "Accept-Encoding") {
			t.Fatalf("%s: missing Vary", ct)
		}
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			t.Fatalf("%s: %v", ct, err)
		}
		body, _ := io.ReadAll(zr)
		if string(body) != bigText {
			t.Fatalf("%s: body mismatch (%d bytes)", ct, len(body))
		}
	}
}

func TestCompressLeavesOtherResponsesAlone(t *testing.T) {
	cases := []struct {
		name, method, path, accept, ct string
		hdr                            []string
	}{
		{"no accept-encoding", "GET", "/", "", "text/html", nil},
		{"gzip refused", "GET", "/", "gzip;q=0, br", "text/html", nil},
		{"identity only", "GET", "/", "identity", "text/html", nil},
		{"binary type", "GET", "/media/a.png", "gzip", "image/png", nil},
		{"event stream", "GET", "/mcp", "gzip", "text/event-stream", nil},
		{"admin page (BREACH)", "GET", "/admin/content", "gzip", "text/html", nil},
		{"admin root", "GET", "/admin", "gzip", "text/html", nil},
		{"range request", "GET", "/static/admin/app.js", "gzip", "text/javascript", []string{"Range", "bytes=0-10"}},
		{"head", "HEAD", "/", "gzip", "text/html", nil},
	}
	for _, c := range cases {
		resp := serveCompressed(t, c.method, c.path, c.accept, textHandler(c.ct), c.hdr...)
		if resp.Header.Get("Content-Encoding") != "" {
			t.Fatalf("%s: must not be compressed: %v", c.name, resp.Header)
		}
		body, _ := io.ReadAll(resp.Body)
		if c.method == "GET" && string(body) != bigText {
			t.Fatalf("%s: body altered", c.name)
		}
	}
}

func TestCompressSkipsAlreadyEncodedAndBodilessStatuses(t *testing.T) {
	pre := serveCompressed(t, "GET", "/", "gzip", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Encoding", "br")
		_, _ = w.Write([]byte("already"))
	})
	if pre.Header.Get("Content-Encoding") != "br" {
		t.Fatalf("existing encoding replaced: %v", pre.Header)
	}
	for _, code := range []int{http.StatusNoContent, http.StatusNotModified} {
		resp := serveCompressed(t, "GET", "/", "gzip", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(code)
		})
		body, _ := io.ReadAll(resp.Body)
		if resp.Header.Get("Content-Encoding") != "" || len(body) != 0 {
			t.Fatalf("%d: encoding %q body %q", code, resp.Header.Get("Content-Encoding"), body)
		}
	}
}

func TestCompressDetectsTypeAndWeakensETag(t *testing.T) {
	resp := serveCompressed(t, "GET", "/", "gzip", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"abc"`)
		_, _ = io.WriteString(w, "<!doctype html><html><body>"+bigText)
	})
	if resp.Header.Get("Content-Encoding") != "gzip" || resp.Header.Get("ETag") != `W/"abc"` {
		t.Fatalf("headers %v", resp.Header)
	}
}

func TestCompressFlushStreamsCompressedBytes(t *testing.T) {
	srv := httptest.NewServer(Compress(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "first ")
		w.(http.Flusher).Flush()
		_ = http.NewResponseController(w).Flush()
		_, _ = io.WriteString(w, "second")
	})))
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.Header.Set("Accept-Encoding", "gzip") // disable the transport's transparent decoding
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(zr)
	if string(body) != "first second" {
		t.Fatalf("body %q", body)
	}
}

func TestAcceptsGzip(t *testing.T) {
	for in, want := range map[string]bool{
		"": false, "gzip": true, "GZIP": true, "deflate, gzip;q=1.0": true, "gzip;q=0": false,
		"*": true, "*;q=0": false, "br": false, "gzip;q=0, *": false, "x-gzip": true, "br;q=1, *;q=0.1": true,
	} {
		if got := acceptsGzip(in); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestAssetCacheControl(t *testing.T) {
	for _, c := range []struct{ url, name, want string }{
		{"/static/admin/vendor/htmx-org-2.0.11.js", "vendor/htmx-org-2.0.11.js", CacheImmutable},
		{"/static/admin/vendor/tailwindcss-3.4.17.js", "vendor/tailwindcss-3.4.17.js", CacheImmutable},
		{"/static/admin/vendor/lucide-core-1.55.0.js", "vendor/lucide-core-1.55.0.js", CacheImmutable},
		{"/themes/default/assets/shell.js?v=1792265354", "shell.js", CacheImmutable},
		{"/static/admin/app.js", "app.js", CacheShort},
		{"/static/admin/vendor/codemirror-editor.js", "vendor/codemirror-editor.js", CacheShort},
		{"/static/admin/fonts/zad/zad-1.woff2", "fonts/zad/zad-1.woff2", CacheShort},
	} {
		r := httptest.NewRequest("GET", c.url, nil)
		if got := AssetCacheControl(r, c.name); got != c.want {
			t.Errorf("%s: %q, want %q", c.url, got, c.want)
		}
	}
}
