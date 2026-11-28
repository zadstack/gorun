package services

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// fakeS3 is a path-style bucket store: HEAD /bucket, then PUT, GET and
// DELETE /bucket/key. fail maps a method to the status it answers instead.
type fakeS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
	fail    map[string]int
	methods []string
}

func newFakeS3() *fakeS3 { return &fakeS3{objects: map[string][]byte{}, fail: map[string]int{}} }

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.methods = append(f.methods, r.Method)
	if code := f.fail[r.Method]; code != 0 {
		w.WriteHeader(code)
		return
	}
	bucket, key, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if bucket == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodHead:
		w.WriteHeader(http.StatusOK)
	case http.MethodPut:
		b, _ := io.ReadAll(r.Body)
		f.objects[bucket+"/"+key] = b
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		b, ok := f.objects[bucket+"/"+key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(b)
	case http.MethodDelete:
		delete(f.objects, bucket+"/"+key)
		w.WriteHeader(http.StatusNoContent)
	}
}

// keys are the stored object keys (bucket/key).
func (f *fakeS3) keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.objects))
	for k := range f.objects {
		out = append(out, k)
	}
	return out
}

// client is an HTTP client that sends every request, whatever its host,
// to f behind a test server.
func (f *fakeS3) client(t *testing.T) *http.Client {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	target, _ := url.Parse(srv.URL)
	return &http.Client{Transport: rewriteTransport{target}}
}

type rewriteTransport struct{ target *url.URL }

func (rt rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	out := r.Clone(r.Context())
	out.URL.Scheme, out.URL.Host, out.Host = rt.target.Scheme, rt.target.Host, rt.target.Host
	return http.DefaultTransport.RoundTrip(out)
}
