package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
)

// URLPrefix is where stored files are served (GET /media/{key...}).
const URLPrefix = "/media/"

// keyPattern is the only key shape the local driver accepts:
// Existing YYYY/MM files and generated site/content/date paths; no traversal.
var keyPattern = regexp.MustCompile(`^(?:remote/[a-f0-9]{32}/)?(?:[0-9]{4}/[0-9]{2}|sites/[0-9a-hjkmnp-tv-z]{26}/(?:uploads/[0-9]{4}/[0-9]{2}|content/[0-9a-hjkmnp-tv-z]{26}/[0-9]{4}/[0-9]{2}/[0-9]{2}))/[0-9A-Za-z_-]{1,80}\.[a-z0-9]{1,5}$`)

// ErrInvalidKey is returned for keys outside keyPattern.
var ErrInvalidKey = errors.New("media: invalid storage key")

// LocalStorage stores files under a directory on disk; every access goes
// through os.Root so a key can never escape it.
type LocalStorage struct{ dir string }

// NewLocalStorage returns a driver rooted at dir (created on first Put).
func NewLocalStorage(dir string) *LocalStorage { return &LocalStorage{dir: dir} }

func (l *LocalStorage) root() (*os.Root, error) {
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return nil, fmt.Errorf("media: create storage dir: %w", err)
	}
	return os.OpenRoot(l.dir)
}

// Put writes r to key atomically (temp file + rename).
func (l *LocalStorage) Put(_ context.Context, key string, r io.Reader, _ string) error {
	if !keyPattern.MatchString(key) {
		return ErrInvalidKey
	}
	root, err := l.root()
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.MkdirAll(path.Dir(key), 0o755); err != nil {
		return fmt.Errorf("media: mkdir: %w", err)
	}
	tmp := key + ".tmp"
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("media: create: %w", err)
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		_ = root.Remove(tmp)
		return fmt.Errorf("media: write: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = root.Remove(tmp)
		return fmt.Errorf("media: close: %w", err)
	}
	if err := root.Rename(tmp, key); err != nil {
		_ = root.Remove(tmp)
		return fmt.Errorf("media: rename: %w", err)
	}
	return nil
}

// Open returns the file at key (an *os.File, so callers may Seek/Stat).
func (l *LocalStorage) Open(_ context.Context, key string) (io.ReadCloser, error) {
	if !keyPattern.MatchString(key) {
		return nil, ErrInvalidKey
	}
	root, err := os.OpenRoot(l.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fs.ErrNotExist
		}
		return nil, err
	}
	defer root.Close()
	return root.Open(key)
}

// Delete removes key; a missing file is not an error.
func (l *LocalStorage) Delete(_ context.Context, key string) error {
	if !keyPattern.MatchString(key) {
		return ErrInvalidKey
	}
	root, err := os.OpenRoot(l.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer root.Close()
	if err := root.Remove(key); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("media: delete: %w", err)
	}
	return nil
}

// URL returns the public URL of key.
func (l *LocalStorage) URL(key string) string { return URLPrefix + key }
