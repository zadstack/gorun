// Package backup makes and lists data-directory backups: a consistent copy of the database
// (VACUUM INTO) and a tar.gz of data trees and local recovery configuration, both under
// <data>/backups/<UTC timestamp>. `gorun backup` and the admin's System → Backups share it.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/db" // also registers the sqlite driver
)

// Dirs are the data-directory trees archived next to the database copy.
var Dirs = []string{"media", "overrides", "extensions", "form-uploads"}

// recoveryFiles hold the site secret (secret.key) and configuration that
// may carry secrets (gorun.toml: secret_key, Stripe, S3, setup token). They
// are archived only with Options.IncludeSecrets: an archive is otherwise a
// file anyone who downloads or copies it could use to decrypt the database's
// protected fields and forge sessions.
var recoveryFiles = []string{"secret.key", "gorun.toml"}

// Options adjust Create.
type Options struct {
	// IncludeSecrets archives secret.key and gorun.toml in plain text
	// (`gorun backup --include-secrets`). Keep such archives offline and
	// encrypted; the admin never sets it.
	IncludeSecrets bool
	// SecretKey (32 bytes) HMACs the manifest so a writable backups
	// directory alone cannot forge digests. Prefer the site secret.
	SecretKey []byte
	// SkipAnalytics leaves <dataDir>/analytics.db out of the backup.
	// Callers set it from the setting named by SettingSkipAnalytics.
	SkipAnalytics bool
	// SkipAnalyticsReason, when set with SkipAnalytics, marks the skip as
	// involuntary (for example the setting could not be read): it is copied
	// to Result.AnalyticsSkipped so callers warn about it. A deliberate
	// exclusion leaves it empty.
	SkipAnalyticsReason string
}

// Codes that start Result.AnalyticsSkipped ("<code>: <detail>").
const (
	AnalyticsNotRegular      = "analytics_not_regular"
	AnalyticsCopyFailed      = "analytics_copy_failed"
	AnalyticsIntegrityFailed = "analytics_integrity_failed"
	AnalyticsSettingUnread   = "analytics_setting_unreadable"
)

// AnalyticsFile is the analytics database in the data directory. Backup
// knows it only by this name (it never imports the analytics module): when
// present, it is copied to <stamp>-analytics.db as an optional member.
const AnalyticsFile = "analytics.db"

// SettingSkipAnalytics is the JSON-bool setting ("Leave analytics out of
// backups") that callers map to Options.SkipAnalytics. It must match
// analytics.SettingBackupExclude.
const SettingSkipAnalytics = "analytics.backup_exclude"

// analyticsSuffix names the analytics copy: <stamp>-analytics.db.
const analyticsSuffix = "-analytics.db"

// stampLayout names a backup by its UTC creation time.
const stampLayout = "20060102T150405Z"

// fileName is a backup file: <stamp>.db, <stamp>.tar.gz or the optional
// <stamp>-analytics.db (group 2 is "db", "tar.gz" or "analytics").
var fileName = regexp.MustCompile(`^(\d{8}T\d{6}Z)(?:\.(db|tar\.gz)|-(analytics)\.db)$`)

// fileKind is the kind of a fileName match: "db", "tar.gz" or "analytics".
func fileKind(m []string) string {
	if m[2] != "" {
		return m[2]
	}
	return m[3]
}

// Result names the files one backup wrote.
type Result struct {
	Stamp, DB, Files string
	Manifest         string
	// Analytics is the <stamp>-analytics.db copy, or "" when the backup
	// has no analytics member (skipped, or no analytics.db to copy).
	Analytics string
	// AnalyticsSkipped is "<code>: <detail>" when analytics.db existed (or
	// may have) but was left out against the operator's choice: the copy
	// failed, the copy failed its integrity check, or the exclusion setting
	// could not be read. The core backup still succeeds without the member;
	// callers must surface this as a warning. Empty otherwise.
	AnalyticsSkipped string
	Count            int // archived files
}

// Entry is one backup in the backups directory (either file may be missing).
type Entry struct {
	Stamp     string
	Time      time.Time
	DBSize    int64 // -1 when the .db is missing
	FilesSize int64 // -1 when the .tar.gz is missing
	// AnalyticsSize is the size of <stamp>-analytics.db, -1 when absent.
	AnalyticsSize int64
	LocalState    string
	HasManifest   bool
	Remote        UploadReceipt
}

// Dir is the backups directory of dataDir.
func Dir(dataDir string) string { return filepath.Join(dataDir, "backups") }

// Create backs up the database at dbPath and the data trees of dataDir,
// named after now, without the recovery secrets (see Options).
func Create(dataDir, dbPath string, now time.Time) (Result, error) {
	return CreateWith(dataDir, dbPath, now, Options{})
}

// CreateWith is Create with options.
func CreateWith(dataDir, dbPath string, now time.Time, opts Options) (Result, error) {
	// A mistyped --data / GORUN_DATA must not "back up" a fresh empty
	// database: the source must already exist.
	if st, err := os.Stat(dbPath); err != nil || !st.Mode().IsRegular() {
		return Result{}, fmt.Errorf("backup: no database at %s (check --data / GORUN_DATA)", dbPath)
	}
	dir := Dir(dataDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Result{}, fmt.Errorf("backup: %w", err)
	}
	stamp := now.UTC().Format(stampLayout)
	lockPath := filepath.Join(dir, stamp+".lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Result{}, fmt.Errorf("backup: reserve timestamp: %w", err)
	}
	if err := lock.Close(); err != nil {
		os.Remove(lockPath)
		return Result{}, fmt.Errorf("backup: reserve timestamp: %w", err)
	}
	defer os.Remove(lockPath)
	res := Result{Stamp: stamp, DB: filepath.Join(dir, stamp+".db"), Files: filepath.Join(dir, stamp+".tar.gz")}
	res.Manifest = filepath.Join(dir, stamp+".manifest.json")
	for _, p := range []string{res.DB, res.Files, res.Manifest, filepath.Join(dir, stamp+".remote.json"), filepath.Join(dir, stamp+analyticsSuffix)} {
		if _, err := os.Stat(p); err == nil {
			return Result{}, fmt.Errorf("backup: %s already exists", p)
		}
	}
	dst, err := os.OpenFile(res.DB, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Result{}, fmt.Errorf("backup: create database destination: %w", err)
	}
	if err := dst.Close(); err != nil {
		os.Remove(res.DB)
		return Result{}, fmt.Errorf("backup: close database destination: %w", err)
	}
	if err := vacuumInto(dbPath, res.DB); err != nil {
		os.Remove(res.DB)
		return Result{}, fmt.Errorf("backup: database: %w", err)
	}
	// Analytics is optional: a failed or damaged copy never fails the core
	// backup. It is left out (no manifest member) and reported instead.
	if opts.SkipAnalytics {
		res.AnalyticsSkipped = opts.SkipAnalyticsReason
	} else {
		copied, skipped := copyAnalytics(dataDir, filepath.Join(dir, stamp+analyticsSuffix))
		res.Analytics, res.AnalyticsSkipped = copied, skipped
	}
	// removeAll undoes a failed backup: never leave half a backup behind.
	removeAll := func() {
		os.Remove(res.Files)
		os.Remove(res.DB)
		if res.Analytics != "" {
			os.Remove(res.Analytics)
		}
	}
	paths := append([]string{}, Dirs...)
	recovery := []string{}
	for _, name := range recoveryFiles {
		if !opts.IncludeSecrets {
			break
		}
		st, err := os.Lstat(filepath.Join(dataDir, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil || !st.Mode().IsRegular() {
			removeAll()
			return Result{}, fmt.Errorf("backup: recovery file %s must be readable and regular", name)
		}
		paths = append(paths, name)
		recovery = append(recovery, name)
	}
	n, err := writeTar(res.Files, dataDir, paths)
	if err != nil {
		removeAll()
		return Result{}, fmt.Errorf("backup: files: %w", err)
	}
	res.Count = n
	if err := writeManifest(res, recovery, now, opts.SecretKey); err != nil {
		removeAll()
		return Result{}, fmt.Errorf("backup: manifest: %w", err)
	}
	return res, nil
}

// List returns the backups of dataDir, newest first.
func List(dataDir string) ([]Entry, error) { return ListWithKey(dataDir, nil) }

// ListWithKey verifies historical readback receipts with the site secret.
// Presence-only local metadata remains distinct from an integrity inspection.
func ListWithKey(dataDir string, key []byte) ([]Entry, error) {
	files, err := os.ReadDir(Dir(dataDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("backup: list: %w", err)
	}
	by := map[string]*Entry{}
	for _, f := range files {
		m := fileName.FindStringSubmatch(f.Name())
		if match := manifestName.FindStringSubmatch(f.Name()); m == nil && match != nil {
			m = []string{match[0], match[1], "manifest", ""}
		}
		if m == nil || !f.Type().IsRegular() {
			continue
		}
		e := by[m[1]]
		if e == nil {
			t, _ := time.Parse(stampLayout, m[1])
			e = &Entry{Stamp: m[1], Time: t, DBSize: -1, FilesSize: -1, AnalyticsSize: -1}
			by[m[1]] = e
		}
		info, err := f.Info()
		if err != nil {
			continue
		}
		switch fileKind(m) {
		case "db":
			e.DBSize = info.Size()
		case "tar.gz":
			e.FilesSize = info.Size()
		case "analytics":
			e.AnalyticsSize = info.Size()
		}
	}
	out := make([]Entry, 0, len(by))
	for _, e := range by {
		e.LocalState = "partial"
		if e.DBSize >= 0 && e.FilesSize >= 0 {
			e.LocalState = "legacy_unverified"
		}
		if m, err := ReadManifest(filepath.Join(Dir(dataDir), e.Stamp+".manifest.json")); err == nil {
			e.HasManifest = true
			analyticsOK := m.Analytics == nil || m.Analytics.Size == e.AnalyticsSize
			if m.Database.Size == e.DBSize && m.Archive.Size == e.FilesSize && analyticsOK {
				e.LocalState = "paired_not_inspected"
			}
		}

		e.Remote = receiptAtWithKey(Dir(dataDir), e.Stamp, key)
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Stamp > out[j].Stamp })
	return out, nil
}

// File returns the path of the backup file name (<stamp>.db, <stamp>.tar.gz,
// <stamp>-analytics.db or <stamp>.manifest.json); any other
// name, or a missing file, is fs.ErrNotExist.
func File(dataDir, name string) (string, error) {
	if (!fileName.MatchString(name) && !manifestName.MatchString(name)) || strings.ContainsAny(name, `/\`) {
		return "", fs.ErrNotExist
	}
	p := filepath.Join(Dir(dataDir), name)
	if st, err := os.Lstat(p); err != nil || !st.Mode().IsRegular() {
		return "", fs.ErrNotExist
	}
	return p, nil
}

// vacuumInto copies the database at src to dst with VACUUM INTO, reading
// src through a read-only connection (it is never created or migrated).
func vacuumInto(src, dst string) error {
	uri, err := db.FileURI(src)
	if err != nil {
		return err
	}
	d, err := sql.Open("sqlite", uri+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer d.Close()
	_, err = d.ExecContext(context.Background(), `VACUUM INTO ?`, dst)
	return err
}

// copyAnalytics snapshots <dataDir>/analytics.db into dst with VACUUM INTO
// on a separate read-only connection: the analytics module keeps its own
// handle, and under WAL the copy is a consistent read transaction that
// includes committed WAL frames. The copy must pass PRAGMA integrity_check
// before it becomes a manifest member.
//
// It returns the copy's path, or "" with no reason when there is no
// analytics.db (nothing about analytics is then recorded), or "" with a
// skip reason ("<code>: <detail>") when the copy could not be made or is
// damaged; the partial copy is then removed.
func copyAnalytics(dataDir, dst string) (path, skipped string) {
	src := filepath.Join(dataDir, AnalyticsFile)
	st, err := os.Lstat(src)
	if errors.Is(err, fs.ErrNotExist) {
		return "", ""
	}
	if err != nil {
		return "", AnalyticsCopyFailed + ": " + err.Error()
	}
	if !st.Mode().IsRegular() {
		return "", AnalyticsNotRegular + ": " + AnalyticsFile + " is not a regular file"
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", AnalyticsCopyFailed + ": " + err.Error()
	}
	if err := f.Close(); err != nil {
		os.Remove(dst)
		return "", AnalyticsCopyFailed + ": " + err.Error()
	}
	if err := vacuumInto(src, dst); err != nil {
		os.Remove(dst)
		return "", AnalyticsCopyFailed + ": " + err.Error()
	}
	if err := checkAnalyticsCopy(dst); err != nil {
		os.Remove(dst)
		return "", AnalyticsIntegrityFailed + ": " + err.Error()
	}
	return dst, ""
}

// checkAnalyticsCopy runs PRAGMA integrity_check on a fresh analytics copy.
// It is a variable so tests can simulate a copy that fails the check.
var checkAnalyticsCopy = func(path string) error {
	d, err := openReadOnlyDB(path)
	if err != nil {
		return err
	}
	defer d.Close()
	rows, err := d.Query(`PRAGMA integrity_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var answer string
		if err := rows.Scan(&answer); err != nil {
			return err
		}
		if answer != "ok" {
			return fmt.Errorf("integrity_check: %s", answer)
		}
	}
	return rows.Err()
}

// writeTar archives the regular files under each root-relative path (paths
// relative to root; missing dirs are skipped, symlinks are not followed).
func writeTar(path, root string, dirs []string) (int, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	n := 0
	for _, dir := range dirs {
		base := filepath.Join(root, dir)
		if _, err := os.Lstat(base); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		err := filepath.WalkDir(base, func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !e.Type().IsRegular() && !e.IsDir() {
				return nil // symlinks, devices, sockets
			}
			info, err := e.Info()
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			hdr, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			hdr.Name = filepath.ToSlash(rel)
			if rel == "secret.key" || rel == "gorun.toml" {
				hdr.Mode = 0o600
			}
			if e.IsDir() {
				hdr.Name += "/"
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if e.IsDir() {
				return nil
			}
			src, err := os.Open(p)
			if err != nil {
				return err
			}
			defer src.Close()
			if _, err := io.Copy(tw, src); err != nil {
				return err
			}
			n++
			return nil
		})
		if err != nil {
			return n, err
		}
	}
	if err := tw.Close(); err != nil {
		return n, err
	}
	if err := gz.Close(); err != nil {
		return n, err
	}
	return n, f.Sync()
}
