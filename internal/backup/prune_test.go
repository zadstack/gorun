package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneKeepsNewestCompletePairs(t *testing.T) {
	dir, path := backupSource(t)
	times := []time.Time{
		time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC),
	}
	var stamps []string
	for _, now := range times {
		res, err := Create(dir, path, now)
		if err != nil {
			t.Fatal(err)
		}
		stamps = append(stamps, res.Stamp)
		if err := StoreUploadReceipt(res, UploadReceipt{State: "failed"}); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := Prune(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if removed < 3 { // at least .db + .tar.gz + manifest for the oldest
		t.Fatalf("removed=%d, want at least 3 files", removed)
	}
	list, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list len=%d want 2: %+v", len(list), list)
	}
	if list[0].Stamp != stamps[2] || list[1].Stamp != stamps[1] {
		t.Fatalf("kept %+v want newest two %s %s", list, stamps[2], stamps[1])
	}
	for _, name := range []string{
		stamps[0] + ".db", stamps[0] + ".tar.gz",
		stamps[0] + ".manifest.json", stamps[0] + ".remote.json",
	} {
		if _, err := os.Stat(filepath.Join(Dir(dir), name)); !os.IsNotExist(err) {
			t.Fatalf("%s still present: %v", name, err)
		}
	}
}

func TestPruneLeavesIncompleteAloneAndNoopWhenKeepLow(t *testing.T) {
	dir, path := backupSource(t)
	res, err := Create(dir, path, time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(Dir(dir), "20261001T000000Z.db")
	if err := os.WriteFile(orphan, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	removed, err := Prune(dir, 0)
	if err != nil || removed != 0 {
		t.Fatalf("keep 0: removed=%d err=%v", removed, err)
	}
	removed, err = Prune(dir, 1)
	if err != nil || removed != 0 {
		t.Fatalf("one complete: removed=%d err=%v", removed, err)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatal("incomplete stamp was pruned")
	}
	if _, err := os.Stat(res.DB); err != nil {
		t.Fatal("complete pair pruned")
	}
}

func TestPruneInvalidStampRejected(t *testing.T) {
	if n, err := removeStamp(t.TempDir(), "../evil"); n != 0 || err == nil {
		t.Fatalf("want error, got n=%d err=%v", n, err)
	}
}
