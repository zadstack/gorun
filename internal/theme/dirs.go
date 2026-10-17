package theme

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zadstack/gorun/internal/config"
)

// EnvThemesDir names the environment variable that points at the themes
// directory.
const EnvThemesDir = "GORUN_THEMES"

// EnvThemesExtra names the environment variable listing extra theme
// directories (separated like PATH), searched after the core directory. Brand
// themes live there, outside the core catalog, on the servers that use them.
const EnvThemesExtra = "GORUN_THEMES_EXTRA"

// ExtraThemesDirs returns the directories listed in $GORUN_THEMES_EXTRA.
func ExtraThemesDirs() []string {
	var out []string
	for _, dir := range filepath.SplitList(os.Getenv(EnvThemesExtra)) {
		if dir = strings.TrimSpace(dir); dir != "" {
			out = append(out, dir)
		}
	}
	return out
}

// ThemesDir returns the themes directory: $GORUN_THEMES, else ./themes
// (relative to the working directory) when it exists, else /app/themes
// (the container layout) when it exists, else <DataDir>/../themes.
func ThemesDir(cfg config.Config) string {
	if v := os.Getenv(EnvThemesDir); v != "" {
		return v
	}
	for _, dir := range []string{"themes", "/app/themes"} {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			return dir
		}
	}
	return filepath.Join(cfg.DataDir, "..", "themes")
}

// overridesDir is where admin/MCP template overrides live for theme slug.
// The slug comes from a setting, so it is validated before it becomes a path.
func overridesDir(cfg config.Config, slug string) (string, error) {
	if !slugRe.MatchString(slug) {
		return "", fmt.Errorf("%w: theme slug %q", ErrInvalidTheme, slug)
	}
	return filepath.Join(cfg.DataDir, "overrides", slug), nil
}

// draftsSubdir holds unpublished overrides inside an overrides dir; the
// normal template set never reads it.
const draftsSubdir = "_drafts"
