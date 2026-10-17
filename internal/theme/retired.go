package theme

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/zadstack/gorun/internal/db"
)

// retiredThemes maps theme slugs folded into the general catalog to the theme
// that replaced them. Sites, starters, saved settings and provisioning
// tools that still name an old slug keep working.
var retiredThemes = map[string]string{
	"minimal":          "default",
	"studio-business":  "business",
	"studio-editorial": "magazine",
	"studio-portfolio": "portfolio",
}

// canonicalSlug returns the theme a retired slug now names, else slug.
func canonicalSlug(slug string) string {
	if next, ok := retiredThemes[slug]; ok {
		return next
	}
	return slug
}

// migrateRetiredTheme moves a site still on a retired theme to its
// replacement, once: the active theme setting, that theme's settings
// (theme.<old>.* → theme.<new>.*, never replacing a key the new slug already
// has) and all nonconflicting overrides. Old keys and files remain intact.
// Files are copied before the atomic settings commit, so a failed migration
// leaves the old active theme intact and can safely be retried at boot.
func (s *service) migrateRetiredTheme(ctx context.Context) error {
	if s.d.Settings == nil {
		return nil
	}
	old := s.d.Settings.GetString(ctx, SettingTheme, s.d.Config.Theme)
	next, retired := retiredThemes[old]
	if !retired {
		return nil
	}
	// Binary-only upgrades must preserve installed legacy identities. Migrate
	// only when a release has actually removed the old theme directory.
	if _, err := findExactInfo(s.roots(), old); err == nil {
		return nil
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	if _, err := findExactInfo(s.roots(), next); err != nil {
		return err
	}
	oldPrefix, newPrefix := "theme."+old+".", "theme."+next+"."
	previous, err := s.d.Settings.All(ctx, oldPrefix)
	if err != nil {
		return err
	}
	current, err := s.d.Settings.All(ctx, newPrefix)
	if err != nil {
		return err
	}
	if err := moveOverrides(s, old, next); err != nil {
		return err
	}
	err = db.InTx(ctx, s.d.DB, func(tx *sql.Tx) error {
		for key, value := range previous {
			moved := newPrefix + strings.TrimPrefix(key, oldPrefix)
			if _, taken := current[moved]; taken {
				continue
			}
			if err := s.d.Settings.SetTx(ctx, tx, moved, value); err != nil {
				return err
			}
		}
		return s.d.Settings.SetTx(ctx, tx, SettingTheme, next)
	})
	if err != nil {
		return err
	}
	s.invalidate()
	s.d.Audit.Record(ctx, "theme.migrate", "theme", next, map[string]any{"from": old})
	return nil
}

func moveOverrides(s *service, old, next string) error {
	from, err := overridesDir(s.d.Config, old)
	if err != nil {
		return err
	}
	to, err := overridesDir(s.d.Config, next)
	if err != nil {
		return err
	}
	// Preflight both trees before copying anything. Never follow symlinks or
	// overwrite a differing destination file (including unpublished drafts).
	source, err := overrideFiles(from)
	if err != nil {
		return err
	}
	target, err := overrideFiles(to)
	if err != nil {
		return err
	}
	for name, data := range source {
		if existing, ok := target[name]; ok && !bytes.Equal(data, existing) {
			return fmt.Errorf("theme: override conflict %s; resolve before migrating %s to %s", name, old, next)
		}
	}
	for name, data := range source {
		if _, exists := target[name]; exists {
			continue
		}
		dest := filepath.Join(to, name)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := copyOverride(dest, data); err != nil {
			return err
		}
	}
	return nil
}

func overrideFiles(root string) (map[string][]byte, error) {
	files := map[string][]byte{}
	if _, err := os.Lstat(root); errors.Is(err, fs.ErrNotExist) {
		return files, nil
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("theme: unsupported override file %s", path)
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[name] = data
		return nil
	})
	return files, err
}

// Publish a complete file without replacing any existing destination. If a
// boot was interrupted after a prior file copy, the next preflight accepts it.
func copyOverride(dest string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(dest), ".theme-migration-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(0o644); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Link(f.Name(), dest)
}
