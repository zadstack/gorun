package wpmigrate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/media"
)

// maxMediaDownload caps one downloaded file.
const maxMediaDownload = 200 << 20

// importMedia downloads an attachment (following CDN redirects across
// hosts) into the media library, deduplicated by source URL; alt, caption
// and title are kept. Size variants (-300x200) are resolved from the
// payload's size list during the rewrite pass.
func (s *service) importMedia(ctx context.Context, m *migrationRow, it *itemRow) error {
	var wm WPMedia
	if err := json.Unmarshal(it.Payload, &wm); err != nil {
		return err
	}
	if wm.URL == "" {
		it.Status, it.Error = ItemSkipped, "no_url"
		return nil
	}
	it.LocalKind = "media"
	// it.LocalID set means this item was imported before and is pending
	// again because WordPress changed it (new modified date or URL).
	changed := it.LocalID != ""
	if !changed {
		if id, _, ok := s.mapped(ctx, m, "media", "attachment", wm.ID); ok {
			if mm, err := s.media.Get(ctx, id); err == nil {
				it.LocalID, it.LocalURL = mm.ID, mm.URL
				it.Would = map[string]any{"action": "reuse"}
				return nil
			}
		}
		if mm, err := s.media.FindBySourceURL(ctx, wm.URL); err == nil {
			it.LocalID, it.LocalURL = mm.ID, mm.URL
			it.Would = map[string]any{"action": "reuse"}
			return nil
		}
	}
	if m.DryRun {
		size := wm.Bytes
		if size == 0 {
			size = s.headSize(ctx, m, wm.URL)
		}
		it.Would = map[string]any{"action": "download", "url": wm.URL, "bytes": size}
		return nil
	}
	data, err := s.download(ctx, m, wm.URL)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	checksum := hex.EncodeToString(sum[:])
	// The previous version: the media this item produced before (when it
	// changed), else the current row for this URL (newest revision).
	var old *media.Media
	if changed {
		if prev, err := s.media.Get(ctx, it.LocalID); err == nil {
			old = prev
		}
	}
	current, curErr := s.media.FindBySourceURL(ctx, wm.URL)
	if old == nil && curErr == nil {
		old = current
	}
	if old != nil && old.Checksum == checksum {
		it.LocalID, it.LocalURL = old.ID, old.URL
		it.Would = map[string]any{"action": "reuse"}
		return nil
	}
	sourceURL := wm.URL
	if curErr == nil {
		// source_url is unique; a new version of a known URL gets a
		// revision marker (lookups of the plain URL find the newest).
		sourceURL = wm.URL + media.RevisionMarker + checksum[:12]
	}
	var mm *media.Media
	if rev, err := s.media.FindBySourceURL(ctx, sourceURL); err == nil && sourceURL != wm.URL && rev.Checksum == checksum {
		// These exact bytes were imported before and that revision was
		// kept (A → B → A): reuse it and make it the current one again.
		mm = rev
		if mc, ok := s.media.(interface {
			MarkCurrent(ctx context.Context, id string) error
		}); ok {
			if err := mc.MarkCurrent(ctx, rev.ID); err != nil {
				return err
			}
		}
	} else {
		// Upload first: a failed upload leaves the old media untouched.
		up, err := s.media.Upload(ctx, fileNameOf(wm.URL), bytes.NewReader(data), media.UploadMeta{
			Alt: wm.Alt, Caption: wm.Caption, Title: wm.Title, SourceURL: sourceURL, AuthorID: m.CreatedBy})
		if err != nil {
			return err
		}
		mm = up
	}
	it.LocalID, it.LocalURL = mm.ID, mm.URL
	it.Would = map[string]any{"action": "download", "bytes": mm.Size}
	if old != nil {
		s.replaceMediaRefs(ctx, m, it, old, mm)
	}
	return nil
}

// replaceMediaRefs points every reference this source owns from old to
// new (featured image, body and SEO URLs of its imported content; product
// images through the importer) and deletes old only when nothing else
// can still reference it.
func (s *service) replaceMediaRefs(ctx context.Context, m *migrationRow, it *itemRow, old, mm *media.Media) {
	keep := func(reason string, meta map[string]any) {
		if meta == nil {
			meta = map[string]any{}
		}
		meta["old_media"], meta["new_media"], meta["reason"] = old.ID, mm.ID, reason
		s.logEntry(ctx, m.ID, "warn", it.ID, "media_old_kept", meta)
	}
	owned := map[string]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT i.local_id FROM migration_items i JOIN migrations g ON g.id = i.migration_id
		WHERE i.kind = 'content' AND i.local_id != '' AND g.source_id = ?`, m.SourceID)
	if err != nil {
		keep("lookup_failed", map[string]any{"error": err.Error()})
		return
	}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			owned[id] = true
		}
	}
	rows.Close()
	// Read-only reference scan: content the migrator did not import keeps
	// the old file alive.
	// The URL stem (storage key without extension) also covers variants
	// (<stem>-medium.png …).
	stem := strings.TrimSuffix(old.URL, path.Ext(old.URL))
	refs, err := s.db.QueryContext(ctx, `SELECT id FROM content WHERE featured_media_id = ? OR instr(body, ?) > 0 OR instr(seo_json, ?) > 0
		OR instr(fields_json, ?) > 0 OR instr(fields_json, ?) > 0`,
		old.ID, stem, stem, old.ID, stem)
	if err != nil {
		keep("lookup_failed", map[string]any{"error": err.Error()})
		return
	}
	var ids []string
	foreign := 0
	for refs.Next() {
		var id string
		if refs.Scan(&id) == nil {
			if owned[id] {
				ids = append(ids, id)
			} else {
				foreign++
			}
		}
	}
	refs.Close()
	urls := map[string]string{old.URL: mm.URL}
	for name, v := range old.Variants {
		if v.URL != "" {
			urls[v.URL] = s.media.URL(mm, name)
		}
	}
	for _, id := range ids {
		if err := s.repointContent(ctx, id, old.ID, mm.ID, urls); err != nil {
			keep("content_update_failed", map[string]any{"content_id": id, "error": err.Error()})
			return
		}
	}
	if imp, ok := s.importer(); ok {
		// With a replacer, always re-point: hand-made products may use the
		// imported image too. Without one, keep the old media whenever this
		// source created products.
		if r, ok := imp.(ProductMediaReplacer); ok {
			if err := r.ReplaceMediaRef(ctx, old.ID, mm.ID); err != nil {
				keep("product_update_failed", map[string]any{"error": err.Error()})
				return
			}
		} else if s.hasDoneProducts(ctx, m) {
			keep("product_images_unknown", nil)
			return
		}
	}
	if foreign > 0 {
		keep("referenced_elsewhere", map[string]any{"content_items": foreign})
		return
	}
	if err := s.media.Delete(ctx, old.ID); err != nil {
		keep("delete_failed", map[string]any{"error": err.Error()})
		return
	}
	s.logEntry(ctx, m.ID, "info", it.ID, "media_updated", map[string]any{"old_media": old.ID, "new_media": mm.ID, "content_items": len(ids)})
}

// repointContent swaps media references inside one content item.
func (s *service) repointContent(ctx context.Context, id, oldID, newID string, urls map[string]string) error {
	item, err := s.content.Get(ctx, id)
	if err != nil {
		return err
	}
	in := content.InputFromItem(item)
	if in.FeaturedMediaID == oldID {
		in.FeaturedMediaID = newID
	}
	pairs := make([]string, 0, 2*len(urls))
	for o, n := range urls {
		pairs = append(pairs, o, n)
	}
	rep := strings.NewReplacer(pairs...)
	in.Body = rep.Replace(in.Body)
	if len(in.SEO) > 0 {
		b, _ := json.Marshal(in.SEO)
		var seoMap map[string]any
		if json.Unmarshal([]byte(rep.Replace(string(b))), &seoMap) == nil {
			in.SEO = seoMap
		}
	}
	if len(in.Fields) > 0 {
		if f, ok := repointValue(in.Fields, oldID, newID, rep).(map[string]any); ok {
			in.Fields = f
		}
	}
	_, err = s.content.Update(ctx, id, in)
	return describe(err)
}

// repointValue swaps media references inside custom field values: a string
// equal to oldID becomes newID (image/file fields store media ids, also in
// lists and repeater rows) and old file URLs become the new ones.
func repointValue(v any, oldID, newID string, rep *strings.Replacer) any {
	switch x := v.(type) {
	case string:
		if x == oldID {
			return newID
		}
		return rep.Replace(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = repointValue(val, oldID, newID, rep)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = repointValue(val, oldID, newID, rep)
		}
		return out
	}
	return v
}

// hasDoneProducts reports whether this source ever created catalog
// products (any product item with a local id, whatever its latest status),
// whose images may point at the replaced media.
func (s *service) hasDoneProducts(ctx context.Context, m *migrationRow) bool {
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT count(*) FROM migration_items i JOIN migrations g ON g.id = i.migration_id
		WHERE i.kind = 'product' AND i.local_id != '' AND g.source_id = ?`, m.SourceID).Scan(&n)
	return n > 0
}

// download fetches a file (cross-host redirects allowed), capped, after
// waiting for the source's rate limiter.
func (s *service) download(ctx context.Context, m *migrationRow, raw string) ([]byte, error) {
	if err := s.limiter(m.SourceID, s.rate(m.Options)).wait(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "GoRun migrator")
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxMediaDownload {
		return nil, fmt.Errorf("download: %d bytes exceeds the limit", resp.ContentLength)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMediaDownload+1))
	if err != nil {
		return nil, fmt.Errorf("download: %v", err)
	}
	if len(data) > maxMediaDownload {
		return nil, fmt.Errorf("download: file exceeds the limit")
	}
	return data, nil
}

// fileNameOf keeps the original (decoded) file name.
func fileNameOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "file"
	}
	name := path.Base(u.Path)
	if d, err := url.PathUnescape(name); err == nil {
		name = d
	}
	if name == "" || name == "." || name == "/" {
		return "file"
	}
	return name
}

// headSize returns Content-Length from a HEAD request (0 when unknown),
// rate-limited like download.
func (s *service) headSize(ctx context.Context, m *migrationRow, raw string) int64 {
	if s.limiter(m.SourceID, s.rate(m.Options)).wait(ctx) != nil {
		return 0
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, raw, nil)
	if err != nil {
		return 0
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return 0
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.ContentLength < 0 {
		return 0
	}
	return resp.ContentLength
}

// mediaKey normalizes a media URL for lookups (scheme-less, decoded).
func mediaKey(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}
	return strings.TrimPrefix(strings.ToLower(u.Host), "www.") + u.Path
}

func mediaExactKey(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}
	return (&url.URL{Host: strings.TrimPrefix(strings.ToLower(u.Host), "www."), Path: u.Path, RawPath: mediaIdentityPath(u), RawQuery: u.RawQuery, ForceQuery: u.ForceQuery}).String()
}

func mediaPathKey(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}
	return strings.TrimPrefix(strings.ToLower(u.Host), "www.") + mediaIdentityPath(u)
}

// Origins can distinguish encoded delimiters and their hex spelling. Decode
// Unicode/unreserved escapes for compatibility without decoding these bytes.
func mediaIdentityPath(u *url.URL) string {
	escaped := u.EscapedPath()
	var normalized strings.Builder
	start := 0
	normalize := func(chunk string) string {
		decoded, err := url.PathUnescape(chunk)
		if err != nil {
			return chunk
		}
		return (&url.URL{Path: decoded}).EscapedPath()
	}
	for i := 0; i+2 < len(escaped); i++ {
		if escaped[i] != '%' {
			continue
		}
		decoded, err := hex.DecodeString(escaped[i+1 : i+3])
		if err != nil || !strings.ContainsRune(":/?#[]@!$&'()*+,;=%\\", rune(decoded[0])) {
			i += 2
			continue
		}
		normalized.WriteString(normalize(escaped[start:i]))
		normalized.WriteString(escaped[i : i+3])
		i += 2
		start = i + 1
	}
	normalized.WriteString(normalize(escaped[start:]))
	return normalized.String()
}
