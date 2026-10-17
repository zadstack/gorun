package wpmigrate

import (
	"context"
	"html"
	"net/url"
	"strings"
)

// A media attribute is not a permalink: without an imported media match,
// retaining the source is safer than inventing a nonexistent local image.
func (rw *rewriter) rewriteImage(ctx context.Context, value string, stats *rewriteStats) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return value
	}
	start := strings.Index(value, fields[0])
	end := start + len(fields[0])
	raw := html.UnescapeString(fields[0])
	if local := rw.mappedMedia(ctx, raw); local != "" {
		stats.media++
		return value[:start] + local + value[end:]
	}
	candidate, err := url.Parse(raw)
	if err != nil {
		return value
	}
	if candidate.Scheme == "" && rw.base != nil {
		candidate = rw.base.ResolveReference(candidate)
	}
	if candidate.Scheme != "http" && candidate.Scheme != "https" {
		return value
	}
	stats.unresolved++
	return value[:start] + html.EscapeString(candidate.String()) + value[end:]
}

func reverseSrcsetSpans(value string) [][2]int {
	spans := srcsetURLSpans(value)
	for left, right := 0, len(spans)-1; left < right; left, right = left+1, right-1 {
		spans[left], spans[right] = spans[right], spans[left]
	}
	return spans
}
