package pattern

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zadstack/gorun/internal/config"
)

// EnvPatternsDir names the environment variable that points at the ship-with
// patterns directory.
const EnvPatternsDir = "GORUN_PATTERNS"

// EnvPatternsExtra lists extra pattern roots (PATH-separated), searched after
// the core directory. Operator-installed packages under DataDir/patterns win
// over ship-with packages of the same slug.
const EnvPatternsExtra = "GORUN_PATTERNS_EXTRA"

// SettingPattern stores the active layout pattern slug (empty = none).
const SettingPattern = "site.pattern"

// PatternsDir returns the core patterns directory.
func PatternsDir(cfg config.Config) string {
	if v := os.Getenv(EnvPatternsDir); v != "" {
		return v
	}
	for _, dir := range []string{"patterns", "/app/patterns", filepath.Join(cfg.DataDir, "bundled-patterns")} {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			return dir
		}
	}
	return filepath.Join(cfg.DataDir, "..", "patterns")
}

// ExtraPatternsDirs returns directories listed in $GORUN_PATTERNS_EXTRA.
func ExtraPatternsDirs() []string {
	var out []string
	for _, dir := range filepath.SplitList(os.Getenv(EnvPatternsExtra)) {
		if dir = strings.TrimSpace(dir); dir != "" {
			out = append(out, dir)
		}
	}
	return out
}

// dataPatternsDir is where operators may drop pattern packages for discovery.
func dataPatternsDir(cfg config.Config) string {
	return filepath.Join(cfg.DataDir, "patterns")
}

// roots returns pattern lookup roots: data dir first (operator packages),
// then core, then extras.
func (s *service) roots() []string {
	out := []string{dataPatternsDir(s.cfg)}
	out = append(out, s.root)
	out = append(out, s.extra...)
	return out
}

func validSlug(slug string) bool {
	if slug == "" || len(slug) > 41 {
		return false
	}
	if slug[0] < 'a' || slug[0] > 'z' {
		return false
	}
	for i := 1; i < len(slug); i++ {
		c := slug[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			continue
		}
		return false
	}
	return true
}

func patternPackageDir(roots []string, slug string) (string, error) {
	if !validSlug(slug) {
		return "", fmt.Errorf("%w: %q", ErrNotFound, slug)
	}
	for _, root := range roots {
		dir := filepath.Join(root, slug)
		if _, err := os.Stat(filepath.Join(dir, "pattern.json")); err == nil {
			return dir, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrNotFound, slug)
}
