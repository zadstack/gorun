package media

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestS3ProbeBucketSignedReadOnly(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "HEAD" || r.URL.Path != "/example-bucket" {
			t.Errorf("unexpected operation %s %s", r.Method, r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") || !strings.Contains(r.Header.Get("Authorization"), "Credential=example-access/") {
			t.Error("missing signature")
		}
		if r.ContentLength > 0 {
			t.Error("unexpected body")
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	driver, err := NewS3Storage(S3Config{Endpoint: srv.URL, Region: "auto", Bucket: "example-bucket", AccessKey: "example-access", SecretKey: "example-secret"}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = driver.ProbeBucket(context.Background(), srv.Client()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	if driver.ProbeBucket(context.Background(), nil) == nil {
		t.Fatal("nil client accepted")
	}
}
