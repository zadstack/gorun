package wpmigrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/media"
	"golang.org/x/net/html"
)

func safeReference(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Opaque != "" || (u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	u.User, u.RawQuery, u.Fragment, u.RawFragment = nil, "", "", ""
	u.ForceQuery = false
	if u.Host != "" {
		redactReferencePath(u)
	}
	return u.String()
}

func redactReferencePath(u *url.URL) {
	// Source paths can themselves carry access tokens, even without a query.
	const prefix = "/__redacted_reference/"
	fingerprint := strings.TrimPrefix(u.Path, prefix)
	decoded, err := hex.DecodeString(fingerprint)
	if !strings.HasPrefix(u.Path, prefix) || err != nil || len(decoded) != sha256.Size {
		digest := sha256.Sum256([]byte(u.Path))
		u.Path = prefix + hex.EncodeToString(digest[:])
	}
	u.RawPath = ""
}

func safeSourceReference(raw string) string {
	clean := safeReference(raw)
	u, err := url.Parse(clean)
	if err != nil || clean == "" {
		return ""
	}
	if u.Host == "" && strings.HasPrefix(u.Path, "/media/") {
		return clean
	}
	redactReferencePath(u)
	return u.String()
}

// Tokenizing covers quoted/unquoted attributes and picture/source without
// mistaking text, comments or script contents for image attributes.
func inlineMediaReferences(body string) []string {
	z := html.NewTokenizer(strings.NewReader(body))
	refs := []string{}
	for {
		switch z.Next() {
		case html.ErrorToken:
			if z.Err() == io.EOF {
				return refs
			}
			return refs
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			switch tok.Data {
			case "img", "source", "video", "audio", "track", "iframe", "embed", "input":
			default:
				continue
			}
			for _, attr := range tok.Attr {
				switch attr.Key {
				case "src", "poster", "data-src":
					refs = append(refs, attr.Val)
				case "srcset":
					refs = append(refs, srcsetReferences(attr.Val)...)
				}
			}
		}
	}
}

func (s *service) validationMediaSources(ctx context.Context, all []itemRow) map[string]string {
	refs := map[string]string{}
	for _, it := range all {
		if it.Kind != "media" || it.Status != ItemDone || it.LocalID == "" {
			continue
		}
		m, err := s.media.Get(ctx, it.LocalID)
		if err != nil {
			continue
		} // The missing record is reported by validateImportedMedia.
		refs[safeReference(m.URL)] = m.SourceURL
		for _, v := range m.Variants {
			refs[safeReference(v.URL)] = m.SourceURL
		}
	}
	return refs
}

// failedMediaKeys indexes the source URLs of media items that failed to
// import, so content still showing one is reported against that content.
func failedMediaKeys(all []itemRow) map[string]bool {
	failed := map[string]bool{}
	for _, it := range all {
		if it.Kind == "media" && it.Status == ItemFailed && it.WPURL != "" {
			failed[mediaExactKey(it.WPURL)] = true
		}
	}
	return failed
}

func (s *service) validateContentMedia(ctx context.Context, rep *ValidateReport, item *content.Item, documentPath string, hosts map[string]bool, sources map[string]string, failed map[string]bool, checked map[string]int) {
	seen := map[string]bool{}
	for _, ref := range inlineMediaReferences(item.Body) {
		if key := strings.TrimSpace(ref); !seen[key] && hosts[hostOf(ref)] && failed[mediaExactKey(ref)] {
			seen[key] = true
			rep.MissingMedia = append(rep.MissingMedia, LinkIssue{ContentID: item.ID, Title: item.Title, URL: safeSourceReference(ref)})
			rep.Errors = append(rep.Errors, rep.issue(item.ID, "media", "retained_source_media", ref, ref, 0))
			continue
		}
		old := sources[safeReference(ref)]
		if old == "" {
			old = ref
		}
		s.validateMediaReference(ctx, rep, item.ID, item.Title, old, resolveDocumentMedia(documentPath, ref), hosts, checked, seen)
	}
	if item.FeaturedMediaID == "" {
		return
	}
	m, err := s.media.Get(ctx, item.FeaturedMediaID)
	if err != nil {
		s.mediaRecordIssue(rep, item.ID, item.Title, item.FeaturedMediaID, "", err)
		return
	}
	s.validateMediaRecord(ctx, rep, item.ID, item.Title, m, hosts, checked, seen)
}

func (s *service) validateImportedMedia(ctx context.Context, rep *ValidateReport, all []itemRow, hosts map[string]bool, checked map[string]int) {
	for _, it := range all {
		if it.Kind != "media" || it.Status != ItemDone || it.LocalID == "" {
			continue
		}
		m, err := s.media.Get(ctx, it.LocalID)
		if err != nil {
			s.mediaRecordIssue(rep, "", "", it.WPURL, it.LocalURL, err)
			continue
		}
		s.validateMediaRecord(ctx, rep, "", m.Title, m, hosts, checked, map[string]bool{})
	}
}

func (s *service) validateMediaRecord(ctx context.Context, rep *ValidateReport, id, title string, m *media.Media, hosts map[string]bool, checked map[string]int, seen map[string]bool) {
	s.validateMediaReference(ctx, rep, id, title, m.SourceURL, m.URL, hosts, checked, seen)
	keys := make([]string, 0, len(m.Variants))
	for key := range m.Variants {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		s.validateMediaReference(ctx, rep, id, title, m.SourceURL, m.Variants[key].URL, hosts, checked, seen)
	}
}

func (s *service) validateMediaReference(ctx context.Context, rep *ValidateReport, id, title, old, ref string, hosts map[string]bool, checked map[string]int, seen map[string]bool) {
	clean := safeReference(ref)
	key := strings.TrimSpace(ref)
	if seen[key] {
		return
	}
	seen[key] = true
	u, err := url.Parse(strings.TrimSpace(ref))
	if err != nil || clean == "" || u.Path == "" {
		rep.UnverifiedChecks = append(rep.UnverifiedChecks, CheckIssue{ContentID: id, Kind: "media", Path: clean, OldRef: safeSourceReference(old), NewRef: clean, Outcome: "skipped", Reason: "invalid_media_reference", Code: "invalid_media_reference", CheckedAt: rep.GeneratedAt})
		return
	}
	local := u.Host == "" && u.Scheme == "" && strings.HasPrefix(u.Path, "/") && !strings.HasPrefix(ref, "//")
	if base, err := url.Parse(s.baseURL(ctx)); err == nil && base.Host != "" && u.Host == base.Host && (u.Scheme == "" || u.Scheme == base.Scheme) && strings.HasPrefix(u.Path, "/") {
		local = true
	}
	if !local {
		clean = safeSourceReference(ref)
		if hosts[hostOf(ref)] && strings.Contains(u.Path, "/wp-content/") {
			rep.MissingMedia = append(rep.MissingMedia, LinkIssue{ContentID: id, Title: title, URL: clean})
			rep.Errors = append(rep.Errors, rep.issue(id, "media", "retained_source_media", old, ref, 0))
		} else {
			rep.UnverifiedChecks = append(rep.UnverifiedChecks, CheckIssue{ContentID: id, Kind: "media", Path: clean, OldRef: safeSourceReference(old), NewRef: clean, Outcome: "skipped", Reason: "external_media_not_probed", Code: "external_media_not_probed", CheckedAt: rep.GeneratedAt})
		}
		return
	}
	clean = safeSourceReference(u.EscapedPath())
	p := u.EscapedPath()
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	st, exists := checked[p]
	if !exists {
		if s.check != nil {
			st = s.check(ctx, p)
		}
		checked[p] = st
		if st != 0 {
			rep.MediaChecked++
		}
	}
	if st == 0 {
		issue := s.unverifiedCheck(ctx, id, "media", clean)
		issue.OldRef, issue.NewRef = safeSourceReference(old), clean
		rep.UnverifiedChecks = append(rep.UnverifiedChecks, issue)
		return
	}
	if st < 200 || st >= 300 {
		rep.MissingMedia = append(rep.MissingMedia, LinkIssue{ContentID: id, Title: title, URL: clean})
		rep.Errors = append(rep.Errors, rep.issue(id, "media", "media_http_status", old, safeSourceReference(ref), st))
	}
}

func (s *service) mediaRecordIssue(rep *ValidateReport, id, title, old, ref string, err error) {
	if errors.Is(err, media.ErrNotFound) {
		rep.MissingMedia = append(rep.MissingMedia, LinkIssue{ContentID: id, Title: title, URL: safeReference(ref)})
		rep.Errors = append(rep.Errors, rep.issue(id, "media", "media_record_missing", old, ref, 0))
		return
	}
	rep.UnverifiedChecks = append(rep.UnverifiedChecks, CheckIssue{ContentID: id, Kind: "media", Path: safeReference(ref), OldRef: safeSourceReference(old), NewRef: safeReference(ref), Outcome: "unavailable", Reason: "media_unavailable", Code: "media_unavailable", CheckedAt: rep.GeneratedAt})
}

func (r *ValidateReport) issue(id, kind, code, old, ref string, status int) ValidationIssue {
	return ValidationIssue{ContentID: id, Kind: kind, Code: code, OldRef: safeSourceReference(old), NewRef: safeReference(ref), Status: status, CheckedAt: r.GeneratedAt}
}

func (r *ValidateReport) summarize(all []itemRow) {
	old := map[string]string{}
	for _, it := range all {
		old[it.LocalID] = it.WPURL
		switch it.Status {
		case ItemFailed:
			r.Errors = append(r.Errors, r.issue(it.LocalID, it.Kind, "import_failed", it.WPURL, it.LocalURL, 0))
		case ItemSkipped:
			r.Warnings = append(r.Warnings, r.issue(it.LocalID, it.Kind, "import_skipped", it.WPURL, it.LocalURL, 0))
		case ItemDone:
		default:
			r.UnverifiedChecks = append(r.UnverifiedChecks, CheckIssue{ContentID: it.LocalID, Kind: "import", Path: safeReference(it.LocalURL), OldRef: safeReference(it.WPURL), NewRef: safeReference(it.LocalURL), Outcome: "skipped", Reason: "import_incomplete", Code: "import_incomplete", CheckedAt: r.GeneratedAt})
		}
	}
	for _, f := range r.RenderFailures {
		r.Errors = append(r.Errors, r.issue(f.ContentID, "render", "render_http_status", old[f.ContentID], f.Path, f.Status))
	}
	for _, l := range r.BrokenLinks {
		r.Errors = append(r.Errors, r.issue(l.ContentID, "link", "broken_internal_link", l.URL, l.URL, 0))
	}
	for i := range r.UnverifiedChecks {
		issue := &r.UnverifiedChecks[i]
		issue.Path = safeReference(issue.Path)
		if issue.CheckedAt == "" {
			issue.CheckedAt = r.GeneratedAt
		}
		if issue.Code == "" {
			issue.Code = issue.Reason
		}
		if issue.NewRef == "" {
			issue.NewRef = issue.Path
		}
		if issue.OldRef == "" {
			issue.OldRef = safeSourceReference(old[issue.ContentID])
		}
	}
	for _, tag := range sortedKeys(r.Shortcodes) {
		r.Warnings = append(r.Warnings, r.issue("", "shortcode", "unresolved_shortcode", tag, tag, 0))
	}
	for _, tag := range sortedKeys(r.UnknownBlocks) {
		r.Warnings = append(r.Warnings, r.issue("", "block", "unknown_block", tag, tag, 0))
	}
	for _, change := range r.SlugChanges {
		r.Warnings = append(r.Warnings, r.issue("", "slug", "slug_changed", change.From, change.To, 0))
	}
	r.Readiness = "ready"
	r.Scope = []string{"import_outcomes", "page_render", "internal_links", "local_media", "shortcodes", "unknown_blocks", "slug_changes"}
	r.Limitations = []string{"semantic_reconciliation_not_performed"}
	switch {
	case len(r.Errors) > 0:
		r.Readiness = "blocked"
	case len(r.UnverifiedChecks) > 0 || r.RenderChecked == 0:
		r.Readiness = "unverified"
	case len(r.Warnings) > 0:
		r.Readiness = "warnings"
	}
}

func resolveDocumentMedia(documentPath, ref string) string {
	candidate, err := url.Parse(strings.TrimSpace(ref))
	if err != nil || candidate.Host != "" || candidate.Scheme != "" || strings.HasPrefix(candidate.Path, "/") {
		return ref
	}
	document, err := url.Parse(documentPath)
	if err != nil || documentPath == "" {
		return ref
	}
	return document.ResolveReference(candidate).String()
}
