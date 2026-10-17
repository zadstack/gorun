package wpmigrate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/wpmigrate/gutenberg"
)

// ValidateReport is step 7: counts per kind, render checks, broken links,
// missing media, unresolved shortcodes and slug changes.
type ValidateReport struct {
	MigrationID      string            `json:"migration_id"`
	GeneratedAt      string            `json:"generated_at"`
	Errors           []ValidationIssue `json:"errors"`
	Warnings         []ValidationIssue `json:"warnings"`
	Readiness        string            `json:"readiness"`
	Scope            []string          `json:"scope"`
	Limitations      []string          `json:"limitations"`
	MediaChecked     int               `json:"media_checked"`
	Counts           []CountRow        `json:"counts"`
	RenderChecked    int               `json:"render_checked"`
	RenderFailures   []PathStatus      `json:"render_failures"`
	UnverifiedChecks []CheckIssue      `json:"unverified_checks"`
	BrokenLinks      []LinkIssue       `json:"broken_links"`
	MissingMedia     []LinkIssue       `json:"missing_media"`
	Shortcodes       map[string]int    `json:"shortcodes"`
	UnknownBlocks    map[string]int    `json:"unknown_blocks"`
	MarkdownRepaired int               `json:"markdown_repaired" doc:"Imported bodies whose raw Markdown (lists, pipe tables, inline code) was converted to HTML."`
	SlugChanges      []SlugChange      `json:"slug_changes"`
	Markdown         string            `json:"markdown"`
}

// ValidationIssue contains safe references and stable failure codes, never raw errors.
type ValidationIssue struct {
	ContentID string `json:"content_id"`
	Kind      string `json:"kind"`
	Code      string `json:"code"`
	OldRef    string `json:"old_ref"`
	NewRef    string `json:"new_ref"`
	CheckedAt string `json:"checked_at"`
	Status    int    `json:"status,omitempty"`
}

// CheckIssue records a check for which no HTTP response was received.
// Reason is a stable code; request errors and credentials are never stored.
type CheckIssue struct {
	ContentID string `json:"content_id"`
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	Outcome   string `json:"outcome"`
	Reason    string `json:"reason"`
	Code      string `json:"code"`
	OldRef    string `json:"old_ref"`
	NewRef    string `json:"new_ref"`
	CheckedAt string `json:"checked_at"`
}

// CountRow compares source and imported counts of one kind.
type CountRow struct {
	Kind     string `json:"kind"`
	Source   int    `json:"source"`
	Imported int    `json:"imported"`
	Skipped  int    `json:"skipped"`
	Failed   int    `json:"failed"`
}

// PathStatus is a local URL that did not render.
type PathStatus struct {
	ContentID string `json:"content_id"`
	Path      string `json:"path"`
	Status    int    `json:"status"`
}

// LinkIssue is a link or image that does not resolve locally.
type LinkIssue struct {
	ContentID string `json:"content_id"`
	Title     string `json:"title"`
	URL       string `json:"url"`
}

// SlugChange is a WordPress slug that changed on import.
type SlugChange struct {
	Type string `json:"type"`
	From string `json:"from"`
	To   string `json:"to"`
	Path string `json:"path"`
}

var (
	hrefRe = regexp.MustCompile(`(?i)\bhref="([^"]*)"`)
)

// Validate builds the report, stores it on the migration and in the log.
func (s *service) Validate(ctx context.Context, migrationID string) (*ValidateReport, error) {
	m, err := s.getMigration(ctx, migrationID)
	if err != nil {
		return nil, err
	}
	rep := &ValidateReport{MigrationID: m.ID, GeneratedAt: db.Now(), Shortcodes: map[string]int{}, UnknownBlocks: map[string]int{},
		Errors: []ValidationIssue{}, Warnings: []ValidationIssue{}, RenderFailures: []PathStatus{}, UnverifiedChecks: []CheckIssue{}, BrokenLinks: []LinkIssue{}, MissingMedia: []LinkIssue{}, SlugChanges: []SlugChange{}}
	all, err := s.items(ctx, m.ID, "")
	if err != nil {
		return nil, err
	}
	rep.Counts = countRows(m, all)
	known := map[string]bool{"/": true}
	for _, it := range all {
		if (it.Kind == "content" || it.Kind == "term") && it.Status == ItemDone && it.LocalURL != "" {
			known[it.LocalURL] = true
		}
	}
	hosts := s.siteHosts(ctx, m)
	checked := map[string]int{}
	mediaChecks := map[string]int{}
	mediaSources := s.validationMediaSources(ctx, all)
	failedMedia := failedMediaKeys(all)
	for _, it := range all {
		if it.Kind != "content" || it.Status != ItemDone || it.LocalID == "" {
			continue
		}
		item, err := s.content.Get(ctx, it.LocalID)
		if err != nil {
			rep.UnverifiedChecks = append(rep.UnverifiedChecks, CheckIssue{ContentID: it.LocalID, Kind: "render", Path: it.LocalURL,
				Outcome: "unavailable", Reason: "content_unavailable"})
			continue
		}
		addCounts(rep.UnknownBlocks, it.Meta["unknown_blocks"])
		if it.Meta["markdown_repaired"] == true {
			rep.MarkdownRepaired++
		}
		if ls, _ := it.Meta["local_slug"].(string); ls != "" && ls != it.WPSlug {
			rep.SlugChanges = append(rep.SlugChanges, SlugChange{Type: item.Type, From: it.WPSlug, To: ls, Path: safeReference(it.LocalURL)})
		}
		for tag, n := range gutenberg.Shortcodes(stripCode(item.Body)) {
			rep.Shortcodes[tag] += n
		}
		switch {
		case item.Status != "published":
			rep.UnverifiedChecks = append(rep.UnverifiedChecks, CheckIssue{ContentID: item.ID, Kind: "render", Path: it.LocalURL,
				Outcome: "skipped", Reason: "unpublished"})
		case s.check == nil || it.LocalURL == "":
			rep.UnverifiedChecks = append(rep.UnverifiedChecks, s.unverifiedCheck(ctx, item.ID, "render", it.LocalURL))
		default:
			if st := s.check(ctx, it.LocalURL); st != 0 {
				rep.RenderChecked++
				if st != 200 {
					rep.RenderFailures = append(rep.RenderFailures, PathStatus{ContentID: item.ID, Path: safeReference(it.LocalURL), Status: st})
				}
			} else {
				rep.UnverifiedChecks = append(rep.UnverifiedChecks, s.unverifiedCheck(ctx, item.ID, "render", it.LocalURL))
			}
		}
		for _, mt := range hrefRe.FindAllStringSubmatch(item.Body, -1) {
			if href := mt[1]; !s.linkOK(ctx, href, hosts, known, checked) {
				local := strings.HasPrefix(strings.TrimSpace(href), "/") && !strings.HasPrefix(strings.TrimSpace(href), "//")
				if st, probed := checked[pathOf(href)]; local && probed && st == 0 {
					rep.UnverifiedChecks = append(rep.UnverifiedChecks, s.unverifiedCheck(ctx, item.ID, "link", href))
					continue
				}
				rep.BrokenLinks = append(rep.BrokenLinks, LinkIssue{ContentID: item.ID, Title: item.Title, URL: safeSourceReference(href)})
			}
		}
		s.validateContentMedia(ctx, rep, item, it.LocalURL, hosts, mediaSources, failedMedia, mediaChecks)
	}
	s.validateImportedMedia(ctx, rep, all, hosts, mediaChecks)
	rep.summarize(all)
	rep.Markdown = rep.markdown()
	if err := s.setStats(ctx, m.ID, "validate", rep); err != nil {
		return nil, err
	}
	s.logEntry(ctx, m.ID, "info", "", "validate_report", map[string]any{"broken_links": len(rep.BrokenLinks),
		"missing_media": len(rep.MissingMedia), "render_failures": len(rep.RenderFailures), "unverified_checks": len(rep.UnverifiedChecks), "markdown": rep.Markdown})
	s.audit.Record(ctx, "wpmigrate.validate", "migration", m.ID, map[string]any{"broken_links": len(rep.BrokenLinks)})
	return rep, nil
}

func (s *service) unverifiedCheck(ctx context.Context, contentID, kind, path string) CheckIssue {
	clean := safeReference(path)
	if kind == "link" {
		clean = safeSourceReference(path)
	}
	issue := CheckIssue{ContentID: contentID, Kind: kind, Path: clean, OldRef: clean, NewRef: clean, CheckedAt: db.Now(), Outcome: "skipped"}
	switch {
	case s.check == nil:
		issue.Reason = "check_disabled"
	case path == "":
		issue.Reason = "missing_path"
	case s.baseURL(ctx) == "":
		issue.Reason = "no_base_url"
	default:
		issue.Outcome, issue.Reason = "unavailable", "request_failed"
	}
	issue.Code = issue.Reason
	return issue
}

// linkOK reports whether href resolves: external links pass, links still
// pointing at the WordPress host fail, local paths must be an imported
// permalink, a redirect, or answer 2xx/3xx to the render check.
func (s *service) linkOK(ctx context.Context, href string, hosts, known map[string]bool, checked map[string]int) bool {
	href = strings.TrimSpace(href)
	switch {
	case href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "mailto:") || strings.HasPrefix(href, "tel:"):
		return true
	case strings.HasPrefix(href, "//") || strings.Contains(href, "://"):
		return !hosts[hostOf(href)]
	case !strings.HasPrefix(href, "/") || strings.HasPrefix(href, "/media/"):
		return true
	}
	u, err := url.Parse(href)
	if err != nil {
		return false
	}
	p := pathOf(u.String())
	if known[p] {
		return true
	}
	if _, ok := s.redirects.Lookup(ctx, p); ok {
		return true
	}
	if s.check == nil {
		checked[p] = 0
		return false
	}
	st, done := checked[p]
	if !done {
		st = s.check(ctx, p)
		checked[p] = st
	}
	return st >= 200 && st < 400
}

// countRows compares discovery counts with item outcomes.
func countRows(m *migrationRow, all []itemRow) []CountRow {
	src := map[string]int{}
	if m.Discovery != nil {
		c := m.Discovery.Counts
		src["user"] = c["users"]
		src["media"] = c["media"]
		src["product"] = c["product"]
		src["menu"] = c["menus"]
		for wp, local := range m.Mapping.Taxonomies {
			if local != "" {
				src["term"] += c[wp]
			}
		}
		for _, typ := range m.Mapping.contentTypes() {
			src["content"] += c[typ]
		}
	}
	rows := map[string]*CountRow{}
	for _, k := range []string{"user", "term", "media", "content", "product", "menu", "redirect"} {
		rows[k] = &CountRow{Kind: k, Source: src[k]}
	}
	for _, it := range all {
		r := rows[it.Kind]
		if r == nil {
			continue
		}
		switch it.Status {
		case ItemDone:
			r.Imported++
		case ItemSkipped:
			r.Skipped++
		case ItemFailed:
			r.Failed++
		}
	}
	out := []CountRow{}
	for _, k := range []string{"user", "term", "media", "content", "product", "menu", "redirect"} {
		out = append(out, *rows[k])
	}
	return out
}

func (r *ValidateReport) markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Migration report / تقرير الترحيل\n\n- Migration: `%s`\n- Generated: %s\n\n", r.MigrationID, r.GeneratedAt)
	fmt.Fprintf(&b, "- Readiness: %s\n- Errors: %d\n- Warnings: %d\n- Unverified checks: %d\n\n", r.Readiness, len(r.Errors), len(r.Warnings), len(r.UnverifiedChecks))
	fmt.Fprintf(&b, "- Media resources checked: %d\n- Scope: %s\n- Limitations: %s\n\nReadiness applies only to the checks in this report. Source text, relationships, authors, taxonomy and SEO are not reconciled by these checks.\n\n", r.MediaChecked, strings.Join(r.Scope, ", "), strings.Join(r.Limitations, ", "))
	for _, group := range []struct {
		name   string
		issues []ValidationIssue
	}{{"Errors / أخطاء", r.Errors}, {"Warnings / تحذيرات", r.Warnings}} {
		fmt.Fprintf(&b, "## %s\n\n", group.name)
		for _, issue := range group.issues {
			fmt.Fprintf(&b, "- %s `%s` → `%s`: %s; content `%s`; HTTP %d; %s\n", md(issue.Kind), md(issue.OldRef), md(issue.NewRef), md(issue.Code), md(issue.ContentID), issue.Status, md(issue.CheckedAt))
		}
		b.WriteString("\n")
	}
	b.WriteString("## Counts / الأعداد\n\n| Kind | WordPress | Imported | Skipped | Failed |\n|---|---:|---:|---:|---:|\n")
	for _, c := range r.Counts {
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d |\n", c.Kind, c.Source, c.Imported, c.Skipped, c.Failed)
	}
	fmt.Fprintf(&b, "\n## Render checks / فحص العرض\n\n%d checked, %d failed.\n", r.RenderChecked, len(r.RenderFailures))
	for _, f := range r.RenderFailures {
		fmt.Fprintf(&b, "- `%s` → HTTP %d\n", md(f.Path), f.Status)
	}
	fmt.Fprintf(&b, "\n## Not verified / لم يتم التحقق\n\n%d unavailable or skipped checks. Report generation does not confirm these paths.\n", len(r.UnverifiedChecks))
	for _, issue := range r.UnverifiedChecks {
		fmt.Fprintf(&b, "- %s `%s`: %s (%s)\n", md(issue.Kind), md(issue.Path), md(issue.Outcome), md(issue.Reason))
		fmt.Fprintf(&b, "  content `%s`; `%s` → `%s`; %s\n", md(issue.ContentID), md(issue.OldRef), md(issue.NewRef), md(issue.CheckedAt))
	}
	section := func(title string, list []LinkIssue) {
		fmt.Fprintf(&b, "\n## %s\n\n", title)
		if len(list) == 0 {
			b.WriteString("None / لا يوجد.\n")
		}
		for _, l := range list {
			fmt.Fprintf(&b, "- %s: `%s`\n", md(l.Title), md(l.URL))
		}
	}
	section("Broken internal links / روابط داخلية معطلة", r.BrokenLinks)
	section("Missing media / وسائط مفقودة", r.MissingMedia)
	b.WriteString("\n## Unresolved shortcodes / أكواد مختصرة لم تُحل\n\n")
	if len(r.Shortcodes) == 0 {
		b.WriteString("None / لا يوجد.\n")
	}
	for _, tag := range sortedKeys(r.Shortcodes) {
		fmt.Fprintf(&b, "- `[%s]` ×%d\n", md(tag), r.Shortcodes[tag])
	}
	if len(r.UnknownBlocks) > 0 {
		b.WriteString("\n## Unknown blocks / كتل غير معروفة\n\n")
		for _, line := range sortedCounts(r.UnknownBlocks) {
			fmt.Fprintf(&b, "- %s\n", md(line))
		}
	}
	if r.MarkdownRepaired > 0 {
		fmt.Fprintf(&b, "\n## Markdown repaired / ماركداون تم إصلاحه\n\n%d imported bodies had raw Markdown converted to HTML.\n", r.MarkdownRepaired)
	}
	b.WriteString("\n## Slug changes / روابط تغيّرت\n\n")
	if len(r.SlugChanges) == 0 {
		b.WriteString("None / لا يوجد.\n")
	}
	sort.Slice(r.SlugChanges, func(i, j int) bool { return r.SlugChanges[i].Path < r.SlugChanges[j].Path })
	for _, c := range r.SlugChanges {
		fmt.Fprintf(&b, "- %s: `%s` → `%s` (%s)\n", md(c.Type), md(c.From), md(c.To), md(c.Path))
	}
	return b.String()
}

// report returns the stored validation report of a migration.
func (s *service) report(ctx context.Context, migrationID string) (*ValidateReport, error) {
	m, err := s.getMigration(ctx, migrationID)
	if err != nil {
		return nil, err
	}
	raw, ok := m.Stats["validate"]
	if !ok {
		return nil, ErrNotFound
	}
	var rep ValidateReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		return nil, err
	}
	rep.sanitizeStoredReferences()
	return &rep, nil
}

// md neutralizes Markdown/HTML in report values: table pipes, backticks
// (which would close code spans), "<" and line breaks.
func md(s string) string {
	return strings.NewReplacer("|", `\|`, "`", "'", "<", "&lt;", "\r", " ", "\n", " ").Replace(s)
}
