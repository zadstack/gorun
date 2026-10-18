package backup

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRestoreNewDirectoryAndRejectExistingState(t *testing.T) {
	dir, path := backupSource(t)
	if err := os.Mkdir(filepath.Join(dir, "media"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "media", "fixture.txt"), []byte("media"), 0600); err != nil {
		t.Fatal(err)
	}
	res, err := Create(dir, path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "restored")
	if _, err := Restore(res.Manifest, dest); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "media", "fixture.txt")); err != nil || string(data) != "media" {
		t.Fatal("media not restored", err)
	}
	if _, err := Restore(res.Manifest, dest); err == nil {
		t.Fatal("existing destination accepted")
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(filepath.Join(dest, "gorun.db") + suffix); !os.IsNotExist(err) {
			t.Fatal("restore copied sidecar")
		}
	}
	if err := os.WriteFile(filepath.Join(dest, "gorun.db-wal"), []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(res.Manifest, dest); err == nil {
		t.Fatal("destination with WAL accepted")
	}
	if data, err := os.ReadFile(filepath.Join(dest, "gorun.db-wal")); err != nil || string(data) != "retain" {
		t.Fatal("existing state was modified")
	}
}

func TestRestoreRejectsUnsafeArchivesBeforeWriting(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "media/../../escape", "media/link", "gorun.db-wal", "gorun.db-shm", "media\\evil"} {
		t.Run(name, func(t *testing.T) {
			dir, path := backupSource(t)
			res, err := Create(dir, path, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			f, err := os.Create(res.Files)
			if err != nil {
				t.Fatal(err)
			}
			gz := gzip.NewWriter(f)
			tw := tar.NewWriter(gz)
			h := &tar.Header{Name: name, Mode: 0600, Typeflag: tar.TypeReg}
			if name == "media/link" {
				h.Typeflag, h.Linkname = tar.TypeSymlink, "../../escape"
			}
			if err := tw.WriteHeader(h); err != nil {
				t.Fatal(err)
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(res.Manifest); err != nil {
				t.Fatal(err)
			}
			if err := writeManifest(res, nil, time.Now()); err != nil {
				t.Fatal(err)
			}
			dest := filepath.Join(t.TempDir(), "restored")
			if _, err := Restore(res.Manifest, dest); err == nil {
				t.Fatal("unsafe archive restored")
			}
			if _, err := os.Stat(dest); !os.IsNotExist(err) {
				t.Fatal("unsafe restore wrote destination")
			}
			if _, err := os.Stat(PartialPath(dest)); !os.IsNotExist(err) {
				t.Fatal("failed restore left its staging directory")
			}
		})
	}
}

func TestRestoreStagesThenRenamesAndRefusesLeftoverPartial(t *testing.T) {
	dir, path := backupSource(t)
	res, err := Create(dir, path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	dest := filepath.Join(parent, "restored")
	if got := PartialPath(dest); got != filepath.Join(parent, ".restored.partial") {
		t.Fatal(got)
	}
	// An interrupted restore (process killed mid-way) leaves only the staging
	// directory, never a destination that looks complete.
	if err := os.Mkdir(PartialPath(dest), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(PartialPath(dest), "gorun.db"), []byte("half"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(res.Manifest, dest); !errors.Is(err, ErrPartialRestore) {
		t.Fatalf("leftover partial: %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("destination created next to a leftover partial")
	}
	if data, err := os.ReadFile(filepath.Join(PartialPath(dest), "gorun.db")); err != nil || string(data) != "half" {
		t.Fatal("leftover partial was modified")
	}
	if err := os.RemoveAll(PartialPath(dest)); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(res.Manifest, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "gorun.db")); err != nil {
		t.Fatal("restored database missing", err)
	}
	if _, err := os.Stat(PartialPath(dest)); !os.IsNotExist(err) {
		t.Fatal("staging directory left after a complete restore")
	}
}

func TestRestoreClampsArchivePermissions(t *testing.T) {
	dir, path := backupSource(t)
	res, err := Create(dir, path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(res.Files)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, h := range []*tar.Header{
		{Name: "media/", Mode: 0777, Typeflag: tar.TypeDir},
		{Name: "media/open", Mode: 0777, Typeflag: tar.TypeDir},
		{Name: "media/open/world.txt", Mode: 0666, Typeflag: tar.TypeReg, Size: 5},
		{Name: "media/run.sh", Mode: 0755, Typeflag: tar.TypeReg, Size: 5},
	} {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte("hello")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := errors.Join(tw.Close(), gz.Close(), f.Close()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(res.Manifest); err != nil {
		t.Fatal(err)
	}
	if err := writeManifest(res, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "restored")
	if _, err := Restore(res.Manifest, dest); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]os.FileMode{
		".": 0700, "gorun.db": 0600, "media": 0700, "media/open": 0700,
		"media/open/world.txt": 0600, "media/run.sh": 0600,
	} {
		st, err := os.Stat(filepath.Join(dest, name))
		if err != nil {
			t.Fatal(err)
		}
		if got := st.Mode().Perm(); got != want {
			t.Errorf("%s: mode %o, want %o", name, got, want)
		}
	}
}
