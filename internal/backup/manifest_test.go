package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCreateWritesPairedManifest(t *testing.T) {
	dir, path := backupSource(t)
	res, err := Create(dir, path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(Dir(dir), res.Stamp+".manifest.json"))
	if err != nil {
		t.Fatal("paired manifest is missing", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["format_version"] != float64(1) || m["stamp"] != res.Stamp || m["consistency"] != "database_snapshot_files_not_atomic" {
		t.Fatal(m)
	}
	for _, field := range []string{"database", "archive"} {
		f := m[field].(map[string]any)
		if len(f["sha256"].(string)) != 64 || f["name"] == "" || f["size"] == nil {
			t.Fatalf("missing file binding: %v", f)
		}
	}
}

func TestListMetadataDoesNotClaimIntegrityAndKeepsLegacyPairs(t *testing.T) {
	dir, path := backupSource(t)
	res, err := Create(dir, path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	legacy := "20200101T000000Z"
	for _, suffix := range []string{".db", ".tar.gz"} {
		if err := os.WriteFile(filepath.Join(Dir(dir), legacy+suffix), []byte("legacy"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	list, err := List(dir)
	if err != nil || len(list) != 2 || list[0].LocalState != "paired_not_inspected" || list[1].LocalState != "legacy_unverified" {
		t.Fatalf("legacy/presence states: %+v %v", list, err)
	}
	if err := StoreUploadReceipt(res, UploadReceipt{State: "partial", Files: []UploadFile{{Name: res.Stamp + ".db", State: "uploaded", CheckedAt: "fixture"}, {Name: res.Stamp + ".tar.gz", State: "failed", Code: "provider_upload_failed", CheckedAt: "fixture"}}}); err != nil {
		t.Fatal(err)
	}
	list, err = List(dir)
	if err != nil || list[0].Remote.State != "partial" || list[0].Remote.ReadBackVerified {
		t.Fatal("partial remote pair called complete")
	}
	if err := os.Remove(res.DB); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(res.Files); err != nil {
		t.Fatal(err)
	}
	list, err = List(dir)
	if err != nil || len(list) != 2 || list[0].LocalState != "partial" || !list[0].HasManifest {
		t.Fatal("orphan manifest hidden")
	}
}
