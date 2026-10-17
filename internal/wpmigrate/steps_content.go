package wpmigrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/seo"
	"github.com/zadstack/gorun/internal/theme"
	"github.com/zadstack/gorun/internal/wpmigrate/gutenberg"
)

// importContent converts and saves one post/page. Re-runs update the item
// imported earlier (looked up by WordPress id, never by slug). Slug
// conflicts with content this source did not create follow the policy;
// duplicates inside the source (same slug under two parents) are always
// suffixed. Provenance (_wp) is stored on the migration item because
// content fields are schema-only.
func (s *service) importContent(ctx context.Context, m *migrationRow, it *itemRow) error {
	var c WPContent
	if err := json.Unmarshal(it.Payload, &c); err != nil {
		return err
	}
	// Serialize slug checks and creates so concurrent workers never race
	// two same-slug pages into the wrong conflict branch.
	s.contentMu.Lock()
	defer s.contentMu.Unlock()
	typ, ok := m.Mapping.localType(c.Type)
	if !ok {
		it.Status, it.Error = ItemSkipped, "type_skipped"
		return nil
	}
	slug := decodeSlug(c.Slug)
	if c.Type == "page" && m.Mapping.skipSlug(slug) {
		it.Status, it.Error = ItemSkipped, "plugin_page"
		return nil
	}
	if !m.DryRun {
		if err := s.ensureType(ctx, m, c.Type, typ); err != nil {
			return err
		}
	}
	conv := s.convertBody(c, m.Options)
	html, repaired := repairMarkdown(conv.HTML)
	conv.HTML = html
	locale := s.locale(ctx)

	var existing *content.Item
	if id, _, ok := s.mapped(ctx, m, "content", c.Type, c.ID); ok {
		if e, err := s.content.Get(ctx, id); err == nil {
			existing = e
		}
	}
	conflict := ""
	if other, err := s.content.GetBySlug(ctx, typ, slug, locale); err == nil && (existing == nil || other.ID != existing.ID) {
		switch {
		case s.ownedBySource(ctx, m, other.ID) || s.previewOwns(ctx, m, typ, slug, it.ID):
			conflict = "duplicate_in_source"
		case m.Options.SlugPolicy == SlugSkip:
			it.Status, it.Error = ItemSkipped, "slug_conflict"
			it.Would = map[string]any{"action": "skip", "type": typ, "slug": slug, "conflict": "existing"}
			return nil
		case m.Options.SlugPolicy == SlugOverwrite && existing == nil:
			existing, conflict = other, "overwrite"
		default:
			conflict = "existing"
		}
	} else if m.DryRun && s.previewOwns(ctx, m, typ, slug, it.ID) {
		conflict = "duplicate_in_source"
	}
	action := "create"
	if existing != nil {
		action = "update"
	}
	it.Meta = map[string]any{"_wp": wpProvenance(c), "unknown_blocks": conv.Unknown, "shortcodes": conv.Shortcodes, "markdown_repaired": repaired}
	if repaired {
		s.logEntry(ctx, m.ID, "info", it.ID, "markdown_repaired", map[string]any{"wp_id": c.ID})
	}
	for _, name := range conv.UnknownNames() {
		s.logEntry(ctx, m.ID, "warn", it.ID, "unknown_block", map[string]any{"block": name, "count": conv.Unknown[name], "wp_id": c.ID})
	}
	if len(conv.Shortcodes) > 0 {
		s.logEntry(ctx, m.ID, "info", it.ID, "shortcodes", map[string]any{"shortcodes": conv.Shortcodes, "wp_id": c.ID})
	}
	if m.DryRun {
		final := slug
		if conflict == "existing" || conflict == "duplicate_in_source" {
			final = s.predictSuffix(ctx, m, typ, slug, locale)
		}
		it.LocalURL = s.predictPath(ctx, m, c, typ, final)
		it.Would = map[string]any{"action": action, "type": typ, "slug": slug, "final_slug": final, "conflict": conflict,
			"shortcodes": conv.Shortcodes, "unknown_blocks": conv.Unknown, "markdown_repaired": repaired, "source_status": c.Status, "local_status": mapStatus(c.Status), "publish_at": contentPublishAt(c), "source_date": wpTime(c.Date)}
		return nil
	}
	in := s.contentInput(ctx, m, c, typ, slug, conv.HTML)
	var saved *content.Item
	var err error
	if existing != nil {
		merged := content.InputFromItem(existing)
		merged.Slug, merged.Title, merged.Excerpt, merged.Body = in.Slug, in.Title, in.Excerpt, in.Body
		merged.Status, merged.PublishAt, merged.ParentID, merged.MenuOrder = in.Status, in.PublishAt, in.ParentID, in.MenuOrder
		merged.AuthorID, merged.FeaturedMediaID, merged.SEO = in.AuthorID, in.FeaturedMediaID, in.SEO
		if merged.Terms == nil {
			merged.Terms = map[string][]string{}
		}
		for tax, slugs := range in.Terms {
			merged.Terms[tax] = slugs
		}
		if merged.Fields == nil {
			merged.Fields = map[string]any{}
		}
		for k, v := range in.Fields {
			merged.Fields[k] = v
		}
		saved, err = s.content.Update(ctx, existing.ID, merged)
	} else {
		saved, err = s.content.Create(ctx, in)
	}
	if err != nil {
		return describe(err)
	}
	it.LocalKind, it.LocalID = "content", saved.ID
	if _, err := s.db.ExecContext(ctx, `UPDATE migration_items SET local_kind = 'content', local_id = ? WHERE id = ?`, saved.ID, it.ID); err != nil {
		return err
	}
	if c.IsFront && s.settings != nil {
		if err := s.settings.Set(ctx, theme.SettingFront, saved.ID); err != nil {
			return err
		}
	}
	it.LocalURL = s.linker.Item(ctx, saved)
	it.Meta["slug_conflict"] = conflict
	it.Meta["local_slug"] = saved.Slug
	it.Would = map[string]any{"action": action, "type": typ, "slug": slug, "final_slug": saved.Slug, "conflict": conflict}
	return nil
}

// describe flattens validation errors into a readable message.
func describe(err error) error {
	var ve *content.ValidationError
	if errors.As(err, &ve) {
		keys := make([]string, 0, len(ve.Fields))
		for k, v := range ve.Fields {
			keys = append(keys, k+"="+v)
		}
		slices.Sort(keys)
		return fmt.Errorf("validation: %s", strings.Join(keys, ", "))
	}
	return err
}

func wpProvenance(c WPContent) map[string]any {
	p := map[string]any{"id": c.ID, "type": c.Type, "link": c.Link, "modified": c.Modified, "status": c.Status, "date": c.Date}
	if c.Template != "" {
		p["template"] = c.Template
	}
	if gutenberg.HasBlocks(c.Raw) {
		p["raw_blocks"] = c.Raw
	}
	return p
}

// convertBody prefers raw block markup, then rendered HTML, then classic
// raw content (paragraphs from blank lines).
func (s *service) convertBody(c WPContent, o Options) gutenberg.Result {
	opts := gutenberg.Options{KeepClasses: o.KeepClasses}
	switch {
	case gutenberg.HasBlocks(c.Raw):
		return gutenberg.ConvertRaw(c.Raw, opts)
	case c.Rendered != "":
		return gutenberg.ConvertRaw(c.Rendered, opts)
	default:
		return gutenberg.ConvertRaw(gutenberg.Autop(c.Raw), opts)
	}
}

func (s *service) contentInput(ctx context.Context, m *migrationRow, c WPContent, typ, slug, body string) content.Input {
	order := c.MenuOrder
	in := content.Input{Type: typ, Slug: slug, Title: firstNonEmpty(c.Title, slug), Excerpt: c.Excerpt, Body: body,
		Status: mapStatus(c.Status), MenuOrder: &order, Terms: map[string][]string{}, Fields: map[string]any{}}
	in.PublishAt = contentPublishAt(c)
	if c.Parent != 0 {
		in.ParentID, _, _ = s.mapped(ctx, m, "content", c.Type, c.Parent)
	}
	if id, _, ok := s.mapped(ctx, m, "user", "user", c.Author); ok {
		in.AuthorID = id
	} else {
		in.AuthorID = m.CreatedBy
	}
	if id, _, ok := s.mapped(ctx, m, "media", "attachment", c.FeaturedMedia); ok {
		in.FeaturedMediaID = id
	}
	meta := yoastToSEO(c.Yoast)
	if meta.Canonical != "" && s.siteHosts(ctx, m)[hostOf(meta.Canonical)] {
		// The old permalink: GoRun emits the item's own URL instead.
		meta.Canonical = ""
	}
	in.SEO = meta.ToMap()
	t, _ := s.content.GetType(ctx, typ)
	for wpTax, ids := range c.Terms {
		local := m.Mapping.Taxonomies[wpTax]
		if local == "" || t == nil || !slices.Contains(t.Taxonomies, local) {
			continue
		}
		for _, id := range ids {
			if termID, _, ok := s.mapped(ctx, m, "term", wpTax, id); ok {
				if term, err := s.tax.GetTerm(ctx, termID); err == nil {
					in.Terms[local] = append(in.Terms[local], term.Slug)
				}
			}
		}
	}
	for key, fm := range m.Mapping.Fields[c.Type] {
		if fm.Target == "" {
			continue
		}
		var v any
		var ok bool
		if k, isACF := strings.CutPrefix(key, "acf:"); isACF {
			v, ok = c.ACF[k]
		} else {
			v, ok = c.Meta[key]
		}
		if ok {
			in.Fields[fm.Target] = v
		}
	}
	return in
}

func contentPublishAt(c WPContent) string {
	status := mapStatus(c.Status)
	if status == "published" || status == "scheduled" || status == "private" {
		return wpTime(c.Date)
	}
	return ""
}

// mapStatus maps WordPress post statuses.
func mapStatus(st string) content.Status {
	switch st {
	case "publish", "inherit":
		return "published"
	case "future":
		return "scheduled"
	case "private":
		return "private"
	case "pending":
		return "pending"
	default:
		return "draft"
	}
}

// wpTime normalizes WordPress GMT dates ("2026-08-31T10:00:00" or
// "2026-08-31 10:00:00") to RFC3339 UTC; "" when invalid.
func wpTime(s string) string {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil && t.Year() > 1970 {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return ""
}

// yoastToSEO maps yoast_head_json to seo.Meta; robots noindex becomes
// "noindex,follow" (or the given follow value).
func yoastToSEO(y map[string]any) seo.Meta {
	if len(y) == 0 {
		return seo.Meta{}
	}
	str := func(k string) string { v, _ := y[k].(string); return strings.TrimSpace(v) }
	m := seo.Meta{Title: str("title"), Description: str("description"), Canonical: str("canonical"),
		OGTitle: str("og_title"), OGDescription: str("og_description"), TwitterCard: str("twitter_card")}
	if imgs, ok := y["og_image"].([]any); ok && len(imgs) > 0 {
		if img, ok := imgs[0].(map[string]any); ok {
			m.OGImage, _ = img["url"].(string)
		}
	}
	if r, ok := y["robots"].(map[string]any); ok {
		index, _ := r["index"].(string)
		follow, _ := r["follow"].(string)
		if index == "noindex" {
			if follow == "" {
				follow = "follow"
			}
			m.Robots = "noindex," + follow
		} else if follow == "nofollow" {
			m.Robots = "index,nofollow"
		}
	}
	if sc, ok := y["schema"]; ok && sc != nil {
		if b, err := json.Marshal(sc); err == nil {
			m.Schema = b
		}
	}
	return m
}

// ensureType creates the GoRun type for a WordPress type mapped to
// "create".
func (s *service) ensureType(ctx context.Context, m *migrationRow, wpType, typ string) error {
	if m.Mapping.Types[wpType].Target != TargetCreate {
		return s.ensureFields(ctx, m, wpType, typ)
	}
	if _, err := s.content.GetType(ctx, typ); err == nil {
		return s.ensureFields(ctx, m, wpType, typ)
	} else if !errors.Is(err, content.ErrNotFound) {
		return err
	}
	name := firstNonEmpty(m.Mapping.Types[wpType].Name, wpType)
	t := content.Type{Slug: typ, Name: map[string]string{"en": name, "ar": name}, Singular: map[string]string{"en": name, "ar": name},
		Plural: map[string]string{"en": name, "ar": name}, Icon: "file-text", Public: true, HasArchive: true,
		Supports: []string{"title", "body", "excerpt", "featured_image", "author", "revisions", "seo"}, Fields: []content.Field{}}
	if err := s.content.SaveType(ctx, t); err != nil {
		return describe(err)
	}
	s.logEntry(ctx, m.ID, "info", "", "type_created", map[string]any{"type": typ})
	return s.ensureFields(ctx, m, wpType, typ)
}

// ensureFields adds mapped meta fields missing from the type.
func (s *service) ensureFields(ctx context.Context, m *migrationRow, wpType, typ string) error {
	fields := m.Mapping.Fields[wpType]
	if len(fields) == 0 {
		return nil
	}
	t, err := s.content.GetType(ctx, typ)
	if err != nil {
		return err
	}
	changed := false
	for _, key := range sortedKeys(fields) {
		fm := fields[key]
		if fm.Target == "" || slices.ContainsFunc(t.Fields, func(f content.Field) bool { return f.Key == fm.Target }) {
			continue
		}
		ft := content.FieldType(firstNonEmpty(fm.Type, "text"))
		t.Fields = append(t.Fields, content.Field{Key: fm.Target, Type: ft, Label: map[string]string{"en": key, "ar": key}})
		changed = true
	}
	if !changed {
		return nil
	}
	return describe(s.content.SaveType(ctx, *t))
}

// ownedBySource reports whether a content item was created by an import
// of this migration's source.
func (s *service) ownedBySource(ctx context.Context, m *migrationRow, contentID string) bool {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM migration_items i JOIN migrations g ON g.id = i.migration_id
		WHERE i.kind = 'content' AND i.local_id = ? AND (i.status = 'done' OR i.migration_id = ?) AND g.source_id = ? LIMIT 1`,
		contentID, m.ID, m.SourceID).Scan(&one)
	return err == nil
}

// previewOwns reports whether another item of this dry run already claims
// slug (two WordPress pages with the same slug under different parents).
func (s *service) previewOwns(ctx context.Context, m *migrationRow, typ, slug, itemID string) bool {
	if !m.DryRun {
		return false
	}
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT count(*) FROM migration_items WHERE migration_id = ? AND kind = 'content'
		AND status = 'preview' AND id != ? AND json_extract(would_json, '$.type') = ? AND json_extract(would_json, '$.slug') = ?`,
		m.ID, itemID, typ, slug).Scan(&n)
	return n > 0
}

// predictSuffix guesses the slug content would assign (slug-2, slug-3…).
func (s *service) predictSuffix(ctx context.Context, m *migrationRow, typ, slug, locale string) string {
	for i := 2; i < 1000; i++ {
		cand := slug + "-" + strconv.Itoa(i)
		if _, err := s.content.GetBySlug(ctx, typ, cand, locale); err == nil {
			continue
		}
		var n int
		_ = s.db.QueryRowContext(ctx, `SELECT count(*) FROM migration_items WHERE migration_id = ? AND kind = 'content'
			AND status = 'preview' AND json_extract(would_json, '$.final_slug') = ? AND json_extract(would_json, '$.type') = ?`,
			m.ID, cand, typ).Scan(&n)
		if n == 0 {
			return cand
		}
	}
	return slug
}

// predictPath is the permalink a dry-run item would get.
func (s *service) predictPath(ctx context.Context, m *migrationRow, c WPContent, typ, slug string) string {
	if c.IsFront {
		return "/"
	}
	if typ != "page" {
		return "/" + typ + "/" + slug
	}
	if c.Parent != 0 {
		var parent string
		_ = s.db.QueryRowContext(ctx, `SELECT local_url FROM migration_items WHERE migration_id = ? AND kind = 'content'
			AND wp_type = ? AND wp_id = ?`, m.ID, c.Type, strconv.Itoa(c.Parent)).Scan(&parent)
		if parent != "" && parent != "/" {
			return parent + "/" + slug
		}
	}
	return "/" + slug
}
