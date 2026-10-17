package wpmigrate

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
)

// previewResult aggregates the "would" plans of a dry run.
func (s *service) previewResult(ctx context.Context, migrationID string) (*PreviewResult, error) {
	list, err := s.items(ctx, migrationID, "")
	if err != nil {
		return nil, err
	}
	res := &PreviewResult{MigrationID: migrationID, Counts: map[string]map[string]int{}, Shortcodes: map[string]int{},
		UnknownBlocks: map[string]int{}, SlugConflicts: []SlugConflict{}, Notes: []string{}, ContentStateCounts: map[string]map[string]int{}}
	opts := Options{}
	if m, err := s.getMigration(ctx, migrationID); err == nil {
		opts = m.Options
	}
	needsCommerce := false
	for _, it := range list {
		action, _ := it.Would["action"].(string)
		if action == "" {
			action = it.Status
		}
		if res.Counts[it.Kind] == nil {
			res.Counts[it.Kind] = map[string]int{}
		}
		res.Counts[it.Kind][action]++
		switch it.Kind {
		case "media":
			if action == "download" {
				res.MediaCount++
				res.MediaBytes += int64(num(it.Would["bytes"]))
			}
		case "product":
			if it.Error == "needs_commerce_module" {
				needsCommerce = true
			}
		case "content":
			var c WPContent
			if err := json.Unmarshal(it.Payload, &c); err != nil {
				return nil, err
			}
			if res.ContentStateCounts[c.Type] == nil {
				res.ContentStateCounts[c.Type] = map[string]int{}
			}
			res.ContentStateCounts[c.Type][c.Status]++
			res.ContentStates = append(res.ContentStates, ContentStatePlan{Type: c.Type, WPID: it.WPID, SourceStatus: c.Status, LocalStatus: string(mapStatus(c.Status)), PublishAt: contentPublishAt(c), SourceDate: wpTime(c.Date)})
			addCounts(res.Shortcodes, it.Would["shortcodes"])
			addCounts(res.UnknownBlocks, it.Would["unknown_blocks"])
			if it.Would["markdown_repaired"] == true {
				res.MarkdownRepaired++
			}
			conflict, _ := it.Would["conflict"].(string)
			if conflict != "" {
				typ, _ := it.Would["type"].(string)
				slug, _ := it.Would["slug"].(string)
				final, _ := it.Would["final_slug"].(string)
				res.SlugConflicts = append(res.SlugConflicts, SlugConflict{Type: typ, WPID: it.WPID, Slug: slug,
					FinalSlug: final, Policy: opts.SlugPolicy, Reason: conflict})
			}
		}
	}
	if needsCommerce {
		res.Notes = append(res.Notes, "wpmigrate.preview.needs_commerce")
	}
	if len(res.Shortcodes) > 0 {
		res.Notes = append(res.Notes, "wpmigrate.preview.shortcodes")
	}
	return res, nil
}

func num(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}

func addCounts(dst map[string]int, v any) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	for k, n := range m {
		dst[k] += int(num(n))
	}
}

// relativeURL turns a same-site absolute URL into a path.
func relativeURL(raw string, hosts map[string]bool) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || !hosts[hostOf(raw)] {
		return raw
	}
	out := u.EscapedPath()
	if out == "" {
		out = "/"
	}
	if u.RawQuery != "" {
		out += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		out += "#" + u.EscapedFragment()
	}
	return out
}
