package backup

import (
	"fmt"
	"os"
	"path/filepath"
)

// Prune deletes the oldest complete local backup pairs under dataDir so that
// at most keep complete pairs remain (newest first). A complete pair has both
// .db and .tar.gz. Incomplete stamps are left alone. keep < 1 is a no-op.
// Each pruned stamp removes .db, .tar.gz, .manifest.json and .remote.json when
// present. Create is unchanged: callers opt into Prune after Create.
func Prune(dataDir string, keep int) (removed int, err error) {
	if keep < 1 {
		return 0, nil
	}
	list, err := List(dataDir)
	if err != nil {
		return 0, err
	}
	var complete []Entry
	for _, e := range list {
		if e.DBSize >= 0 && e.FilesSize >= 0 {
			complete = append(complete, e)
		}
	}
	if len(complete) <= keep {
		return 0, nil
	}
	dir := Dir(dataDir)
	for _, e := range complete[keep:] {
		n, remErr := removeStamp(dir, e.Stamp)
		removed += n
		if remErr != nil && err == nil {
			err = remErr
		}
	}
	return removed, err
}

func removeStamp(dir, stamp string) (int, error) {
	if !ValidStamp(stamp) {
		return 0, fmt.Errorf("backup: invalid prune stamp %q", stamp)
	}
	n := 0
	var first error
	for _, name := range []string{
		stamp + ".db",
		stamp + ".tar.gz",
		stamp + ".manifest.json",
		stamp + ".remote.json",
	} {
		p := filepath.Join(dir, name)
		if err := os.Remove(p); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			if first == nil {
				first = fmt.Errorf("backup: prune %s: %w", name, err)
			}
			continue
		}
		n++
	}
	return n, first
}
