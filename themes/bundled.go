// Package themes bundles the default theme for standalone local starts.
package themes

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed default
var bundled embed.FS

// InstallDefault installs a complete default theme without replacing existing files.
func InstallDefault(root string) error {
	if _, err := os.Stat(filepath.Join(root, "default")); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(root, ".default-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	err = fs.WalkDir(bundled, "default", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel("default", path)
		if err != nil {
			return err
		}
		dest := filepath.Join(tmp, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0755)
		}
		b, err := bundled.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, b, 0644)
	})
	if err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(root, "default"))
}
