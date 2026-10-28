package admin

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/db"
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
	res := remoteBackupFixture(t, key, 0)
	for _, failSecond := range []bool{false, true} {
		puts := 0
		objects := map[string][]byte{}
		client := &http.Client{Transport: backupTransport(func(r *http.Request) (*http.Response, error) {
			if r.Header.Get("Authorization") == "" {
				t.Error("unsigned backup request")
			}
			if r.Method == http.MethodGet {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(objects[r.URL.Path])), Header: http.Header{}}, nil
			}
			puts++
			if r.Method != http.MethodPut || !strings.HasSuffix(r.URL.Path, ".gorb") {
				t.Error("unexpected network operation")
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
			if bytes.Contains(body, []byte("sensitive synthetic fixture")) {
				t.Error("plaintext sent")
			}
			plain, err := backup.Decrypt(bytes.NewReader(body), key, strings.TrimSuffix(filepath.Base(r.URL.Path), ".gorb"))
			if err != nil || len(plain) == 0 {
				t.Error("unrecoverable upload", err)
			}
			objects[r.URL.Path] = body
			status := 200
			if failSecond && puts == 2 {
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
		if receipt.ReadBackVerified == failSecond || receipt.Files[0].State != "uploaded" || (failSecond && (receipt.State != "partial" || receipt.Files[1].State != "failed")) || (!failSecond && receipt.State != "uploaded") {
			t.Fatalf("incorrect completeness: %+v", receipt)
		}
		if puts != 2 {
			t.Fatal(puts)
		}
		for _, p := range []string{res.DB, res.Files} {
			if _, err = os.Stat(p); err != nil {
				t.Fatal("local backup removed on remote failure")
			}
		}
	}
}

func TestRemoteBackupStreamsLargeArchiveAndVerifiesReadback(t *testing.T) {
	key := bytes.Repeat([]byte{4}, 32)
	dest, err := media.PrepareDestination("Fixture", "backup", media.S3Config{Endpoint: "https://objects.example.com", Region: "auto", Bucket: "private-backups", AccessKey: "fixture-access", SecretKey: "fixture-secret"}, key)
	if err != nil {
		t.Fatal(err)
	}
	res := remoteBackupFixture(t, key, 119<<20)
	large, err := os.Stat(res.Files)
	if err != nil || large.Size() <= 118<<20 {
		t.Fatal("large fixture missing", err)
	}
	dir := t.TempDir()
	puts, gets := 0, 0
	client := &http.Client{Transport: backupTransport(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") == "" {
			t.Error("unsigned private backup request")
		}
		object := filepath.Join(dir, filepath.Base(request.URL.Path))
		if request.Method == http.MethodPut {
			puts++
			file, err := os.OpenFile(object, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
			if err != nil {
				return nil, err
			}
			digest := sha256.New()
			n, copyErr := io.Copy(io.MultiWriter(file, digest), request.Body)
			closeErr := file.Close()
			if copyErr != nil {
				return nil, copyErr
			}
			if closeErr != nil {
				return nil, closeErr
			}
			if n != request.ContentLength || hex.EncodeToString(digest.Sum(nil)) != request.Header.Get("X-Amz-Content-Sha256") {
				t.Error("signed payload length or digest differs")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
		}
		if request.Method != http.MethodGet {
			t.Errorf("unexpected method %s", request.Method)
		}
		gets++
		file, err := os.Open(object)
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Body: file, Header: http.Header{}}, nil
	})}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	receipt, err := uploadEncryptedBackup(context.Background(), res, dest, key, "01m4qaqm4rp3t0ysghn6n7hjws", client)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.State != "uploaded" || puts != 2 || gets != 2 {
		t.Fatalf("not verified: %+v PUT=%d GET=%d", receipt, puts, gets)
	}
	t.Logf("119MiB encrypted upload/readback allocated %d bytes", after.TotalAlloc-before.TotalAlloc)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 32<<20 {
		t.Fatalf("119MiB upload/readback allocated %d bytes; expected bounded buffers", allocated)
	}
	for _, path := range []string{res.DB, res.Files} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("local backup lost", err)
		}
	}
}

func remoteBackupFixture(t *testing.T, key []byte, archiveSize int64) backup.Result {
	t.Helper()
	dir := t.TempDir()
	database := filepath.Join(dir, "gorun.db")
	source, err := db.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.Exec("CREATE TABLE fixture (message TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err = source.Exec("INSERT INTO fixture VALUES (?)", "sensitive synthetic fixture"); err != nil {
		t.Fatal(err)
	}
	source.Close()
	if err = os.Mkdir(filepath.Join(dir, "media"), 0o700); err != nil {
		t.Fatal(err)
	}
	mediaFile, err := os.OpenFile(filepath.Join(dir, "media", "fixture.bin"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if archiveSize > 0 {
		_, err = io.CopyN(mediaFile, rand.Reader, archiveSize)
	} else {
		_, err = mediaFile.Write([]byte("sensitive synthetic fixture"))
	}
	if err != nil {
		t.Fatal(err)
	}
	if err = mediaFile.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := backup.CreateWith(dir, database, time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC), backup.Options{SecretKey: key})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRemoteBackupReadbackRejectsTamperingAndRetainsLocal(t *testing.T) {
	key := bytes.Repeat([]byte{4}, 32)
	dest, err := media.PrepareDestination("Fixture", "backup", media.S3Config{Endpoint: "https://objects.example.com", Bucket: "private-backups", AccessKey: "access", SecretKey: "secret"}, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range []string{"tampered", "truncated", "wrong-content", "denied"} {
		t.Run(failure, func(t *testing.T) {
			res := remoteBackupFixture(t, key, 0)
			var ciphertext []byte
			client := &http.Client{Transport: backupTransport(func(request *http.Request) (*http.Response, error) {
				if request.Method == http.MethodPut {
					var err error
					ciphertext, err = io.ReadAll(request.Body)
					if err != nil {
						return nil, err
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}, nil
				}
				status := 200
				switch failure {
				case "tampered":
					ciphertext[len(ciphertext)-1] ^= 1
				case "truncated":
					ciphertext = ciphertext[:len(ciphertext)-1]
				case "wrong-content":
					ciphertext, err = backup.Encrypt(bytes.NewReader([]byte("different authenticated bytes")), key, filepath.Base(res.DB))
					if err != nil {
						return nil, err
					}
				case "denied":
					status = 403
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(ciphertext))}, nil
			})}
			receipt, err := uploadEncryptedBackup(context.Background(), res, dest, key, "01m4qaqm4rp3t0ysghn6n7hjws", client)
			if err == nil || receipt.ReadBackVerified || receipt.State != "partial" || receipt.Files[0].Code != "provider_readback_failed" {
				t.Fatalf("readback failure hidden: %+v %v", receipt, err)
			}
			for _, path := range []string{res.DB, res.Files} {
				if _, err = os.Stat(path); err != nil {
					t.Fatal("local backup lost", err)
				}
			}
			leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(res.DB), ".gorun-encrypted-*"))
			if err != nil || len(leftovers) != 0 {
				t.Fatal("ciphertext spool left behind", leftovers, err)
			}
		})
	}
}

func TestRemoteBackupChecksManifestBeforeSendingChangedFile(t *testing.T) {
	key := bytes.Repeat([]byte{4}, 32)
	res := remoteBackupFixture(t, key, 0)
	dest, err := media.PrepareDestination("Fixture", "backup", media.S3Config{Endpoint: "https://objects.example.com", Bucket: "private-backups", AccessKey: "access", SecretKey: "secret"}, key)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(res.DB, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.WriteAt([]byte("tampered"), 0); err != nil {
		t.Fatal(err)
	}
	file.Close()
	calls := 0
	client := &http.Client{Transport: backupTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected request") })}
	if _, err = uploadEncryptedBackup(context.Background(), res, dest, key, "01m4qaqm4rp3t0ysghn6n7hjws", client); err == nil || calls != 0 {
		t.Fatal("changed source uploaded", err, calls)
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(res.DB), ".gorun-encrypted-*"))
	if len(leftovers) != 0 {
		t.Fatal("failed encryption spool retained")
	}
}
