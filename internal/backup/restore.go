package backup

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrPartialRestore reports that an interrupted restore left its staging
// directory (see PartialPath); remove it before restoring again.
var ErrPartialRestore = errors.New("backup: an interrupted restore left a partial directory")

// PartialPath is the staging directory Restore fills before it renames it to
// destination: a hidden sibling, so an interrupted restore never looks complete.
func PartialPath(destination string) string {
	destination = filepath.Clean(destination)
	return filepath.Join(filepath.Dir(destination), "."+filepath.Base(destination)+".partial")
}

// RemovePartial deletes the leftover staging directory for destination
// (see PartialPath). It only removes that exact sibling name under the
// destination's parent via os.OpenRoot — never a path outside restores/.
func RemovePartial(destination string) error {
	destination = filepath.Clean(destination)
	if destination == "." || destination == string(filepath.Separator) {
		return errors.New("backup: remove partial requires a destination path")
	}
	partial := PartialPath(destination)
	parent, err := os.OpenRoot(filepath.Dir(partial))
	if err != nil {
		return err
	}
	defer parent.Close()
	name := filepath.Base(partial)
	if _, err := parent.Lstat(name); err != nil {
		return err
	}
	return parent.RemoveAll(name)
}

// Restore writes only into a new directory. It never merges into an existing
// installation; the caller must review external requirements before booting it.
// Everything is written into PartialPath(destination) first and renamed to
// destination only after the database and archive checksums verify, so
// destination exists only for a complete restore.
//
// A backup with the optional analytics member restores it as
// <destination>/analytics.db, verified like gorun.db. The staging directory
// is new and empty, so no analytics.db-wal or -shm can sit beside it. A backup
// without the member restores no analytics.db: Restore never touches the live
// data directory, so an existing analytics.db stays where it is (copy it into
// the new directory before cutover to keep it). Restore never runs against
// the live tree, so a running analytics store needs no Reopen.
func Restore(manifestPath, destination string) (report *Inspection, resultErr error) {
	return RestoreWith(manifestPath, destination, nil)
}

// RestoreWith is Restore, verifying a keyed manifest HMAC when key is set.
func RestoreWith(manifestPath, destination string, key []byte) (report *Inspection, resultErr error) {
	destination = filepath.Clean(destination)
	if destination == "." || destination == string(filepath.Separator) {
		return nil, errors.New("backup: restore requires a new destination directory")
	}
	report, err := InspectWith(manifestPath, key)
	if err != nil {
		return nil, err
	}
	if report.Readiness == "blocked" {
		return report, errors.New("backup: preflight blocked restore")
	}
	m, err := ReadManifest(manifestPath)
	if err != nil {
		return report, err
	}
	parent, err := os.OpenRoot(filepath.Dir(destination))
	if err != nil {
		return report, err
	}
	defer parent.Close()
	base := filepath.Base(destination)
	staging := filepath.Base(PartialPath(destination))
	if _, err := parent.Lstat(base); err == nil || !errors.Is(err, os.ErrNotExist) {
		return report, fmt.Errorf("backup: destination must not exist: %s", destination)
	}
	if err := parent.Mkdir(staging, 0700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return report, fmt.Errorf("%w: %s", ErrPartialRestore, PartialPath(destination))
		}
		return report, err
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, parent.RemoveAll(staging))
		}
	}()
	root, err := parent.OpenRoot(staging)
	if err != nil {
		return report, err
	}
	defer root.Close()
	dir := filepath.Dir(manifestPath)
	if err := copyRestoredDatabase(root, filepath.Join(dir, m.Database.Name)); err != nil {
		return report, err
	}
	if actual, err := digest(filepath.Join(PartialPath(destination), "gorun.db")); err != nil || actual.Size != m.Database.Size || actual.SHA256 != m.Database.SHA256 {
		return report, errors.New("backup: restored database checksum mismatch")
	}
	if m.Analytics != nil {
		if err := restoreAnalytics(root, PartialPath(destination), filepath.Join(dir, m.Analytics.Name), *m.Analytics); err != nil {
			return report, err
		}
	}
	_, _, err = scanArchive(filepath.Join(dir, m.Archive.Name), func(name string, h *tar.Header, reader io.Reader) error {
		// Permissions are clamped, never taken from the archive: the restored
		// tree holds site data and possibly secret.key.
		if h.Typeflag == tar.TypeDir {
			if err := root.MkdirAll(name, 0700); err != nil {
				return err
			}
			return root.Chmod(name, 0700)
		}
		if err := root.MkdirAll(filepath.Dir(name), 0700); err != nil {
			return err
		}
		f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, writeErr := io.Copy(f, reader)
		syncErr := f.Sync()
		return errors.Join(writeErr, syncErr, f.Close())
	})
	if err != nil {
		return report, err
	}
	if actual, err := digest(filepath.Join(dir, m.Archive.Name)); err != nil || actual.Size != m.Archive.Size || actual.SHA256 != m.Archive.SHA256 {
		return report, errors.New("backup: archive changed during restore")
	}
	if err := root.Close(); err != nil {
		return report, err
	}
	if _, err := parent.Lstat(base); err == nil || !errors.Is(err, os.ErrNotExist) {
		return report, fmt.Errorf("backup: destination appeared during restore: %s", destination)
	}
	if err := parent.Rename(staging, base); err != nil {
		return report, err
	}
	return report, nil
}

// restoreAnalytics copies the analytics member to staging/analytics.db and
// checks it against the manifest digest. Stale sidecars are refused rather
// than removed: staging is created empty, so one there means interference.
func restoreAnalytics(root *os.Root, staging, source string, want FileDigest) error {
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := root.Lstat(AnalyticsFile + suffix); !errors.Is(err, os.ErrNotExist) {
			return errors.New("backup: unexpected analytics sidecar in restore staging")
		}
	}
	if err := copyRestoredFile(root, source, AnalyticsFile); err != nil {
		return err
	}
	if actual, err := digest(filepath.Join(staging, AnalyticsFile)); err != nil || actual.Size != want.Size || actual.SHA256 != want.SHA256 {
		return errors.New("backup: restored analytics checksum mismatch")
	}
	return nil
}

func copyRestoredDatabase(root *os.Root, source string) error {
	return copyRestoredFile(root, source, "gorun.db")
}

func copyRestoredFile(root *os.Root, source, name string) error {
	f, err := os.Open(source)
	if err != nil {
		return err
	}
	defer f.Close()
	out, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := io.Copy(out, f)
	syncErr := out.Sync()
	return errors.Join(writeErr, syncErr, out.Close())
}
