package admin

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/media"
)

// A backup with the analytics member uploads <stamp>-analytics.db as a third
// encrypted object, and its readback receipt records all three.
func TestRemoteBackupCarriesAnalyticsCopy(t *testing.T) {
	key := bytes.Repeat([]byte{4}, 32)
	dest, err := media.PrepareDestination("Fixture", "backup", media.S3Config{Endpoint: "https://objects.example.com", Region: "auto", Bucket: "private-backups", AccessKey: "fixture-access", SecretKey: "fixture-secret"}, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, name := range []string{"gorun.db", backup.AnalyticsFile} {
		d, err := db.Open(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.Exec(`CREATE TABLE fixture (v TEXT)`); err != nil {
			t.Fatal(err)
		}
		d.Close()
	}
	res, err := backup.CreateWith(dir, filepath.Join(dir, "gorun.db"), time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC), backup.Options{SecretKey: key})
	if err != nil || res.Analytics == "" {
		t.Fatalf("fixture: %+v %v", res, err)
	}
	objects := map[string][]byte{}
	client := &http.Client{Transport: backupTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(objects[r.URL.Path])), Header: http.Header{}}, nil
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		if _, err := backup.Decrypt(bytes.NewReader(body), key, strings.TrimSuffix(filepath.Base(r.URL.Path), ".gorb")); err != nil {
			t.Errorf("unrecoverable upload %s: %v", r.URL.Path, err)
		}
		objects[r.URL.Path] = body
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})}
	receipt, err := uploadEncryptedBackup(context.Background(), res, dest, key, "01m4qaqm4rp3t0ysghn6n7hjws", client)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.State != "uploaded" || !receipt.ReadBackVerified || len(receipt.Files) != 3 || receipt.Files[2].Name != res.Stamp+"-analytics.db" || len(objects) != 3 {
		t.Fatalf("receipt: %+v objects=%d", receipt, len(objects))
	}
	if err := backup.StoreUploadReceiptWithKey(res, receipt, key); err != nil {
		t.Fatal(err)
	}
	list, err := backup.ListWithKey(dir, key)
	if err != nil || len(list) != 1 || list[0].Remote.State != "uploaded" || !list[0].Remote.ReadBackVerified {
		t.Fatalf("list: %+v %v", list, err)
	}
}
