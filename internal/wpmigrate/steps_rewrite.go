package wpmigrate

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/media"
)

// generateRewrites adds one rewrite item per imported content item; the
// checksum follows the content item's, so unchanged items are not
// rewritten again on re-runs.
func (s *service) generateRewrites(ctx context.Context, m *migrationRow) error {
	list, err := s.items(ctx, m.ID, "content", ItemDone)
	if err != nil {
		return err
	}
	// The checksum also covers the media and content maps, so a retry that
	// imports more media or pages rewrites every item again.
	var mediaIDs, contentDone string
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE((SELECT group_concat(local_id, ',') FROM (SELECT local_id FROM migration_items
		WHERE migration_id = ? AND kind = 'media' AND status = 'done' ORDER BY local_id)), ''),
		(SELECT count(*) FROM migration_items WHERE migration_id = ? AND kind = 'content' AND status = 'done')`, m.ID, m.ID).Scan(&mediaIDs, &contentDone)
	fp := shortHash(mediaIDs) + "/" + contentDone
	for _, it := range list {
		if it.LocalID == "" {
			continue
		}
		b, _ := json.Marshal(map[string]string{"content_id": it.LocalID})
		row := itemRow{Kind: "rewrite", WPType: it.WPType, WPID: it.WPID, WPSlug: it.WPSlug, WPURL: it.WPURL,
			Checksum: shortHash(it.Checksum + it.LocalID + fp), Payload: b}
		if _, err := s.upsertItem(ctx, m.ID, row, false); err != nil {
			return err
		}
	}
	s.logEntry(ctx, m.ID, "info", "", "step_items", map[string]any{"step": "rewrite", "items": len(list)})
	return nil
}

// rewriteItem replaces WordPress upload URLs (any size variant, srcset
// included) with local media URLs and same-site links with local paths, in
// the body and in the SEO og_image / schema.
func (s *service) rewriteItem(ctx context.Context, m *migrationRow, it *itemRow) error {
	var p struct {
		ContentID string `json:"content_id"`
	}
	if err := json.Unmarshal(it.Payload, &p); err != nil {
		return err
	}
	item, err := s.content.Get(ctx, p.ContentID)
	if errors.Is(err, content.ErrNotFound) {
		it.Status, it.Error = ItemSkipped, "content_missing"
		return nil
	}
	if err != nil {
		return err
	}
	rw, err := s.runRewriter(ctx, m)
	if err != nil {
		return err
	}
	body, stats := rw.rewrite(ctx, item.Body)
	seoMap, seoChanged := rw.rewriteSEO(ctx, item.SEO, &stats)
	it.LocalKind, it.LocalID, it.LocalURL = "content", item.ID, ""
	it.Meta = map[string]any{"media": stats.media, "links": stats.links, "unresolved_media": stats.unresolved}
	s.logEntry(ctx, m.ID, "info", it.ID, "rewrite", map[string]any{"content_id": item.ID, "media": stats.media,
		"links": stats.links, "unresolved_media": stats.unresolved})
	if body == item.Body && !seoChanged {
		return nil
	}
	in := content.InputFromItem(item)
	in.Body, in.SEO = body, seoMap
	if _, err := s.content.Update(ctx, item.ID, in); err != nil {
		return describe(err)
	}
	return nil
}

type rewriteStats struct{ media, links, unresolved int }

// rewriter holds the lookup tables of one migration run (built once per
// run and shared by the rewrite items).
type rewriter struct {
	s          *service
	base       *url.URL
	hosts      map[string]bool
	urlRe      *regexp.Regexp    // a site URL followed by a terminator (attribute values, JSON)
	textRe     *regexp.Regexp    // a site URL that is a whole token of plain text
	media      map[string]string // mediaExactKey(url) → local media id (originals and size variants)
	mediaPaths map[string]string // empty ID means ambiguous across known source URLs
	links      map[string]string // decoded WP path → local path

	mu       sync.Mutex
	byID     map[string]*media.Media
	bySource map[string]*media.Media // source reference → prior import; nil caches a miss
}

var (
	htmlTagRe = regexp.MustCompile(`<[^>]*>`)
	urlAttrRe = regexp.MustCompile(`(?i)(\s(?:href|src|srcset|poster|data-src)\s*=\s*)("[^"]*"|'[^']*')`)
)

// runRewriter returns the rewriter of m's current run, building it once.
func (s *service) runRewriter(ctx context.Context, m *migrationRow) (*rewriter, error) {
	key := m.ID + ":" + strconv.Itoa(m.Cursor)
	if v, ok := s.rwCache.Load(key); ok {
		return v.(*rewriter), nil
	}
	rw, err := s.newRewriter(ctx, m)
	if err != nil {
		return nil, err
	}
	v, _ := s.rwCache.LoadOrStore(key, rw)
	return v.(*rewriter), nil
}

// dropRewriters forgets the cached rewriters of a migration.
func (s *service) dropRewriters(id string) {
	s.rwCache.Range(func(k, _ any) bool {
		if strings.HasPrefix(k.(string), id+":") {
			s.rwCache.Delete(k)
		}
		return true
	})
}

// compileHosts builds the URL patterns for hosts. The host must be followed
// by a port, a path/query/fragment, or a terminator, so look-alike hosts
// (example.org.evil.net, example.organic) never match.
func (rw *rewriter) compileHosts() {
	var alts []string
	for h := range rw.hosts {
		if h != "" {
			alts = append(alts, regexp.QuoteMeta(h))
		}
	}
	if len(alts) == 0 {
		return
	}
	sort.Strings(alts)
	host := `(?:www\.)?(?:` + strings.Join(alts, "|") + `)(?::\d+)?(?:[/?#][^\s"'<>)]*)?`
	rw.urlRe = regexp.MustCompile(`(?i)((?:https?:)?//` + host + `)([\s"'<>),]|$)`)
	rw.textRe = regexp.MustCompile(`(?i)(^|[\s(])(https?://` + host + `)(\.?(?:[\s<),;!?]|$))`)
}

func (s *service) newRewriter(ctx context.Context, m *migrationRow) (*rewriter, error) {
	rw := &rewriter{s: s, hosts: s.siteHosts(ctx, m), media: map[string]string{}, mediaPaths: map[string]string{}, links: map[string]string{}, byID: map[string]*media.Media{}, bySource: map[string]*media.Media{}}
	rw.compileHosts()
	if src, err := s.getSource(ctx, m.SourceID); err == nil {
		rw.base, _ = url.Parse(s.sourceDocumentURL(src))
		if rw.base != nil && !strings.HasSuffix(rw.base.Path, "/") {
			rw.base.Path += "/"
		}
	}
	mediaItems, err := s.items(ctx, m.ID, "media", ItemDone)
	if err != nil {
		return nil, err
	}
	mediaSources := []string{}
	for _, it := range mediaItems {
		var wm WPMedia
		if json.Unmarshal(it.Payload, &wm) != nil || it.LocalID == "" {
			continue
		}
		rw.media[mediaExactKey(wm.URL)] = it.LocalID
		rw.addMediaPath(wm.URL, it.LocalID)
		mediaSources = append(mediaSources, wm.URL)
		for _, sz := range wm.Sizes {
			rw.media[mediaExactKey(sz)] = it.LocalID
			rw.addMediaPath(sz, it.LocalID)
			mediaSources = append(mediaSources, sz)
		}
	}
	contentItems, err := s.items(ctx, m.ID, "content", ItemDone)
	if err != nil {
		return nil, err
	}
	for _, it := range contentItems {
		if it.WPURL != "" && it.LocalURL != "" {
			rw.links[pathOf(it.WPURL)] = it.LocalURL
		}
		if it.LocalID == "" {
			continue
		}
		item, err := s.content.Get(ctx, it.LocalID)
		if errors.Is(err, content.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		refs := inlineMediaReferences(item.Body)
		seo, err := json.Marshal(item.SEO)
		if err != nil {
			return nil, err
		}
		refs = append(refs, mediaTokenRe.FindAllString(string(seo), -1)...)
		for _, raw := range refs {
			u, err := url.Parse(raw)
			if err != nil || (u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https") {
				continue
			}
			if u.Host == "" && rw.base != nil {
				u = rw.base.ResolveReference(u)
			}
			for _, candidate := range mediaCandidates(u) {
				mediaSources = append(mediaSources, candidate.String())
			}
		}
	}
	if err := rw.indexPriorMediaPaths(ctx, mediaSources); err != nil {
		return nil, err
	}
	return rw, nil
}

func (rw *rewriter) addMediaPath(raw, id string) {
	key := mediaPathKey(raw)
	if previous, exists := rw.mediaPaths[key]; exists && previous != id {
		rw.mediaPaths[key] = ""
		return
	}
	rw.mediaPaths[key] = id
}

func (rw *rewriter) indexPriorMediaPaths(ctx context.Context, sources []string) error {
	prefixes := map[string]bool{}
	wanted := map[string]bool{}
	for _, raw := range sources {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			continue
		}
		wanted[mediaPathKey(raw)] = true
		u.RawQuery, u.Fragment, u.RawFragment, u.ForceQuery = "", "", "", false
		host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
		for _, scheme := range []string{"https", "http"} {
			for _, alias := range []string{host, "www." + host} {
				candidate := *u
				candidate.Scheme, candidate.Host = scheme, alias
				for _, ref := range mediaSourceReferences(&candidate) {
					prefixes[ref] = true
				}
				candidate.RawPath = mediaIdentityPath(&candidate)
				for _, ref := range mediaSourceReferences(&candidate) {
					prefixes[ref] = true
				}
			}
		}
	}
	// Query only known source paths, in bounded batches. A run without source
	// media references does not query native media rows for its rewrite.
	ordered := sortedKeys(prefixes)
	seen := map[string]bool{}
	for start := 0; start < len(ordered); start += 64 {
		where, args := []string{}, []any{}
		for _, prefix := range ordered[start:min(start+64, len(ordered))] {
			escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix)
			where = append(where, `(source_url=? OR source_url LIKE ? ESCAPE '\' OR source_url LIKE ? ESCAPE '\')`)
			args = append(args, prefix, escaped+"?%", escaped+media.RevisionMarker+"%")
		}
		rows, err := rw.s.db.QueryContext(ctx, `SELECT id,source_url FROM media WHERE `+strings.Join(where, " OR ")+` ORDER BY CASE WHEN current_at<>'' THEN current_at ELSE id END DESC`, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, raw string
			if err := rows.Scan(&id, &raw); err != nil {
				rows.Close()
				return err
			}
			base, _, _ := strings.Cut(raw, media.RevisionMarker)
			if seen[base] {
				continue
			}
			seen[base] = true
			if wanted[mediaPathKey(base)] {
				rw.addMediaPath(base, id)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

var scaledSuffix = regexp.MustCompile(`-scaled(\.[A-Za-z0-9]+)$`)

var sizeSuffix = regexp.MustCompile(`-(\d+)x(\d+)(\.[A-Za-z0-9]+)$`)

// rewrite rewrites URLs inside href/src/srcset/poster/data-src attribute
// values and URLs that are whole tokens of text; other text is untouched.
func (rw *rewriter) rewrite(ctx context.Context, body string) (string, rewriteStats) {
	var st rewriteStats
	if body == "" {
		return body, st
	}
	var b strings.Builder
	last := 0
	for _, loc := range htmlTagRe.FindAllStringIndex(body, -1) {
		b.WriteString(rw.rewriteText(ctx, body[last:loc[0]], &st))
		b.WriteString(rw.rewriteTag(ctx, body[loc[0]:loc[1]], &st))
		last = loc[1]
	}
	b.WriteString(rw.rewriteText(ctx, body[last:], &st))
	return b.String(), st
}

func (rw *rewriter) rewriteTag(ctx context.Context, tag string, st *rewriteStats) string {
	return urlAttrRe.ReplaceAllStringFunc(tag, func(m string) string {
		sm := urlAttrRe.FindStringSubmatch(m)
		q, val := sm[2], sm[2][1:len(sm[2])-1]
		if strings.Contains(strings.ToLower(sm[1]), "srcset") {
			for _, span := range reverseSrcsetSpans(val) {
				val = val[:span[0]] + rw.rewriteImage(ctx, val[span[0]:span[1]], st) + val[span[1]:]
			}
		} else if !strings.Contains(strings.ToLower(sm[1]), "href") {
			val = rw.rewriteImage(ctx, val, st)
		} else {
			val = rw.rewriteLeading(ctx, val, st)
		}
		return sm[1] + q[:1] + val + q[:1]
	})
}

// rewriteLeading resolves the leading source attachment first, then a site URL.
// URLs nested inside an unrelated link are left alone.
func (rw *rewriter) rewriteLeading(ctx context.Context, v string, st *rewriteStats) string {
	if strings.TrimSpace(v) == "" {
		return v
	}
	start := len(v) - len(strings.TrimLeft(v, " \t\r\n"))
	end := start + len(strings.Fields(v[start:])[0])
	if local := rw.mappedMedia(ctx, html.UnescapeString(v[start:end])); local != "" {
		st.media++
		return v[:start] + local + v[end:]
	}
	if rw.urlRe == nil {
		return v
	}
	loc := rw.urlRe.FindStringSubmatchIndex(v)
	if loc == nil || strings.TrimSpace(v[:loc[0]]) != "" {
		return v
	}
	return v[:loc[2]] + rw.replace(ctx, v[loc[2]:loc[3]], st, true) + v[loc[3]:]
}

// rewriteValue rewrites every site URL in v (links too when links is set).
func (rw *rewriter) rewriteValue(ctx context.Context, v string, st *rewriteStats, links bool) string {
	return rw.urlRe.ReplaceAllStringFunc(v, func(m string) string {
		sm := rw.urlRe.FindStringSubmatch(m)
		return rw.replace(ctx, sm[1], st, links) + sm[2]
	})
}

// rewriteText rewrites site URLs that stand alone in text. The search
// resumes right after each URL so its terminator can start the next match.
func (rw *rewriter) rewriteText(ctx context.Context, text string, st *rewriteStats) string {
	if rw.textRe == nil || text == "" {
		return text
	}
	var b strings.Builder
	pos := 0
	for pos <= len(text) {
		loc := rw.textRe.FindStringSubmatchIndex(text[pos:])
		if loc == nil {
			break
		}
		urlStart, urlEnd := pos+loc[4], pos+loc[5]
		b.WriteString(text[pos:urlStart])
		b.WriteString(rw.replace(ctx, text[urlStart:urlEnd], st, true))
		pos = urlEnd
	}
	b.WriteString(text[pos:])
	return b.String()
}

// rewriteUploads resolves only upload URLs in s (og_image, schema JSON).
func (rw *rewriter) rewriteUploads(ctx context.Context, s string, st *rewriteStats) string {
	if s == "" {
		return s
	}
	return mediaTokenRe.ReplaceAllStringFunc(s, func(raw string) string {
		if local := rw.mappedMedia(ctx, html.UnescapeString(raw)); local != "" {
			st.media++
			return local
		}
		if rw.urlRe != nil {
			return rw.rewriteValue(ctx, raw, st, false)
		}
		return raw
	})
}

// rewriteSEO resolves upload URLs in og_image and schema, maps the
// canonical URL to the local permalink and every site URL in the schema to
// this site (see rewriteSchemaIDs).
func (rw *rewriter) rewriteSEO(ctx context.Context, m map[string]any, st *rewriteStats) (map[string]any, bool) {
	if len(m) == 0 {
		return m, false
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	changed := false
	if og, ok := m["og_image"].(string); ok {
		if nv := rw.rewriteUploads(ctx, og, st); nv != og {
			out["og_image"], changed = nv, true
		}
	}
	if c, ok := m["canonical"].(string); ok && rw.urlRe != nil {
		if nv := rw.localAbsolute(ctx, c, st); nv != c {
			out["canonical"], changed = nv, true
		}
	}
	if sc, ok := m["schema"]; ok && sc != nil {
		if b, err := json.Marshal(sc); err == nil {
			nv := rw.rewriteUploads(ctx, string(b), st)
			var v any
			if json.Unmarshal([]byte(nv), &v) == nil {
				v = rw.rewriteSchemaIDs(ctx, v, st)
				if nb, err := json.Marshal(v); err == nil && string(nb) != string(b) {
					out["schema"], changed = v, true
				}
			}
		}
	}
	return out, changed
}

// rewriteSchemaIDs walks JSON-LD recursively and maps EVERY string value
// that starts with a WordPress site URL (@id, url, item, target,
// urlTemplate, contentUrl, sameAs, …) to this site: the permalink map
// first, then the same path; absolute when the base URL is known. The
// WordPress search action (/?s=…) becomes /search?q=…. It returns a new
// value; v is not modified.
func (rw *rewriter) rewriteSchemaIDs(ctx context.Context, v any, st *rewriteStats) any {
	switch x := v.(type) {
	case string:
		if rw.urlRe == nil {
			return x
		}
		return rw.localAbsolute(ctx, x, st)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = rw.rewriteSchemaIDs(ctx, val, st)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = rw.rewriteSchemaIDs(ctx, x[i], st)
		}
		return out
	}
	return v
}

// localAbsolute rewrites str when it starts with a site URL: local
// permalink (or path), the WordPress search (/?s=…) mapped to
// /search?q=…, prefixed with the base URL when known. Other strings are
// returned unchanged.
func (rw *rewriter) localAbsolute(ctx context.Context, str string, st *rewriteStats) string {
	nv := rw.wpSearch(str, st)
	if nv == "" {
		if nv = rw.rewriteLeading(ctx, str, st); nv == str {
			return str
		}
	}
	if base := rw.s.baseURL(ctx); base != "" && strings.HasPrefix(nv, "/") {
		nv = base + nv
	}
	return nv
}

// wpSearch returns "/search?q=<term>" when str is exactly a WordPress
// search URL of the site (https://site/?s=<term>), else "". The term is
// kept verbatim so a JSON-LD placeholder ({search_term_string}) survives.
func (rw *rewriter) wpSearch(str string, st *rewriteStats) string {
	u, err := url.Parse(strings.TrimSpace(str))
	if err != nil || (u.Path != "" && u.Path != "/") || !strings.HasPrefix(u.RawQuery, "s=") || strings.Contains(u.RawQuery, "&") {
		return ""
	}
	if !rw.hosts[strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")] {
		return ""
	}
	st.links++
	return "/search?q=" + strings.TrimPrefix(u.RawQuery, "s=")
}

// replace maps one site URL: uploads → local media URL (counted, or kept
// and counted unresolved); other URLs → local permalink or path when links
// is set.
func (rw *rewriter) replace(ctx context.Context, raw string, st *rewriteStats, links bool) string {
	core := strings.TrimRight(raw, ".,;")
	trail := raw[len(core):]
	u, err := url.Parse(core)
	if err != nil {
		return raw
	}
	if local := rw.mappedMedia(ctx, core); local != "" {
		st.media++
		return local + trail
	}
	if strings.HasPrefix(u.Path, "/wp-content/uploads/") {
		if local := rw.mediaURL(ctx, u); local != "" {
			st.media++
			return local + trail
		}
		st.unresolved++
		return raw
	}
	if !links {
		return raw
	}
	st.links++
	local, ok := rw.links[pathOf(u.String())]
	if !ok {
		local = u.EscapedPath()
		if local == "" {
			local = "/"
		}
		if u.RawQuery != "" {
			local += "?" + u.RawQuery
		}
	}
	if u.Fragment != "" {
		local += "#" + u.EscapedFragment()
	}
	return local + trail
}

var mediaTokenRe = regexp.MustCompile(`(?:https?:)?//[^\s"'<>]+|/[^\s"'<>]+`)

// Resolve a source attachment before applying site/permalink rewriting. Relative
// paths belong to the source site, never to an unrelated CDN with the same name.
func (rw *rewriter) mappedMedia(ctx context.Context, raw string) string {
	if rw.s == nil || rw.s.media == nil {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	if u.Host == "" {
		if rw.base != nil && rw.base.Host != "" {
			u = rw.base.ResolveReference(u)
		} else {
			if !strings.HasPrefix(raw, "/") {
				return ""
			}
			for h := range rw.hosts {
				candidate := *u
				candidate.Host = h
				if local := rw.mediaURL(ctx, &candidate); local != "" {
					return local
				}
			}
			return ""
		}
	}
	return rw.mediaURL(ctx, u)
}

// mediaURL resolves an upload URL (original, -WxH variant or -scaled) to
// the local media URL, picking the variant closest to the requested width.
func (rw *rewriter) mediaURL(ctx context.Context, u *url.URL) string {
	width := 0
	if m := sizeSuffix.FindStringSubmatch(u.Path); m != nil {
		width, _ = strconv.Atoi(m[1])
	}
	candidates := mediaCandidates(u)
	// Exhaust exact query identities, including prior imports, before any
	// queryless fallback can select another attachment at the same path.
	for _, candidate := range candidates {
		id, ok := rw.media[mediaExactKey(candidate.String())]
		if mm := rw.lookup(ctx, id, ok, candidate); mm != nil {
			return rw.s.media.URL(mm, variantFor(width))
		}
	}
	for _, candidate := range candidates {
		key := mediaPathKey(candidate.String())
		id, known := rw.mediaPaths[key]
		if !known {
			// Legacy manually built rewriters use decoded path keys. Do not
			// apply those aliases to encoded delimiters.
			if key == mediaKey(candidate.String()) || len(mediaSourceReferences(candidate)) > 1 {
				id = rw.media[mediaKey(candidate.String())]
			}
		}
		if id != "" {
			if mm := rw.mediaByID(ctx, id); mm != nil {
				return rw.s.media.URL(mm, variantFor(width))
			}
		}
	}
	return ""
}

func mediaCandidates(u *url.URL) []*url.URL {
	candidates := []*url.URL{u}
	escaped := mediaIdentityPath(u)
	appendPath := func(path string) {
		decoded, err := url.PathUnescape(path)
		if err != nil {
			return
		}
		candidate := *u
		candidate.Path, candidate.RawPath = decoded, path
		candidates = append(candidates, &candidate)
	}
	if m := sizeSuffix.FindStringSubmatch(escaped); m != nil {
		base := sizeSuffix.ReplaceAllString(escaped, "$3")
		for _, path := range []string{base, strings.TrimSuffix(base, m[3]) + "-scaled" + m[3]} {
			appendPath(path)
		}
	}
	if base := scaledSuffix.ReplaceAllString(escaped, "$1"); base != escaped {
		appendPath(base)
	}
	return candidates
}

func (rw *rewriter) lookup(ctx context.Context, id string, ok bool, u *url.URL) *media.Media {
	if ok {
		if mm := rw.mediaByID(ctx, id); mm != nil {
			return mm
		}
	}
	// Media imported by an earlier migration: try the exact source URL.
	for _, scheme := range []string{u.Scheme, "https", "http"} {
		if scheme == "" {
			continue
		}
		candidate := *u
		candidate.Scheme, candidate.Fragment, candidate.RawFragment = scheme, "", ""
		for _, ref := range mediaSourceReferences(&candidate) {
			if mm := rw.mediaBySource(ctx, ref); mm != nil {
				return mm
			}
		}
	}
	return nil
}

// mediaBySource finds media imported earlier from ref. Media does not change
// while a run rewrites, so each reference is queried once per run rewriter,
// misses included; a failed query is not cached.
func (rw *rewriter) mediaBySource(ctx context.Context, ref string) *media.Media {
	rw.mu.Lock()
	mm, cached := rw.bySource[ref]
	rw.mu.Unlock()
	if cached {
		return mm
	}
	mm, err := rw.s.media.FindBySourceURL(ctx, ref)
	if err != nil {
		if !errors.Is(err, media.ErrNotFound) {
			return nil
		}
		mm = nil
	}
	rw.mu.Lock()
	if rw.bySource != nil {
		rw.bySource[ref] = mm
	}
	rw.mu.Unlock()
	return mm
}

func mediaSourceReferences(u *url.URL) []string {
	encoded := u.String()
	raw := "//" + u.Host + u.Path
	if u.Scheme != "" {
		raw = u.Scheme + ":" + raw
	}
	if u.RawQuery != "" || u.ForceQuery {
		raw += "?" + u.RawQuery
	}
	parsed, err := url.Parse(raw)
	if raw != encoded && err == nil && parsed.Path == u.Path && parsed.RawQuery == u.RawQuery && parsed.Fragment == "" && mediaPathKey(raw) == mediaPathKey(encoded) {
		return []string{encoded, raw}
	}
	return []string{encoded}
}

func (rw *rewriter) mediaByID(ctx context.Context, id string) *media.Media {
	rw.mu.Lock()
	mm, cached := rw.byID[id]
	rw.mu.Unlock()
	if cached {
		return mm
	}
	mm, err := rw.s.media.Get(ctx, id)
	if err != nil {
		return nil
	}
	rw.mu.Lock()
	rw.byID[id] = mm
	rw.mu.Unlock()
	return mm
}

// variantFor maps a requested width to a media variant ("" = original).
func variantFor(width int) string {
	switch {
	case width == 0:
		return ""
	case width <= 300:
		return "thumbnail"
	case width <= 768:
		return "medium"
	case width <= 1536:
		return "large"
	}
	return ""
}
