package wpmigrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
)

// generate creates or refreshes the items of a step. A source step fetches
// from WordPress unless it already did in this run (fetched_through, reset
// by Start/Preview); derived steps (redirects, rewrite) are always rebuilt
// from imported items.
func (s *service) generate(ctx context.Context, m *migrationRow, step string) error {
	switch step {
	case "redirects":
		return s.generateRedirects(ctx, m)
	case "rewrite":
		if m.DryRun {
			return nil
		}
		return s.generateRewrites(ctx, m)
	}
	idx := slices.Index(steps, step)
	if idx <= m.FetchedThrough {
		return nil
	}
	if err := s.fetchStep(ctx, m, step); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE migrations SET fetched_through = max(fetched_through, ?) WHERE id = ? AND cursor = ?`,
		idx, m.ID, m.Cursor)
	return err
}

// fetchStep reads one source step from WordPress and upserts its items.
func (s *service) fetchStep(ctx context.Context, m *migrationRow, step string) error {
	src, err := s.getSource(ctx, m.SourceID)
	if err != nil {
		return err
	}
	source, err := s.openSourceFor(ctx, m, src)
	if err != nil {
		return err
	}
	var items []itemRow
	switch step {
	case "users":
		if m.Options.SkipUsers {
			return nil
		}
		users, err := source.Users(ctx)
		if err != nil {
			return s.unavailable(ctx, m, step, err)
		}
		for _, u := range users {
			items = append(items, newItem("user", "user", u.ID, u.Slug, "", u.Email+"|"+u.Name, u))
		}
	case "terms":
		terms, err := source.Terms(ctx, m.Mapping.taxonomies())
		if err != nil {
			return s.unavailable(ctx, m, step, err)
		}
		depth := termDepths(terms)
		for _, t := range terms {
			it := newItem("term", t.Taxonomy, t.ID, t.Slug, "", t.Slug+"|"+t.Name+"|"+strconv.Itoa(t.Parent)+"|"+t.Description, t)
			it.Phase = depth[t.ID]
			items = append(items, it)
		}
	case "media":
		media, err := source.Media(ctx)
		if err != nil {
			return s.unavailable(ctx, m, step, err)
		}
		for _, md := range media {
			items = append(items, newItem("media", "attachment", md.ID, md.Slug, md.URL, md.Modified+"|"+md.URL, md))
		}
	case "content":
		list, err := source.Content(ctx, m.Mapping.contentTypes())
		if err != nil {
			return err
		}
		depth := pageDepths(list)
		for _, c := range list {
			sum := hashJSON(c)
			it := newItem("content", c.Type, c.ID, c.Slug, c.Link, sum, c)
			if c.Type == "page" {
				it.Phase = depth[c.ID]
			}
			items = append(items, it)
		}
	case "products":
		if !m.Mapping.wantsProducts() {
			return nil
		}
		products, err := source.Products(ctx)
		if err != nil {
			return s.unavailable(ctx, m, step, err)
		}
		for _, p := range products {
			items = append(items, newItem("product", "product", p.ID, p.Slug, "", hashJSON(p), p))
		}
	case "menus":
		menus, err := source.Menus(ctx)
		if err != nil {
			return s.unavailable(ctx, m, step, err)
		}
		for _, mn := range menus {
			items = append(items, newItem("menu", "nav_menu", mn.ID, mn.Slug, "", hashJSON(mn), mn))
		}
	}
	for _, it := range items {
		if _, err := s.upsertItem(ctx, m.ID, it, m.DryRun); err != nil {
			return err
		}
	}
	if step == "products" {
		// Products skipped because the commerce module was missing are not
		// final: once an importer is registered, a re-run imports them.
		if _, ok := s.importer(); ok {
			if _, err := s.db.ExecContext(ctx, `UPDATE migration_items SET status = 'pending', error = '' WHERE migration_id = ?
				AND kind = 'product' AND status = 'skipped' AND error = 'needs_commerce_module'`, m.ID); err != nil {
				return err
			}
		}
	}
	s.logEntry(ctx, m.ID, "info", "", "step_items", map[string]any{"step": step, "items": len(items)})
	return nil
}

// unavailable logs data the source cannot provide; the step continues
// with no items. Other errors fail the step.
func (s *service) unavailable(ctx context.Context, m *migrationRow, step string, err error) error {
	if errors.Is(err, ErrAuthFailed) || errors.Is(err, ErrAuthUnavailable) {
		return err
	}
	if errors.Is(err, ErrUnavailable) || isForbidden(err) {
		s.logEntry(ctx, m.ID, "warn", "", "step_unavailable", map[string]any{"step": step, "reason": err.Error()})
		return nil
	}
	return err
}

func newItem(kind, wpType string, wpID int, slug, link, checksum string, payload any) itemRow {
	b, _ := json.Marshal(payload)
	return itemRow{Kind: kind, WPType: wpType, WPID: strconv.Itoa(wpID), WPSlug: decodeSlug(slug), WPURL: link,
		Checksum: shortHash(checksum), Payload: b}
}

func shortHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:12])
}

func hashJSON(v any) string {
	b, _ := json.Marshal(v)
	return shortHash(string(b))
}

// pageDepths returns each page's depth in its parent chain (0 = top).
func pageDepths(list []WPContent) map[int]int {
	parent := map[int]int{}
	for _, c := range list {
		if c.Type == "page" {
			parent[c.ID] = c.Parent
		}
	}
	return depths(parent)
}

func termDepths(terms []WPTerm) map[int]int {
	parent := map[int]int{}
	for _, t := range terms {
		parent[t.ID] = t.Parent
	}
	return depths(parent)
}

func depths(parent map[int]int) map[int]int {
	out := map[int]int{}
	for id := range parent {
		d, cur := 0, parent[id]
		for cur != 0 && d < 32 {
			if _, ok := parent[cur]; !ok {
				break
			}
			d++
			cur = parent[cur]
		}
		out[id] = d
	}
	return out
}
