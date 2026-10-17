package media

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/settings"
)

type destinationTransport func(*http.Request) (*http.Response, error)

func (f destinationTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestManagedDestinationsKeepOldFilesWhenSwitchingOrDisabled(t *testing.T) {
	database := newTestDB(t)
	store := settings.New(database)
	ctx := context.Background()
	secret := make([]byte, 32)
	objects := map[string][]byte{}
	client := &http.Client{Transport: destinationTransport(func(r *http.Request) (*http.Response, error) {
		key := r.URL.String()
		status := 200
		var body []byte
		switch r.Method {
		case "PUT":
			body, _ = io.ReadAll(r.Body)
			objects[key] = body
		case "GET":
			var ok bool
			body, ok = objects[key]
			if !ok {
				status = 404
			}
		case "DELETE":
			delete(objects, key)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
	})}
	managed := &ManagedStorage{db: database, settings: store, secret: secret, fallback: NewLocalStorage(t.TempDir()), client: client}
	activate := func(bucket, access string) Destination {
		t.Helper()
		dest, err := PrepareDestination("Test", "media", S3Config{Endpoint: "https://objects.example.com", Bucket: bucket, AccessKey: access, SecretKey: "fake-test-secret"}, secret)
		if err != nil {
			t.Fatal(err)
		}
		if err = SaveDestination(ctx, database, store, dest); err != nil {
			t.Fatal(err)
		}
		return dest
	}
	first := activate("bucket-one", "fake-a")
	key, err := managed.UploadKey(ctx, "2026/10/fixture.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if err = managed.Put(ctx, key, strings.NewReader("original"), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	second := activate("bucket-two", "fake-b")
	if first.ID == second.ID {
		t.Fatal("bucket change reused destination")
	}
	if err = store.Set(ctx, "storage.active.media", ""); err != nil {
		t.Fatal(err)
	}
	rc, err := managed.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if string(mustRead(t, rc)) != "original" {
		t.Fatal("old object stranded")
	}
	rotated := activate("bucket-one", "fake-rotated")
	if rotated.ID != first.ID {
		t.Fatal("credential rotation changed object namespace")
	}
	var raw string
	if err = database.QueryRow(`SELECT value_json FROM settings WHERE key=?`, DestinationPrefix+first.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "fake-rotated") || strings.Contains(raw, "fake-test-secret") {
		t.Fatal("plaintext credentials stored")
	}
	if _, err = managed.Open(ctx, "remote/"+first.ID+"/sites/01m4qaqm4rp3t0ysghn6n7hjws/uploads/2026/10/fixture.jpg"); err == nil {
		t.Fatal("cross-site object namespace accepted")
	}
	if _, err = managed.Open(ctx, "remote/"+second.ID+"/backups/private.db.gorb"); err == nil {
		t.Fatal("backup path accepted as media")
	}
}

func TestSafeStorageClientDoesNotForwardCredentialsOnRedirect(t *testing.T) {
	calls := 0
	client := SafeStorageClient()
	client.Transport = destinationTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://different.example.com/stolen"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	driver, err := NewS3Storage(S3Config{Endpoint: "https://objects.example.com", Bucket: "backup-fixture", AccessKey: "fake-access", SecretKey: "fake-secret"}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	err = driver.PutEncryptedBackup(context.Background(), client, "backups/01m4qaqm4rp3t0ysghn6n7hjws/2026/10/12/20261012T120000Z.db.gorb", []byte("encrypted fixture"))
	if err == nil || calls != 1 {
		t.Fatal("redirect followed or accepted")
	}
}

func TestSafeStorageClientRejectsPrivateDestinations(t *testing.T) {
	for _, endpoint := range []string{"https://127.0.0.1:9", "https://[::1]:9", "https://169.254.169.254"} {
		t.Run(endpoint, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := SafeStorageClient().Do(req)
			if resp != nil {
				resp.Body.Close()
			}
			if err == nil || !strings.Contains(err.Error(), "blocked") {
				t.Fatalf("private address not rejected by policy: %v", err)
			}
		})
	}
}
