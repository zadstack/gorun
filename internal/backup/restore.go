package backup

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Restore writes only into a new directory. It never merges into an existing
// installation; the caller must review external requirements before booting it.
func Restore(manifestPath, destination string) (report *Inspection, resultErr error) {
	destination = filepath.Clean(destination)
	if destination == "." || destination == string(filepath.Separator) {
		return nil, errors.New("backup: restore requires a new destination directory")
	}
	report, err := Inspect(manifestPath)
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
	if err := parent.Mkdir(base, 0700); err != nil {
		return report, fmt.Errorf("backup: destination must not exist: %w", err)
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, parent.RemoveAll(base))
		}
	}()
	root, err := parent.OpenRoot(base)
	if err != nil {
		return report, err
	}
	defer root.Close()
	dir := filepath.Dir(manifestPath)
	if err := copyRestoredDatabase(root, filepath.Join(dir, m.Database.Name)); err != nil {
		return report, err
	}
	if actual, err := digest(filepath.Join(destination, "gorun.db")); err != nil || actual.Size != m.Database.Size || actual.SHA256 != m.Database.SHA256 {
		return report, errors.New("backup: restored database checksum mismatch")
	}
	_, _, err = scanArchive(filepath.Join(dir, m.Archive.Name), func(name string, h *tar.Header, reader io.Reader) error {
		if h.Typeflag == tar.TypeDir {
			return root.MkdirAll(name, 0700)
		}
		if err := root.MkdirAll(filepath.Dir(name), 0700); err != nil {
			return err
		}
		mode := os.FileMode(h.Mode) & 0777
		if name == "secret.key" || name == "gorun.toml" {
			mode = 0600
		}
		f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
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
	return report, nil
}

func copyRestoredDatabase(root *os.Root, source string) error {
	f, err := os.Open(source)
	if err != nil {
		return err
	}
	defer f.Close()
	out, err := root.OpenFile("gorun.db", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := io.Copy(out, f)
	syncErr := out.Sync()
	return errors.Join(writeErr, syncErr, out.Close())
}
