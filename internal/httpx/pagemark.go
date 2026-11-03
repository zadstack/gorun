package httpx

import (
	"context"
	"sync/atomic"
)

// PageMark records, for one request, that the active theme rendered the
// response. A middleware that must act only on theme pages (analytics
// script injection) installs one with WithPageMark before calling the
// handler, and the theme renderer calls MarkThemePage on success. Nothing
// travels in the response, so no header can leak to the client.
type PageMark struct{ themed atomic.Bool }

type pageMarkKey struct{}

// WithPageMark returns ctx carrying a fresh mark, and the mark.
func WithPageMark(ctx context.Context) (context.Context, *PageMark) {
	m := &PageMark{}
	return context.WithValue(ctx, pageMarkKey{}, m), m
}

// MarkThemePage notes that the theme rendered this request's page. It is a
// no-op when no middleware installed a mark.
func MarkThemePage(ctx context.Context) {
	if m, ok := ctx.Value(pageMarkKey{}).(*PageMark); ok {
		m.themed.Store(true)
	}
}

// Themed reports whether MarkThemePage was called. A nil mark is false.
func (m *PageMark) Themed() bool { return m != nil && m.themed.Load() }
