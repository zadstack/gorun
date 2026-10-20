package pattern

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings"
	"github.com/zadstack/gorun/internal/theme"
)

// Service manages layout pattern packages.
type Service interface {
	List(ctx context.Context) ([]Info, error)
	Lookup(slug string) (Info, error)
	ActiveInfo(ctx context.Context) (Info, bool)
	Activate(ctx context.Context, slug string) error
	Clear(ctx context.Context) error
}

type service struct {
	cfg      config.Config
	settings *settings.Store
	audit    interface {
		Record(ctx context.Context, action, objectType, objectID string, meta any)
	}
	log   *slog.Logger
	root  string
	extra []string
	theme theme.PatternAware
}

var _ Service = (*service)(nil)
var _ theme.PatternLayer = (*service)(nil)

func newService(d *kernel.Deps, root string, th theme.PatternAware) *service {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &service{
		cfg: d.Config, settings: d.Settings, audit: d.Audit, log: log,
		root: root, extra: ExtraPatternsDirs(), theme: th,
	}
}

// Active implements theme.PatternLayer.
func (s *service) Active(ctx context.Context) (dir, slug string, ok bool) {
	info, yes := s.ActiveInfo(ctx)
	if !yes {
		return "", "", false
	}
	return info.Dir, info.Slug, true
}

func (s *service) activeSlug(ctx context.Context) string {
	if s.settings == nil {
		return ""
	}
	return strings.TrimSpace(s.settings.GetString(ctx, SettingPattern, ""))
}

// ActiveInfo returns the active pattern when one is set and loadable.
func (s *service) ActiveInfo(ctx context.Context) (Info, bool) {
	slug := s.activeSlug(ctx)
	if slug == "" {
		return Info{}, false
	}
	info, err := s.Lookup(slug)
	if err != nil {
		s.log.Warn("pattern: active package missing", "slug", slug, "err", err)
		return Info{}, false
	}
	return info, true
}

// List returns installed pattern packages (data dir, then bundled, then extras).
func (s *service) List(context.Context) ([]Info, error) {
	seen := map[string]bool{}
	var out []Info
	dataRoot := dataPatternsDir(s.cfg)
	for i, root := range s.roots() {
		bundled := root != dataRoot
		entries, err := os.ReadDir(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("pattern: list %s: %w", root, err)
		}
		for _, e := range entries {
			if !e.IsDir() || seen[e.Name()] {
				continue
			}
			info, err := loadInfo(filepath.Join(root, e.Name()), bundled)
			if err != nil {
				if i == 0 {
					s.log.Debug("pattern: skip package", "dir", e.Name(), "err", err)
				}
				continue
			}
			seen[info.Slug] = true
			out = append(out, info)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

// Lookup reads one installed pattern.
func (s *service) Lookup(slug string) (Info, error) {
	dir, err := patternPackageDir(s.roots(), slug)
	if err != nil {
		return Info{}, err
	}
	bundled := filepath.Clean(filepath.Dir(dir)) != filepath.Clean(dataPatternsDir(s.cfg))
	return loadInfo(dir, bundled)
}

// Activate validates and stores site.pattern, then invalidates theme caches.
func (s *service) Activate(ctx context.Context, slug string) error {
	info, err := s.Lookup(slug)
	if err != nil {
		return err
	}
	if err := s.settings.Set(ctx, SettingPattern, info.Slug); err != nil {
		return err
	}
	if s.theme != nil {
		s.theme.InvalidateCache()
	}
	if s.audit != nil {
		s.audit.Record(ctx, "pattern.activate", "pattern", info.Slug, nil)
	}
	return nil
}

// Clear removes the active pattern (theme templates only).
func (s *service) Clear(ctx context.Context) error {
	if err := s.settings.Set(ctx, SettingPattern, ""); err != nil {
		return err
	}
	if s.theme != nil {
		s.theme.InvalidateCache()
	}
	if s.audit != nil {
		s.audit.Record(ctx, "pattern.clear", "pattern", "", nil)
	}
	return nil
}
