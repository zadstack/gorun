package backup

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

// analyticsSource writes <dir>/analytics.db with rows rows and returns the
// still-open writer, so part of the data sits in the WAL (the analytics
// module holds its own handle while a backup runs).
func analyticsSource(t *testing.T, dir string, rows int) *sql.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(dir, AnalyticsFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := d.Exec(`PRAGMA wal_autocheckpoint=0`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`CREATE TABLE events (id INTEGER PRIMARY KEY, path TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for i := range rows {
		if _, err := d.Exec(`INSERT INTO events (path) VALUES (?)`, "/p"+strings.Repeat("x", i%7)); err != nil {
			t.Fatal(err)
		}
	}
	if st, err := os.Stat(filepath.Join(dir, AnalyticsFile+"-wal")); err != nil || st.Size() == 0 {
		t.Fatalf("fixture expects uncheckpointed WAL data: %v", err)
	}
	return d
}

func countEvents(t *testing.T, path string) int {
	t.Helper()
	d, err := openReadOnlyDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var ok string
	if err := d.QueryRow(`PRAGMA integrity_check`).Scan(&ok); err != nil || ok != "ok" {
		t.Fatalf("integrity of %s: %q %v", path, ok, err)
	}
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestBackupIncludesAnalyticsWhenPresent(t *testing.T) {
	dir, path := backupSource(t)
	analyticsSource(t, dir, 120)
	key := bytes.Repeat([]byte{5}, 32)
	res, err := CreateWith(dir, path, time.Now(), Options{SecretKey: key})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(Dir(dir), res.Stamp+"-analytics.db")
	if res.Analytics != want {
		t.Fatalf("Result.Analytics = %q, want %q", res.Analytics, want)
	}
	if n := countEvents(t, want); n != 120 {
		t.Fatalf("copy has %d events, want 120 (WAL rows must be in the snapshot)", n)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Lstat(want + suffix); !os.IsNotExist(err) {
			t.Fatalf("copy left a %s sidecar", suffix)
		}
	}
	m, err := ReadManifest(res.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := digest(want)
	if err != nil {
		t.Fatal(err)
	}
	if m.Analytics == nil || *m.Analytics != actual {
		t.Fatalf("manifest analytics member = %+v, want %+v", m.Analytics, actual)
	}
	if ok, err := CheckManifestHMAC(m, key); err != nil || !ok {
		t.Fatalf("hmac: %v %v", ok, err)
	}
	// The HMAC covers the analytics member: altering or dropping it fails.
	tampered := *m
	other := *m.Analytics
	other.SHA256 = strings.Repeat("0", 64)
	tampered.Analytics = &other
	if ok, _ := CheckManifestHMAC(&tampered, key); ok {
		t.Fatal("hmac ignores the analytics digest")
	}
	tampered.Analytics = nil
	if ok, _ := CheckManifestHMAC(&tampered, key); ok {
		t.Fatal("hmac accepts a stripped analytics member")
	}
	rep, err := InspectWith(res.Manifest, key)
	if err != nil || rep.Readiness == "blocked" || !rep.IntegrityOK || !rep.Analytics {
		t.Fatalf("inspect: %+v %v", rep, err)
	}
	list, err := List(dir)
	if err != nil || len(list) != 1 || list[0].AnalyticsSize != actual.Size || list[0].LocalState != "paired_not_inspected" {
		t.Fatalf("list: %+v %v", list, err)
	}
	if p, err := File(dir, res.Stamp+"-analytics.db"); err != nil || p != want {
		t.Fatalf("File: %q %v", p, err)
	}
}

func TestBackupAnalyticsCopyChangedIsBlocked(t *testing.T) {
	dir, path := backupSource(t)
	analyticsSource(t, dir, 3)
	res, err := Create(dir, path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(res.Analytics, []byte("not the snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err := Inspect(res.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Readiness != "blocked" || !slices.Contains(rep.Errors, "pair_checksum_mismatch") {
		t.Fatalf("changed analytics copy: %+v", rep)
	}
	if _, err := Restore(res.Manifest, filepath.Join(t.TempDir(), "r")); err == nil {
		t.Fatal("restore accepted a changed analytics copy")
	}
}

func TestBackupSkipsAnalytics(t *testing.T) {
	dir, path := backupSource(t)
	analyticsSource(t, dir, 3)
	res, err := CreateWith(dir, path, time.Now(), Options{SkipAnalytics: true})
	if err != nil {
		t.Fatal(err)
	}
	assertNoAnalytics(t, dir, res)
}

func TestBackupWithoutAnalyticsFile(t *testing.T) {
	dir, path := backupSource(t)
	res, err := Create(dir, path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	assertNoAnalytics(t, dir, res)
}

func assertNoAnalytics(t *testing.T, dir string, res Result) {
	t.Helper()
	if res.Analytics != "" {
		t.Fatalf("Result.Analytics = %q", res.Analytics)
	}
	if _, err := os.Lstat(filepath.Join(Dir(dir), res.Stamp+"-analytics.db")); !os.IsNotExist(err) {
		t.Fatal("analytics copy written")
	}
	raw, err := os.ReadFile(res.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["analytics"]; ok || bytes.Contains(raw, []byte("analytics")) {
		t.Fatalf("manifest mentions analytics: %s", raw)
	}
	rep, err := Inspect(res.Manifest)
	if err != nil || rep.Readiness == "blocked" || rep.Analytics {
		t.Fatalf("inspect: %+v %v", rep, err)
	}
	list, err := List(dir)
	if err != nil || len(list) != 1 || list[0].AnalyticsSize != -1 {
		t.Fatalf("list: %+v %v", list, err)
	}
}

// legacyHMAC is the manifest HMAC as computed before the analytics member.
func legacyHMAC(key []byte, m Manifest) string {
	mac := hmac.New(sha256.New, key)
	for i, part := range []string{manifestMACDomain, m.Stamp, m.Database.Name, m.Database.SHA256, m.Archive.Name, m.Archive.SHA256} {
		if i > 0 {
			mac.Write([]byte{0})
		}
		mac.Write([]byte(part))
	}
	return hex.EncodeToString(mac.Sum(nil))
}

func TestOldManifestStillValid(t *testing.T) {
	dir, path := backupSource(t)
	key := bytes.Repeat([]byte{8}, 32)
	res, err := CreateWith(dir, path, time.Now(), Options{SecretKey: key})
	if err != nil {
		t.Fatal(err)
	}
	m, err := ReadManifest(res.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	// A manifest written by an older release: same fields, no analytics key,
	// HMAC from the original formula.
	old := map[string]any{
		"format_version": 1, "stamp": m.Stamp, "created_at": m.CreatedAt,
		"database": m.Database, "archive": m.Archive, "consistency": m.Consistency,
		"recovery_files": m.RecoveryFiles, "limitations": m.Limitations,
		"encrypted_file_limit": m.EncryptedFileLimit, "hmac": legacyHMAC(key, *m),
	}
	raw, err := json.MarshalIndent(old, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(res.Manifest, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadManifest(res.Manifest)
	if err != nil || got.Analytics != nil {
		t.Fatalf("old manifest: %+v %v", got, err)
	}
	if ok, err := CheckManifestHMAC(got, key); err != nil || !ok {
		t.Fatalf("old hmac: %v %v", ok, err)
	}
	rep, err := InspectWith(res.Manifest, key)
	if err != nil || rep.Readiness == "blocked" {
		t.Fatalf("inspect old: %+v %v", rep, err)
	}
	dest := filepath.Join(t.TempDir(), "restored")
	if _, err := RestoreWith(res.Manifest, dest, key); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dest, AnalyticsFile)); !os.IsNotExist(err) {
		t.Fatal("restore of a backup without analytics created analytics.db")
	}
}

func TestManifestRejectsBadAnalyticsMember(t *testing.T) {
	dir, path := backupSource(t)
	analyticsSource(t, dir, 2)
	res, err := Create(dir, path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(res.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string][]byte{
		"other stamp": bytes.Replace(raw, []byte(res.Stamp+"-analytics.db"), []byte("20000101T000000Z-analytics.db"), 1),
		"traversal":   bytes.Replace(raw, []byte(res.Stamp+"-analytics.db"), []byte("../analytics.db"), 1),
		"main db":     bytes.Replace(raw, []byte(res.Stamp+"-analytics.db"), []byte(res.Stamp+".db"), 1),
	} {
		p := filepath.Join(t.TempDir(), res.Stamp+".manifest.json")
		if err := os.WriteFile(p, bad, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadManifest(p); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

func TestRestoreRoundTripsAnalytics(t *testing.T) {
	dir, path := backupSource(t)
	analyticsSource(t, dir, 42)
	key := bytes.Repeat([]byte{4}, 32)
	res, err := CreateWith(dir, path, time.Now(), Options{SecretKey: key})
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "restored")
	rep, err := RestoreWith(res.Manifest, dest, key)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Analytics {
		t.Fatal("report does not mention the analytics member")
	}
	restored := filepath.Join(dest, AnalyticsFile)
	m, _ := ReadManifest(res.Manifest)
	if actual, err := digest(restored); err != nil || actual.SHA256 != m.Analytics.SHA256 {
		t.Fatalf("restored analytics digest: %+v %v", actual, err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Lstat(restored + suffix); !os.IsNotExist(err) {
			t.Fatalf("restore left %s", suffix)
		}
	}
	if n := countEvents(t, restored); n != 42 {
		t.Fatalf("restored %d events, want 42", n)
	}
	// The restored file opens as a normal read-write analytics database.
	d, err := db.Open(restored)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO events (path) VALUES ('/after')`); err != nil {
		t.Fatal(err)
	}
	d.Close()
}

func TestPruneRemovesAnalyticsCopy(t *testing.T) {
	dir, path := backupSource(t)
	analyticsSource(t, dir, 2)
	var first Result
	for i, now := range []time.Time{time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)} {
		res, err := Create(dir, path, now)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = res
		}
	}
	if _, err := PruneVerified(dir, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(first.Analytics); !os.IsNotExist(err) {
		t.Fatal("pruned stamp kept its analytics copy")
	}
}

func TestUploadReceiptCoversAnalytics(t *testing.T) {
	dir, path := backupSource(t)
	analyticsSource(t, dir, 2)
	key := bytes.Repeat([]byte{6}, 32)
	res, err := CreateWith(dir, path, time.Now(), Options{SecretKey: key})
	if err != nil {
		t.Fatal(err)
	}
	file := func(name string) UploadFile {
		return UploadFile{Name: name, State: "uploaded", ObjectKey: "backups/x/" + name + ".gorb", CheckedAt: "fixture"}
	}
	two := []UploadFile{file(res.Stamp + ".db"), file(res.Stamp + ".tar.gz")}
	if err := StoreUploadReceiptWithKey(res, UploadReceipt{State: "uploaded", DestinationID: "d", ReadBackVerified: true, Files: two}, key); err == nil {
		t.Fatal("verified receipt without the analytics copy accepted")
	}
	three := append(two, file(res.Stamp+"-analytics.db"))
	if err := StoreUploadReceiptWithKey(res, UploadReceipt{State: "uploaded", DestinationID: "d", ReadBackVerified: true, Files: three}, key); err != nil {
		t.Fatal(err)
	}
	got := receiptAtWithKey(Dir(dir), res.Stamp, key)
	if got.State != "uploaded" || !got.ReadBackVerified || len(got.Files) != 3 {
		t.Fatalf("receipt: %+v", got)
	}
}
