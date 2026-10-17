package media

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeS3 is a path-style bucket that checks every request is signed for its payload.
type fakeS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
	types   map[string]string
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AK/") || !strings.Contains(auth, "/auto/s3/aws4_request") ||
		r.Header.Get("x-amz-content-sha256") != hexSHA256(body) {
		http.Error(w, "SignatureDoesNotMatch", http.StatusForbidden)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/bucket/")
	switch r.Method {
	case http.MethodPut:
		f.objects[key], f.types[key] = body, r.Header.Get("Content-Type")
	case http.MethodGet:
		b, ok := f.objects[key]
		if !ok {
			http.Error(w, "NoSuchKey", http.StatusNotFound)
			return
		}
		w.Write(b)
	case http.MethodDelete:
		delete(f.objects, key)
		w.WriteHeader(http.StatusNoContent)
	}
}

func TestS3StoragePutOpenDeleteWithLocalFallback(t *testing.T) {
	fake := &fakeS3{objects: map[string][]byte{}, types: map[string]string{}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	local := NewLocalStorage(t.TempDir())
	ctx := context.Background()
	if err := local.Put(ctx, "2026/01/old.png", strings.NewReader("old"), "image/png"); err != nil {
		t.Fatal(err)
	}
	s, err := NewS3Storage(S3Config{Endpoint: srv.URL, Region: "auto", Bucket: "bucket", AccessKey: "AK", SecretKey: "SK"}, local, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "2026/10/new.png", bytes.NewReader([]byte("new")), "image/png"); err != nil {
		t.Fatal(err)
	}
	if string(fake.objects["2026/10/new.png"]) != "new" || fake.types["2026/10/new.png"] != "image/png" {
		t.Fatalf("stored: %v", fake.objects)
	}
	for key, want := range map[string]string{"2026/10/new.png": "new", "2026/01/old.png": "old"} {
		rc, err := s.Open(ctx, key)
		if err != nil {
			t.Fatalf("open %s: %v", key, err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		if string(b) != want {
			t.Fatalf("%s: %q", key, b)
		}
	}
	if _, err := s.Open(ctx, "2026/10/none.png"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := s.Open(ctx, "../x"); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("bad key: %v", err)
	}
	if err := s.Delete(ctx, "2026/10/new.png"); err != nil || fake.objects["2026/10/new.png"] != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.Delete(ctx, "2026/01/old.png"); err != nil {
		t.Fatal(err)
	}
	if _, err := local.Open(ctx, "2026/01/old.png"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("local copy kept: %v", err)
	}
	if got := s.URL("2026/10/new.png"); got != "/media/2026/10/new.png" {
		t.Fatalf("url: %s", got)
	}
	pub, _ := NewS3Storage(S3Config{Endpoint: srv.URL, Region: "auto", Bucket: "bucket", AccessKey: "AK", SecretKey: "SK",
		PublicURL: "https://cdn.example.com/"}, local, true)
	if got := pub.URL("2026/10/new.png"); got != "https://cdn.example.com/2026/10/new.png" {
		t.Fatalf("public url: %s", got)
	}
	if got := pub.URL("2026/10/logo.svg"); got != "/media/2026/10/logo.svg" {
		t.Fatalf("svg keeps the sandboxing CSP of /media/: %s", got)
	}
	// a wrong secret is reported, not hidden
	fake.objects = map[string][]byte{}
	bad, _ := NewS3Storage(S3Config{Endpoint: srv.URL, Region: "auto", Bucket: "bucket", AccessKey: "XX", SecretKey: "SK"}, local, true)
	if err := bad.Put(ctx, "2026/10/x.png", strings.NewReader("x"), "image/png"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("refused put: %v", err)
	}
}

func TestS3ConfigValidation(t *testing.T) {
	local := NewLocalStorage(t.TempDir())
	for name, c := range map[string]S3Config{
		"http in prod": {Endpoint: "http://s3.example.com", Bucket: "b", AccessKey: "a", SecretKey: "s"},
		"no bucket":    {Endpoint: "https://s3.example.com", AccessKey: "a", SecretKey: "s"},
		"no keys":      {Endpoint: "https://s3.example.com", Bucket: "b"},
		"bad bucket":   {Endpoint: "https://s3.example.com", Bucket: "a/b", AccessKey: "a", SecretKey: "s"},
		"path":         {Endpoint: "https://s3.example.com/x", Bucket: "b", AccessKey: "a", SecretKey: "s"},
		"public http":  {Endpoint: "https://s3.example.com", Bucket: "b", AccessKey: "a", SecretKey: "s", PublicURL: "http://cdn.example.com"},
	} {
		if _, err := NewS3Storage(c, local, false); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	s, err := NewS3Storage(S3Config{Endpoint: "https://acc.r2.cloudflarestorage.com", Bucket: "media", AccessKey: "a", SecretKey: "s"}, local, false)
	if err != nil || s.region != "auto" {
		t.Fatalf("r2 default region: %v %+v", err, s)
	}
}
