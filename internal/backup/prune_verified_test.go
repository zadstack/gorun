package backup

import (
	"os"
	"testing"
	"time"
)

func TestVerifiedRetentionPreservesGoodCopiesWhenNewerPairIsCorrupt(t *testing.T) {
	dir, path := backupSource(t)
	var backups []Result
	for i := 1; i <= 3; i++ {
		res, err := Create(dir, path, time.Date(2026, 10, i, 0, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		backups = append(backups, res)
	}
	if err := os.WriteFile(backups[2].Files, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	removed, err := PruneVerified(dir, 2)
	if err != nil || removed != 0 {
		t.Fatalf("pruned a good copy based on corrupt replacement: %d %v", removed, err)
	}
	for _, res := range backups {
		if _, err := os.Stat(res.DB); err != nil {
			t.Fatal("a copy was removed", err)
		}
	}
	removed, err = PruneVerified(dir, 1)
	if err != nil || removed < 3 {
		t.Fatalf("verified retention: %d %v", removed, err)
	}
	if _, err := os.Stat(backups[1].DB); err != nil {
		t.Fatal("newest good backup removed")
	}
	if _, err := os.Stat(backups[2].Files); err != nil {
		t.Fatal("corrupt backup should be preserved for investigation")
	}
}
