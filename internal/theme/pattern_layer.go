package theme

import "context"

// PatternLayer is an optional layout package layered above the active theme
// and below site overrides. Patterns own opinionated page structure; themes
// remain identity systems (type, color, spacing, chrome). Wired by the
// pattern module after theme boots.
type PatternLayer interface {
	// Active returns the filesystem directory and slug of the active pattern.
	// ok is false when no pattern is active or the package cannot be loaded.
	Active(ctx context.Context) (dir, slug string, ok bool)
}

// PatternAware is implemented by the theme service so the pattern module can
// attach a layer and flush compiled template caches when the active pattern
// changes.
type PatternAware interface {
	SetPatternLayer(p PatternLayer)
	InvalidateCache()
}

var _ PatternAware = (*service)(nil)

// SetPatternLayer attaches the layout pattern provider (nil clears it).
func (s *service) SetPatternLayer(p PatternLayer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.patterns = p
	clear(s.cache)
}

// InvalidateCache drops compiled template sets so the next render reloads
// layers (overrides, pattern, theme).
func (s *service) InvalidateCache() { s.invalidate() }
