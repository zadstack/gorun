package backup

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// manifestMACDomain separates backup manifest HMACs from other site-secret uses
// (same pattern as backupCipher's "gorun.private.backup.v1").
const manifestMACDomain = "gorun.private.backup.manifest.v1"

const ManifestVersion = 1
const consistency = "database_snapshot_files_not_atomic"

var backupLimitations = []string{"writes_must_stop_for_full_consistency", "external_environment_not_archived", "s3_objects_not_archived", "theme_bundles_not_archived", "original_site_secret_required"}

var manifestName = regexp.MustCompile(`^(\d{8}T\d{6}Z)\.manifest\.json$`)

type FileDigest struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Manifest struct {
	FormatVersion      int        `json:"format_version"`
	Stamp              string     `json:"stamp"`
	CreatedAt          string     `json:"created_at"`
	Database           FileDigest `json:"database"`
	Archive            FileDigest `json:"archive"`
	Consistency        string     `json:"consistency"`
	RecoveryFiles      []string   `json:"recovery_files"`
	Limitations        []string   `json:"limitations"`
	EncryptedFileLimit int64      `json:"encrypted_file_limit"`
	// HMAC is hex(HMAC-SHA256(site secret, domain || canonical digests)).
	// Empty on legacy manifests written before keyed integrity.
	HMAC string `json:"hmac,omitempty"`
}

type UploadFile struct {
	Name      string `json:"name"`
	State     string `json:"state"`
	Code      string `json:"code,omitempty"`
	CheckedAt string `json:"checked_at"`
}

// UploadReceipt records provider acknowledgments, never remote read-back proof.
type UploadReceipt struct {
	State            string       `json:"state"`
	Files            []UploadFile `json:"files"`
	ReadBackVerified bool         `json:"read_back_verified"`
	DatabaseSHA256   string       `json:"database_sha256,omitempty"`
	ArchiveSHA256    string       `json:"archive_sha256,omitempty"`
}

func digest(path string) (FileDigest, error) {
	var out FileDigest
	st, err := os.Lstat(path)
	if err != nil {
		return out, err
	}
	if !st.Mode().IsRegular() {
		return out, errors.New("backup: file must be regular")
	}
	f, err := os.Open(path)
	if err != nil {
		return out, err
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return out, errors.Join(err, closeErr)
	}
	return FileDigest{Name: filepath.Base(path), Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

func writeManifest(res Result, recovery []string, now time.Time, key []byte) error {
	database, err := digest(res.DB)
	if err != nil {
		return err
	}
	archive, err := digest(res.Files)
	if err != nil {
		return err
	}
	m := Manifest{FormatVersion: ManifestVersion, Stamp: res.Stamp, CreatedAt: now.UTC().Format(time.RFC3339), Database: database, Archive: archive,
		Consistency: consistency, RecoveryFiles: recovery, EncryptedFileLimit: MaxEncryptedFile,
		Limitations: append([]string{}, backupLimitations...)}
	if len(key) == 32 {
		m.HMAC = manifestHMAC(key, m)
	}
	return writeJSONExclusive(res.Manifest, m)
}

// manifestHMAC binds the stamp and file digests to the site secret so a
// writable backups directory alone cannot forge an accepted pair.
func manifestHMAC(key []byte, m Manifest) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(manifestMACDomain))
	mac.Write([]byte{0})
	mac.Write([]byte(m.Stamp))
	mac.Write([]byte{0})
	mac.Write([]byte(m.Database.Name))
	mac.Write([]byte{0})
	mac.Write([]byte(m.Database.SHA256))
	mac.Write([]byte{0})
	mac.Write([]byte(m.Archive.Name))
	mac.Write([]byte{0})
	mac.Write([]byte(m.Archive.SHA256))
	return hex.EncodeToString(mac.Sum(nil))
}

// CheckManifestHMAC reports whether m was signed with key. Unsigned
// (legacy) manifests return false, nil — callers decide whether to warn.
func CheckManifestHMAC(m *Manifest, key []byte) (ok bool, err error) {
	if m == nil || m.HMAC == "" {
		return false, nil
	}
	if len(key) != 32 {
		return false, errors.New("backup: site secret required to verify manifest hmac")
	}
	want, err := hex.DecodeString(m.HMAC)
	if err != nil || len(want) != sha256.Size {
		return false, errors.New("backup: invalid manifest hmac")
	}
	got, err := hex.DecodeString(manifestHMAC(key, *m))
	if err != nil {
		return false, err
	}
	if !hmac.Equal(want, got) {
		return false, errors.New("backup: manifest hmac mismatch")
	}
	return true, nil
}

func writeJSONExclusive(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(append(raw, '\n'))
	syncErr := f.Sync()
	closeErr := f.Close()
	if err = errors.Join(writeErr, syncErr, closeErr); err != nil {
		os.Remove(path)
	}
	return err
}

func readJSONRegular(path string, out any) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Size() > 1<<20 {
		return errors.New("backup: metadata must be a small regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, (1<<20)+1))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return errors.New("backup: invalid metadata")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("backup: invalid metadata trailing data")
	}
	return nil
}

func ReadManifest(path string) (*Manifest, error) {
	match := manifestName.FindStringSubmatch(filepath.Base(path))
	if match == nil {
		return nil, errors.New("backup: invalid manifest filename")
	}
	var m Manifest
	if err := readJSONRegular(path, &m); err != nil {
		return nil, err
	}
	if m.FormatVersion != ManifestVersion || m.Stamp != match[1] || m.Database.Name != m.Stamp+".db" || m.Archive.Name != m.Stamp+".tar.gz" || m.Consistency != consistency {
		return nil, errors.New("backup: unsupported or mismatched manifest")
	}
	for _, f := range []FileDigest{m.Database, m.Archive} {
		h, err := hex.DecodeString(f.SHA256)
		if err != nil || len(h) != sha256.Size || f.Size < 0 {
			return nil, errors.New("backup: invalid file digest")
		}
	}
	return &m, nil
}

func StoreUploadReceipt(res Result, receipt UploadReceipt) error {
	if !fileName.MatchString(res.Stamp+".db") || filepath.Base(res.DB) != res.Stamp+".db" || filepath.Base(res.Files) != res.Stamp+".tar.gz" || filepath.Dir(res.DB) != filepath.Dir(res.Files) {
		return errors.New("backup: invalid upload pair")
	}
	path := filepath.Join(filepath.Dir(res.DB), res.Stamp+".remote.json")
	m, err := ReadManifest(filepath.Join(filepath.Dir(res.DB), res.Stamp+".manifest.json"))
	if err != nil {
		return err
	}
	for _, f := range []FileDigest{m.Database, m.Archive} {
		actual, err := digest(filepath.Join(filepath.Dir(res.DB), f.Name))
		if err != nil || actual.SHA256 != f.SHA256 || actual.Size != f.Size {
			return errors.New("backup: upload pair changed")
		}
	}
	receipt.DatabaseSHA256, receipt.ArchiveSHA256 = m.Database.SHA256, m.Archive.SHA256
	// Each backup has one manual upload attempt. O_EXCL keeps receipts immutable.
	return writeJSONExclusive(path, receipt)
}

func receiptAt(dir, stamp string) UploadReceipt {
	unknown := UploadReceipt{State: "unverified", Files: []UploadFile{}}
	var receipt UploadReceipt
	if readJSONRegular(filepath.Join(dir, stamp+".remote.json"), &receipt) != nil || receipt.ReadBackVerified {
		return unknown
	}
	if len(receipt.Files) != 2 {
		return unknown
	}
	m, err := ReadManifest(filepath.Join(dir, stamp+".manifest.json"))
	if err != nil || receipt.DatabaseSHA256 != m.Database.SHA256 || receipt.ArchiveSHA256 != m.Archive.SHA256 {
		return unknown
	}
	uploaded := 0
	for i, f := range receipt.Files {
		name := stamp + ".db"
		if i == 1 {
			name = stamp + ".tar.gz"
		}
		if f.Name != name || f.CheckedAt == "" {
			return unknown
		}
		switch f.State {
		case "uploaded":
			uploaded++
		case "failed", "not_attempted":
		default:
			return unknown
		}
	}
	state := "failed"
	if uploaded == 2 {
		state = "uploaded"
	} else if uploaded == 1 {
		state = "partial"
	}
	if receipt.State != state {
		return unknown
	}
	return receipt
}
