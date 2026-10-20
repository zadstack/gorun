package pattern

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zadstack/gorun/pkg/patternkit"
)

var (
	// ErrNotFound is returned for unknown pattern slugs.
	ErrNotFound = errors.New("pattern: not found")
	// ErrInvalid is returned for a broken pattern package.
	ErrInvalid = errors.New("pattern: invalid")
)

// Info is a discovered pattern package.
type Info struct {
	Slug        string            `json:"slug"`
	Version     string            `json:"version"`
	Name        map[string]string `json:"name"`
	Description map[string]string `json:"description"`
	Dir         string            `json:"dir"`
	Bundled     bool              `json:"bundled"`
}

// DisplayName picks a localized label.
func (i Info) DisplayName(locale string) string {
	if v := strings.TrimSpace(i.Name[locale]); v != "" {
		return v
	}
	if v := strings.TrimSpace(i.Name["en"]); v != "" {
		return v
	}
	if v := strings.TrimSpace(i.Name["ar"]); v != "" {
		return v
	}
	return i.Slug
}

// DisplayDescription picks a localized description.
func (i Info) DisplayDescription(locale string) string {
	if v := strings.TrimSpace(i.Description[locale]); v != "" {
		return v
	}
	if v := strings.TrimSpace(i.Description["en"]); v != "" {
		return v
	}
	return strings.TrimSpace(i.Description["ar"])
}

func loadInfo(dir string, bundled bool) (Info, error) {
	issues := patternkit.ValidateDir(dir)
	if len(issues) > 0 {
		return Info{}, fmt.Errorf("%w: %s", ErrInvalid, issues[0])
	}
	raw, err := os.ReadFile(filepath.Join(dir, patternkit.ManifestFile))
	if err != nil {
		return Info{}, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	var m patternkit.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Info{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	abs := dir
	if a, err := filepath.Abs(dir); err == nil {
		abs = a
	}
	return Info{
		Slug:        m.Slug,
		Version:     m.Version,
		Name:        m.Name,
		Description: m.Description,
		Dir:         abs,
		Bundled:     bundled,
	}, nil
}
