package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/db"
)

func backupSource(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "gorun.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec(`CREATE TABLE credentials (encrypted TEXT)`); err != nil {
		t.Fatal(err)
	}
	d.Close()
	return dir, path
}

func TestBackupRestoresEncryptedCredentials(t *testing.T) {
	dir, path := backupSource(t)
	cfg, err := config.Load([]string{"--data", dir})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "gorun.toml"), []byte("theme = \"example\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	enc, err := db.Encrypt(cfg.SecretKey, []byte("fixture-credential"), "billing")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := db.Open(path)
	if _, err = d.Exec(`INSERT INTO credentials VALUES (?)`, enc); err != nil {
		t.Fatal(err)
	}
	d.Close()
	res, err := CreateWith(dir, path, time.Now(), Options{IncludeSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	restore := t.TempDir()
	b, err := os.ReadFile(res.DB)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(restore, "gorun.db"), b, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(res.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(restore, h.Name), b, os.FileMode(h.Mode)); err != nil {
			t.Fatal(err)
		}
	}
	restored, err := config.Load([]string{"--data", restore})
	if err != nil {
		t.Fatal(err)
	}
	if restored.Theme != "example" {
		t.Error("local configuration was not restored")
	}
	rd, err := db.Open(restored.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	var saved string
	if err = rd.QueryRowContext(context.Background(), `SELECT encrypted FROM credentials`).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	plain, err := db.Decrypt(restored.SecretKey, saved, "billing")
	if err != nil {
		t.Fatal("restored instance cannot decrypt its stored credential")
	}
	if string(plain) != "fixture-credential" {
		t.Error("restored credential differs")
	}
	for _, p := range []string{res.DB, res.Files, filepath.Join(restore, "secret.key"), filepath.Join(restore, "gorun.toml")} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm()&0077 != 0 {
			t.Errorf("backup recovery file is readable outside owner: %s", filepath.Base(p))
		}
	}
}

func TestConcurrentBackupNeverDeletesSuccessfulSnapshot(t *testing.T) {
	dir, path := backupSource(t)
	now := time.Now()
	var wg sync.WaitGroup
	results := make(chan Result, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := Create(dir, path, now)
			if err == nil {
				results <- res
			}
		}()
	}
	wg.Wait()
	close(results)
	count := 0
	for res := range results {
		count++
		for _, p := range []string{res.DB, res.Files} {
			if _, err := os.Stat(p); err != nil {
				t.Error("successful snapshot disappeared", err)
			}
		}
	}
	if count != 1 {
		t.Errorf("successful backups=%d, want one", count)
	}
}

func TestFailedArchiveLeavesNoSnapshot(t *testing.T) {
	dir, path := backupSource(t)
	if err := os.WriteFile(filepath.Join(dir, "media"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", filepath.Join(dir, "secret.key")); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateWith(dir, path, time.Now(), Options{IncludeSecrets: true}); err == nil {
		t.Fatal("unrecoverable key was silently omitted")
	}
	entries, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("failed archive left a database snapshot")
	}
}

func TestBackupLeavesSecretsOutByDefault(t *testing.T) {
	dir, path := backupSource(t)
	if _, err := config.Load([]string{"--data", dir}); err != nil { // writes secret.key
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gorun.toml"), []byte("[stripe]\nsecret_key = \"sk_fixture\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	res, err := Create(dir, path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(res.Files)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Name == "secret.key" || h.Name == "gorun.toml" {
			t.Fatalf("%s archived without IncludeSecrets", h.Name)
		}
	}
	m, err := ReadManifest(res.Manifest)
	if err != nil || len(m.RecoveryFiles) != 0 {
		t.Fatalf("manifest lists recovery files: %v %v", m, err)
	}
	report, err := Inspect(res.Manifest)
	if err != nil || report.RecoveryKeyArchived || !slices.Contains(report.Warnings, "independent_original_secret_required") {
		t.Fatalf("inspection must ask for the independently kept secret: %+v %v", report, err)
	}
}
