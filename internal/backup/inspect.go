package backup

import (
	"archive/tar"
	"compress/gzip"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

type ExternalRequirement struct {
	Kind      string `json:"kind"`
	Reference string `json:"reference"`
	State     string `json:"state"`
}

type Inspection struct {
	Stamp                string                `json:"stamp"`
	CheckedAt            string                `json:"checked_at"`
	LocalComplete        bool                  `json:"local_complete"`
	IntegrityOK          bool                  `json:"integrity_ok"`
	ForeignKeyViolations int                   `json:"foreign_key_violations"`
	ArchiveFiles         int                   `json:"archive_files"`
	RecoveryKeyArchived  bool                  `json:"recovery_key_archived"`
	Consistency          string                `json:"consistency"`
	Readiness            string                `json:"readiness"`
	Errors               []string              `json:"errors"`
	Warnings             []string              `json:"warnings"`
	CutoverChecklist     []string              `json:"cutover_checklist"`
	ExternalRequirements []ExternalRequirement `json:"external_requirements"`
	Remote               UploadReceipt         `json:"remote"`
}

// Inspect never creates a DB, secret or receipt, and makes no network requests.
// Readiness tiers:
//   - blocked: local pair cannot be restored (errors)
//   - review_required: local pair usable, but site-specific warnings need attention before cutover
//   - local_ok: local checksums/integrity passed; cutover_checklist still lists secrets/themes/writes/remote
func Inspect(manifestPath string) (*Inspection, error) {
	return InspectWith(manifestPath, nil)
}

// InspectWith is Inspect, verifying a keyed manifest HMAC when key is the
// 32-byte site secret. Signed manifests without a matching key are blocked.
func InspectWith(manifestPath string, key []byte) (*Inspection, error) {
	m, err := ReadManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(manifestPath)
	r := &Inspection{Stamp: m.Stamp, CheckedAt: time.Now().UTC().Format(time.RFC3339), Consistency: m.Consistency, Readiness: "local_ok",
		Errors: []string{}, Warnings: []string{}, CutoverChecklist: append([]string{}, backupLimitations...),
		ExternalRequirements: []ExternalRequirement{}, Remote: receiptAt(dir, m.Stamp)}
	if m.HMAC != "" {
		ok, herr := CheckManifestHMAC(m, key)
		if herr != nil || !ok {
			r.Errors = append(r.Errors, "manifest_hmac_mismatch")
			r.LocalComplete = false
			r.IntegrityOK = false
			r.Readiness = "blocked"
			return r, nil
		}
	}
	r.LocalComplete = true
	for _, f := range []FileDigest{m.Database, m.Archive} {
		p := filepath.Join(dir, f.Name)
		actual, err := digest(p)
		if err != nil {
			r.Errors = append(r.Errors, "pair_file_missing_or_unreadable")
			r.LocalComplete = false
			continue
		}
		if actual.Size != f.Size || actual.SHA256 != f.SHA256 {
			r.Errors = append(r.Errors, "pair_checksum_mismatch")
		}
		if f.Size > MaxEncryptedFile {
			r.Warnings = append(r.Warnings, "encrypted_upload_limit_exceeded")
		}
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Lstat(filepath.Join(dir, m.Database.Name) + suffix); err == nil || !os.IsNotExist(err) {
			r.Errors = append(r.Errors, "stale_sqlite_sidecar")
		}
	}
	if len(r.Errors) == 0 {
		files, keys, err := scanArchive(filepath.Join(dir, m.Archive.Name), nil)
		if err != nil {
			r.Errors = append(r.Errors, "archive_invalid_or_unsafe")
		} else {
			r.ArchiveFiles, r.RecoveryKeyArchived = files, keys
		}
		d, err := openReadOnlyDB(filepath.Join(dir, m.Database.Name))
		if err != nil {
			r.Errors = append(r.Errors, "database_unreadable")
		} else {
			r.checkDatabase(d)
			if err := d.Close(); err != nil {
				r.Errors = append(r.Errors, "database_close_failed")
			}
		}
	}
	if !r.RecoveryKeyArchived {
		r.CutoverChecklist = append(r.CutoverChecklist, "independent_original_secret_required")
	}
	if r.Remote.State != "uploaded" {
		r.CutoverChecklist = append(r.CutoverChecklist, "remote_pair_not_confirmed")
	}
	r.CutoverChecklist = append(r.CutoverChecklist, "remote_readback_not_verified")
	switch {
	case len(r.Errors) > 0:
		r.Readiness = "blocked"
	case len(r.Warnings) > 0:
		r.Readiness = "review_required"
	default:
		r.Readiness = "local_ok"
	}
	return r, nil
}

func openReadOnlyDB(path string) (*sql.DB, error) {
	uri, err := db.FileURI(path)
	if err != nil {
		return nil, err
	}
	d, err := sql.Open("sqlite", uri+"?mode=ro&immutable=1")
	if err != nil {
		return nil, err
	}
	if err := d.Ping(); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

func (r *Inspection) checkDatabase(d *sql.DB) {
	rows, err := d.Query(`PRAGMA integrity_check`)
	if err != nil {
		r.Errors = append(r.Errors, "database_integrity_check_failed")
		return
	}
	r.IntegrityOK = true
	for rows.Next() {
		var answer string
		if rows.Scan(&answer) != nil || answer != "ok" {
			r.IntegrityOK = false
		}
	}
	if rows.Err() != nil {
		r.IntegrityOK = false
	}
	rows.Close()
	if !r.IntegrityOK {
		r.Errors = append(r.Errors, "database_integrity_failed")
	}
	rows, err = d.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		r.Errors = append(r.Errors, "foreign_key_check_failed")
		return
	}
	for rows.Next() {
		r.ForeignKeyViolations++
	}
	if rows.Err() != nil {
		r.Errors = append(r.Errors, "foreign_key_check_failed")
	}
	rows.Close()
	if r.ForeignKeyViolations != 0 {
		r.Errors = append(r.Errors, "foreign_key_violations")
	}
	r.checkExternalMedia(d)
}

func (r *Inspection) checkExternalMedia(d *sql.DB) {
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='media'`).Scan(&n); err != nil {
		r.Errors = append(r.Errors, "external_reference_check_failed")
		return
	}
	if n == 0 {
		return
	}
	rows, err := d.Query(`SELECT DISTINCT storage_key FROM media WHERE storage_key LIKE 'remote/%'`)
	if err != nil {
		r.Errors = append(r.Errors, "external_reference_check_failed")
		return
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			r.Errors = append(r.Errors, "external_reference_check_failed")
			continue
		}
		parts := strings.SplitN(key, "/", 3)
		if len(parts) != 3 || len(parts[1]) != 32 {
			r.Warnings = append(r.Warnings, "external_reference_invalid")
			continue
		}
		if id, err := hex.DecodeString(parts[1]); err != nil || len(id) != 16 {
			r.Warnings = append(r.Warnings, "external_reference_invalid")
			continue
		}
		if seen[parts[1]] {
			continue
		}
		seen[parts[1]] = true
		state := "destination_missing"
		var count int
		if err := d.QueryRow(`SELECT count(*) FROM settings WHERE key=?`, "storage.destination."+parts[1]).Scan(&count); err == nil && count == 1 {
			state = "configured_not_verified"
		}
		r.ExternalRequirements = append(r.ExternalRequirements, ExternalRequirement{Kind: "s3_media", Reference: parts[1], State: state})
		r.Warnings = append(r.Warnings, "external_media_required")
	}
	if rows.Err() != nil {
		r.Errors = append(r.Errors, "external_reference_check_failed")
	}
}

func archivePath(h *tar.Header) (string, error) {
	name := strings.TrimSuffix(h.Name, "/")
	if name == "" || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || path.Clean(name) != name || h.Size < 0 {
		return "", errors.New("backup: unsafe archive path")
	}
	if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir {
		return "", errors.New("backup: archive links and special files are forbidden")
	}
	for _, p := range recoveryFiles {
		if name == p && h.Typeflag == tar.TypeReg {
			return name, nil
		}
	}
	for _, dir := range Dirs {
		if name == dir && h.Typeflag == tar.TypeDir || strings.HasPrefix(name, dir+"/") {
			return name, nil
		}
	}
	return "", errors.New("backup: unexpected archive member")
}

func scanArchive(path string, visit func(string, *tar.Header, io.Reader) error) (int, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return 0, false, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	n, key := 0, false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return n, key, err
		}
		name, err := archivePath(h)
		if err != nil {
			return n, key, err
		}
		if seen[name] {
			return n, key, errors.New("backup: duplicate archive path")
		}
		seen[name] = true
		if name == "secret.key" {
			if h.Size > 1024 {
				return n, false, errors.New("backup: invalid recovery key file")
			}
			data, err := io.ReadAll(tr)
			if err != nil {
				return n, false, err
			}
			encoded := strings.TrimSpace(string(data))
			secret, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				secret, err = base64.RawURLEncoding.DecodeString(strings.TrimRight(encoded, "="))
			}
			if err != nil || len(secret) != 32 {
				return n, false, errors.New("backup: invalid recovery key file")
			}
			// Restore visits the bytes after validation; the key never enters a report.
			if visit != nil {
				if err := visit(name, h, strings.NewReader(string(data))); err != nil {
					return n, false, err
				}
			}
			key = true
			n++
			continue
		}
		if h.Typeflag == tar.TypeReg {
			n++
		}
		if visit != nil {
			if err := visit(name, h, tr); err != nil {
				return n, key, err
			}
		}
	}
	// Read the gzip trailer so truncated streams and checksum errors cannot pass.
	trailing, err := io.Copy(io.Discard, gz)
	if err == nil && trailing != 0 {
		err = errors.New("backup: trailing archive content")
	}
	return n, key, err
}
