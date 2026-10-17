package diagnostics

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

type handler struct {
	base      slog.Handler
	collector *Collector
}

func (c *Collector) Handler(base slog.Handler) slog.Handler { return &handler{base, c} }
func (h *handler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= slog.LevelInfo || h.base.Enabled(ctx, l)
}
func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	if r.Message == "http" {
		status := 0
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "status" && a.Value.Kind() == slog.KindInt64 {
				n := a.Value.Int64()
				if n >= 500 && n <= 599 {
					status = int(n)
				}
			}
			return true
		})
		if status > 0 {
			h.collector.observe("http", "http_5xx", status, time.Now())
		}
	}
	if r.Level >= slog.LevelError {
		category := "application"
		for _, prefix := range prefixCategories() {
			if strings.HasPrefix(r.Message, prefix+":") || strings.HasPrefix(r.Message, prefix+" ") {
				category = prefix
				break
			}
		}
		code := "runtime_error"
		if r.Message == "http: panic" {
			category = "http"
			code = "panic"
		}
		h.collector.observe(category, code, 0, time.Now())
	}
	if h.base.Enabled(ctx, r.Level) {
		return h.base.Handle(ctx, r)
	}
	return nil
}
func (h *handler) WithAttrs(a []slog.Attr) slog.Handler {
	return &handler{h.base.WithAttrs(a), h.collector}
}
func (h *handler) WithGroup(g string) slog.Handler { return &handler{h.base.WithGroup(g), h.collector} }
