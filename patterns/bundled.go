// Package patterns bundles the core layout patterns for installs that carry
// only the executable.
package patterns

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed company editorial guidebook showcase
var bundled embed.FS

const stampFile = ".bundle-sha256"

// Install writes the bundled patterns to dir. It leaves a current copy alone,
// so a process sharing the data directory never sees it change, and replaces
// one from another release. dir belongs to the core: operator packages live in
// <data>/patterns.
func Install(dir string) error {
	stamp, err := digest()
	if err != nil {
		return err
	}
	if b, err := os.ReadFile(filepath.Join(dir, stampFile)); err == nil && string(b) == stamp {
		return nil
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".bundled-patterns-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	err = fs.WalkDir(bundled, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dest := filepath.Join(tmp, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		b, err := bundled.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, b, 0o644)
	})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, stampFile), []byte(stamp), 0o644); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return os.Rename(tmp, dir)
}

// digest identifies the bundled content, so it changes only with the patterns.
func digest() (string, error) {
	h := sha256.New()
	err := fs.WalkDir(bundled, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := bundled.ReadFile(path)
		if err != nil {
			return err
		}
		h.Write([]byte(path + "\x00"))
		h.Write(b)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil)), err
}
