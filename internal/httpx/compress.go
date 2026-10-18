package httpx

import (
	"bufio"
	"compress/gzip"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// compressibleTypes are the response media types Compress gzips. Images
// (other than SVG), fonts, archives and video are already compressed, and
// text/event-stream must reach the client unbuffered.
var compressibleTypes = map[string]bool{
	"text/html": true, "text/css": true, "text/plain": true, "text/javascript": true, "text/xml": true,
	"text/csv": true, "text/markdown": true, "application/javascript": true, "application/json": true,
	"application/problem+json": true, "application/xml": true, "application/rss+xml": true,
	"application/atom+xml": true, "application/manifest+json": true, "application/ld+json": true,
	"image/svg+xml": true,
}

var gzipPool = sync.Pool{New: func() any {
	w, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression)
	return w
}}

// Compress gzips text responses (see compressibleTypes) for clients that
// accept gzip. It leaves alone: responses that already have a
// Content-Encoding, range requests, bodiless statuses, and admin pages
// (/admin and /admin/...), which embed the session's CSRF token next to
// user-controlled text and so stay uncompressed to rule out BREACH-style
// length oracles. Admin static assets live under /static/admin/ and are
// compressed. It always adds Vary: Accept-Encoding to compressible paths.
func Compress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/admin/") || r.Header.Get("Range") != "" {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Accept-Encoding")
		if !acceptsGzip(r.Header.Get("Accept-Encoding")) {
			next.ServeHTTP(w, r)
			return
		}
		cw := &gzipResponseWriter{ResponseWriter: w, head: r.Method == http.MethodHead}
		defer cw.close()
		next.ServeHTTP(cw, r)
	})
}

// acceptsGzip reports whether an Accept-Encoding value allows gzip (an
// explicit gzip or * entry whose q is not 0).
func acceptsGzip(header string) bool {
	gzipQ, starQ := -1.0, -1.0
	for _, part := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		q := 1.0
		for _, p := range strings.Split(params, ";") {
			if k, v, ok := strings.Cut(strings.TrimSpace(p), "="); ok && strings.EqualFold(k, "q") {
				if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					q = f
				}
			}
		}
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "gzip", "x-gzip":
			gzipQ = q
		case "*":
			starQ = q
		}
	}
	if gzipQ >= 0 {
		return gzipQ > 0
	}
	return starQ > 0
}

type gzipResponseWriter struct {
	http.ResponseWriter
	head        bool
	wroteHeader bool
	gz          *gzip.Writer // nil unless this response is compressed
}

// decide runs once, before the first byte: compress only a compressible
// body that nobody has encoded yet.
func (w *gzipResponseWriter) decide(status int) {
	h := w.Header()
	mediaType, _, _ := strings.Cut(h.Get("Content-Type"), ";")
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if w.head || status < 200 || status == http.StatusNoContent || status == http.StatusNotModified ||
		status == http.StatusPartialContent || h.Get("Content-Encoding") != "" || h.Get("Content-Range") != "" ||
		!compressibleTypes[mediaType] {
		return
	}
	h.Del("Content-Length")
	h.Set("Content-Encoding", "gzip")
	if etag := h.Get("ETag"); etag != "" && !strings.HasPrefix(etag, "W/") {
		h.Set("ETag", "W/"+etag) // the encoded bytes differ from the identity representation
	}
	gz := gzipPool.Get().(*gzip.Writer)
	gz.Reset(w.ResponseWriter)
	w.gz = gz
}

func (w *gzipResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if status >= 100 && status < 200 { // informational: the real header comes later
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.wroteHeader = true
	w.decide(status)
	w.ResponseWriter.WriteHeader(status)
}

func (w *gzipResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", http.DetectContentType(p))
		}
		w.WriteHeader(http.StatusOK)
	}
	if w.gz != nil {
		return w.gz.Write(p)
	}
	return w.ResponseWriter.Write(p)
}

// Flush pushes buffered compressed bytes to the client.
func (w *gzipResponseWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.gz != nil {
		_ = w.gz.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack hands the connection over (websocket upgrades) when the
// underlying writer supports it and nothing was compressed yet.
func (w *gzipResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if w.gz != nil {
		return nil, nil, errors.New("httpx: cannot hijack a compressed response")
	}
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *gzipResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *gzipResponseWriter) close() {
	if w.gz == nil {
		return
	}
	_ = w.gz.Close()
	w.gz.Reset(io.Discard)
	gzipPool.Put(w.gz)
	w.gz = nil
}
