package admin

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/media"
)

type backupTransport func(*http.Request) (*http.Response, error)

func (f backupTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestRemoteBackupUploadsOnlyAuthenticatedCiphertextAndRetainsLocalOnFailure(t *testing.T) {
	key := bytes.Repeat([]byte{4}, 32)
	dest, err := media.PrepareDestination("Fixture", "backup", media.S3Config{Endpoint: "https://objects.example.com", Region: "auto", Bucket: "private-backups", AccessKey: "fixture-access", SecretKey: "fixture-secret"}, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	res := backup.Result{Stamp: "20261012T120000Z", DB: filepath.Join(dir, "20261012T120000Z.db"), Files: filepath.Join(dir, "20261012T120000Z.tar.gz")}
	for _, p := range []string{res.DB, res.Files} {
		if err = os.WriteFile(p, []byte("sensitive synthetic fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, failSecond := range []bool{false, true} {
		calls := 0
		client := &http.Client{Transport: backupTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			body, _ := io.ReadAll(r.Body)
			if bytes.Contains(body, []byte("sensitive synthetic fixture")) {
				t.Fatal("plaintext sent")
			}
			if r.Method != "PUT" || !strings.Contains(r.URL.Path, "/backups/01m4qaqm4rp3t0ysghn6n7hjws/2026/10/12/") || !strings.HasSuffix(r.URL.Path, ".gorb") {
				t.Fatal("invalid backup path")
			}
			if r.Header.Get("Authorization") == "" {
				t.Fatal("unsigned upload")
			}
			plain, err := backup.Decrypt(bytes.NewReader(body), key, strings.TrimSuffix(filepath.Base(r.URL.Path), ".gorb"))
			if err != nil || string(plain) != "sensitive synthetic fixture" {
				t.Fatal("unrecoverable upload")
			}
			status := 200
			if failSecond && calls == 2 {
				status = 403
			}
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("fixture-secret must not reach errors")), Header: http.Header{}}, nil
		})}
		receipt, uploadErr := uploadEncryptedBackup(context.Background(), res, dest, key, "01m4qaqm4rp3t0ysghn6n7hjws", client)
		err = uploadErr
		if failSecond && (err == nil || strings.Contains(err.Error(), "fixture-secret")) {
			t.Fatal("failure not safely reported")
		}
		if !failSecond && err != nil {
			t.Fatal(err)
		}
		if receipt.ReadBackVerified || receipt.Files[0].State != "uploaded" || (failSecond && (receipt.State != "partial" || receipt.Files[1].State != "failed")) || (!failSecond && receipt.State != "uploaded") {
			t.Fatalf("incorrect completeness: %+v", receipt)
		}
		if calls != 2 {
			t.Fatal(calls)
		}
		for _, p := range []string{res.DB, res.Files} {
			if _, err = os.Stat(p); err != nil {
				t.Fatal("local backup removed on remote failure")
			}
		}
	}
}
