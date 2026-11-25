package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const releaseUpgradeDocs = upgradeRepository + "/blob/main/docs/UPGRADE.md#2-upgrade--release-binary"

var upgradeThemeSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type previousTheme struct {
	Slug  string `json:"slug"`
	Added bool   `json:"added,omitempty"`
}

// localThemeDir distinguishes a missing directory from a blocked or unsafe entry.
func localThemeDir(path string) (bool, error) {
	st, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !st.IsDir() {
		return false, fmt.Errorf("%s must be a real directory", path)
	}
	return true, nil
}

func planThemeUpgrade(dir string, slugs []string) ([]previousTheme, error) {
	var plan []previousTheme
	for _, slug := range slugs {
		exists, err := localThemeDir(filepath.Join(dir, slug))
		if err != nil {
			return nil, err
		}
		if exists || slug == "default" {
			plan = append(plan, previousTheme{Slug: slug, Added: !exists})
		}
	}
	return plan, nil
}

func themeUpgradeError(dir string, err error) error {
	return fmt.Errorf("upgrade themes at %s: %w. Use --binary-only to skip themes or follow the manual runbook at %s", dir, err, releaseUpgradeDocs)
}

func checkThemeDirMode(dir string) error {
	st, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode().Perm()&0o222 == 0 {
		return fmt.Errorf("%s must be a writable directory", dir)
	}
	return nil
}

func writableThemeDir(dir string) error {
	if err := checkThemeDirMode(dir); err != nil {
		return err
	}
	probe, err := os.MkdirTemp(dir, ".gorun-write-*")
	if err != nil {
		return err
	}
	return os.Remove(probe)
}

// Check that the live and staging directories support same-filesystem renames.
func checkThemeStaging(dir, stage string) error {
	probe, err := os.MkdirTemp(dir, ".gorun-write-*")
	if err != nil {
		return err
	}
	if err := os.Rename(probe, filepath.Join(stage, ".probe")); err != nil {
		return errors.Join(err, os.Remove(probe))
	}
	return os.Remove(filepath.Join(stage, ".probe"))
}

func checkPreviousThemes(dir string, plan []previousTheme) error {
	previous := filepath.Join(dir, ".previous")
	exists, err := localThemeDir(previous)
	if err != nil || !exists {
		return err
	}
	if err := writableThemeDir(previous); err != nil {
		return err
	}
	for _, entry := range plan {
		if _, err := localThemeDir(filepath.Join(previous, entry.Slug)); err != nil {
			return err
		}
	}
	return nil
}

type themeRenames struct {
	root  *os.Root
	moves [][2]string
}

func (r *themeRenames) move(from, to string) error {
	if err := r.root.Rename(from, to); err != nil {
		return err
	}
	r.moves = append(r.moves, [2]string{from, to})
	return nil
}

func (r *themeRenames) restore() error {
	var err error
	for i := len(r.moves) - 1; i >= 0; i-- {
		move := r.moves[i]
		if undoErr := r.root.Rename(move[1], move[0]); undoErr != nil {
			err = errors.Join(err, fmt.Errorf("restore theme %s: %w", move[0], undoErr))
		}
	}
	return err
}

func swapUpgradeThemes(r *themeRenames, dir, stage string, plan []previousTheme) error {
	for _, entry := range plan {
		active := filepath.Join(dir, entry.Slug)
		previous := filepath.Join(dir, ".previous", entry.Slug)
		saved := filepath.Join(stage, ".saved-"+entry.Slug)
		if !entry.Added {
			if _, err := r.root.Lstat(previous); err == nil {
				if err := r.move(previous, saved); err != nil {
					return err
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err := r.move(active, previous); err != nil {
				return err
			}
		}
		if err := r.move(filepath.Join(stage, entry.Slug), active); err != nil {
			return err
		}
	}
	return nil
}

func validateThemeRollback(dir string, plan []previousTheme) error {
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil || !filepath.IsAbs(dir) || canonical != dir {
		return fmt.Errorf("invalid rollback themes directory %q", dir)
	}
	seen := make(map[string]bool)
	for _, entry := range plan {
		if !upgradeThemeSlug.MatchString(entry.Slug) || seen[entry.Slug] || (entry.Added && entry.Slug != "default") {
			return fmt.Errorf("invalid rollback theme %q", entry.Slug)
		}
		seen[entry.Slug] = true
		paths := []string{filepath.Join(dir, entry.Slug)}
		if !entry.Added {
			paths = append(paths, filepath.Join(dir, ".previous"), filepath.Join(dir, ".previous", entry.Slug))
		}
		for _, path := range paths {
			exists, err := localThemeDir(path)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("missing rollback theme directory %s", path)
			}
		}
	}
	return nil
}

func warnSkippedThemes(out io.Writer, dir string, slugs []string) {
	var skipped []string
	for _, slug := range slugs {
		// Include uncertain entries when a theme path cannot be inspected.
		if _, err := os.Lstat(filepath.Join(dir, slug)); !errors.Is(err, os.ErrNotExist) {
			skipped = append(skipped, slug)
		}
	}
	fmt.Fprintf(out, "WARN: --binary-only skips themes in %s. Bundled slugs left at the old release: %s.\n", dir, formatThemeSlugs(skipped))
}

func reportUpgradeThemes(out io.Writer, dir string, slugs []string) error {
	plan, err := planThemeUpgrade(dir, slugs)
	if err != nil {
		return themeUpgradeError(dir, err)
	}
	var refresh []string
	for _, entry := range plan {
		if entry.Added {
			fmt.Fprintf(out, "Install missing bundled theme %s in %s.\n", entry.Slug, dir)
		} else {
			refresh = append(refresh, entry.Slug)
		}
	}
	fmt.Fprintf(out, "Themes directory: %s. Bundled slugs to refresh: %s.\n", dir, formatThemeSlugs(refresh))
	return nil
}

func formatThemeSlugs(slugs []string) string {
	if len(slugs) == 0 {
		return "(none)"
	}
	return strings.Join(slugs, ", ")
}
