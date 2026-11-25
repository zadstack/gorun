package mcp

import (
	"context"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/taxonomy"
	"github.com/zadstack/gorun/internal/theme"
)

// Completion sources: what a prompt argument or template variable completes to.
const (
	completeTypes        = "types"         // content type slugs
	completeTaxonomies   = "taxonomies"    // taxonomy slugs (of context "type", when given)
	completeTerms        = "terms"         // term slugs of context "taxonomy"
	completeContentSlugs = "content_slugs" // content slugs of context "type"
	completeLocales      = "locales"       // the site's content languages
	completeTemplates    = "templates"     // theme template names (themes.manage)
	completeMenus        = "menus"         // menu slugs
)

const (
	maxCompletions = 20
	// maxSlugCandidates bounds the slugs read for one completion before
	// each is checked through content.Service.Get (per-item read rules).
	maxSlugCandidates = 100
)

// complete answers completion/complete for a prompt argument or a resource
// template variable. The reference must exist (else -32602) and be usable
// by the key (a listed prompt, a readable template; else -32003); values
// come from the services as the key (their read rules apply), prefix
// matched, at most 20. An argument without a completion source gets none.
func (s *server) complete(ctx context.Context, p *sdk.CompleteParams, gates map[string]func(context.Context) bool) (*sdk.CompleteResult, error) {
	var sources map[string]string
	switch p.Ref.Type {
	case "ref/prompt":
		def, ok := s.prompt(p.Ref.Name)
		if !ok {
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "unknown prompt " + p.Ref.Name}
		}
		if !s.promptVisible(ctx, def) {
			return nil, &jsonrpc.Error{Code: rpcForbidden, Message: "forbidden: this key cannot use prompt " + def.name}
		}
		sources = def.complete
	case "ref/resource":
		if rt, ok := s.resourceTemplate(p.Ref.URI); ok {
			if !s.canRead(ctx, rt) {
				return nil, &jsonrpc.Error{Code: rpcForbidden, Message: "forbidden: this key cannot read " + rt.uri}
			}
			sources = rt.complete
		} else if _, known := gates[p.Ref.URI]; !known && p.Ref.URI != guideURI {
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "unknown resource template " + p.Ref.URI}
		}
	default:
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "unknown reference type " + p.Ref.Type}
	}
	var given map[string]string
	if p.Context != nil {
		given = p.Context.Arguments
	}
	values, more, err := s.completionValues(ctx, sources[p.Argument.Name], p.Argument.Value, given)
	if err != nil {
		s.log.Error("mcp: completion", "source", sources[p.Argument.Name], "err", err)
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "internal error"}
	}
	if values == nil {
		values = []string{}
	}
	return &sdk.CompleteResult{Completion: sdk.CompletionResultDetails{Values: values, HasMore: more}}, nil
}

// completionValues returns the values of source starting with prefix
// (case-insensitive), sorted, at most maxCompletions, and whether more
// matched. Unknown sources and missing context give no values.
func (s *server) completionValues(ctx context.Context, source, prefix string, given map[string]string) ([]string, bool, error) {
	var all []string
	switch source {
	case completeTypes:
		types, err := s.contentTypes(ctx)
		if err != nil {
			return nil, false, err
		}
		for _, t := range types {
			all = append(all, t.Slug)
		}
	case completeTaxonomies:
		svc, ok := kernel.Get[taxonomy.Service](s.d, kernel.ServiceTaxonomy)
		if !ok {
			return nil, false, nil
		}
		list, err := svc.Taxonomies(ctx)
		if err != nil {
			return nil, false, err
		}
		for _, t := range list {
			if typ := given["type"]; typ == "" || slices.Contains(t.Types, typ) {
				all = append(all, t.Slug)
			}
		}
	case completeTerms:
		return s.termSlugs(ctx, given["taxonomy"], prefix, given["locale"])
	case completeContentSlugs:
		return s.contentSlugs(ctx, given["type"], prefix, given["locale"])
	case completeLocales:
		all = i18n.ReadContentLocales(ctx, s.d.Settings)
	case completeTemplates:
		svc, ok := kernel.Get[theme.Service](s.d, kernel.ServiceTheme)
		if !ok || !canUse(ctx, s, "list_templates") {
			return nil, false, nil
		}
		files, err := svc.Templates(ctx)
		if err != nil {
			return nil, false, err
		}
		for _, f := range files {
			all = append(all, f.Name)
		}
	case completeMenus:
		svc, ok := kernel.Get[menus.Service](s.d, kernel.ServiceMenus)
		if !ok {
			return nil, false, nil
		}
		list, err := svc.List(ctx)
		if err != nil {
			return nil, false, err
		}
		for _, m := range list {
			all = append(all, m.Slug)
		}
	default:
		return nil, false, nil
	}
	values, more := matchPrefix(all, prefix)
	return values, more, nil
}

// canUse reports whether the key in ctx holds the scopes of the tool name.
func canUse(ctx context.Context, s *server, name string) bool {
	t, ok := s.tool(name)
	return ok && allowed(ctx, t)
}

// matchPrefix keeps the distinct values starting with prefix (ignoring
// case), sorted, at most maxCompletions; more reports a cut.
func matchPrefix(all []string, prefix string) ([]string, bool) {
	p := strings.ToLower(prefix)
	out, seen := []string{}, map[string]bool{}
	for _, v := range all {
		if v != "" && !seen[v] && strings.HasPrefix(strings.ToLower(v), p) {
			seen[v] = true
			out = append(out, v)
		}
	}
	slices.Sort(out)
	if len(out) > maxCompletions {
		return out[:maxCompletions], true
	}
	return out, false
}

// termSlugs completes the term slugs of taxonomy (optionally one locale).
func (s *server) termSlugs(ctx context.Context, tax, prefix, locale string) ([]string, bool, error) {
	svc, ok := kernel.Get[taxonomy.Service](s.d, kernel.ServiceTaxonomy)
	if !ok || tax == "" {
		return nil, false, nil
	}
	if _, err := svc.Get(ctx, tax); err != nil {
		return nil, false, nil // unknown taxonomy: nothing to suggest
	}
	var all []string
	for page := 1; page <= 10; page++ {
		terms, total, err := svc.Terms(ctx, tax, taxonomy.TermQuery{Locale: locale, Page: page, PerPage: 100})
		if err != nil {
			return nil, false, err
		}
		for _, t := range terms {
			all = append(all, t.Slug)
		}
		if page*100 >= total || len(terms) == 0 {
			break
		}
	}
	values, more := matchPrefix(all, prefix)
	return values, more, nil
}

// contentSlugs completes the slugs of items of typ starting with prefix.
// Candidates come from one bounded query; each is then read through
// content.Service.Get, so the service's read rules (authors who may only
// see their own drafts) apply exactly as in get_content.
func (s *server) contentSlugs(ctx context.Context, typ, prefix, locale string) ([]string, bool, error) {
	svc, ok := kernel.Get[content.Service](s.d, kernel.ServiceContent)
	if !ok || typ == "" {
		return nil, false, nil
	}
	q := `SELECT id, slug FROM content WHERE type = ? AND status != 'trash' AND slug LIKE ? ESCAPE '\'`
	args := []any{typ, likePrefix(prefix)}
	if locale != "" {
		q += ` AND locale = ?`
		args = append(args, locale)
	}
	q += ` ORDER BY slug, id LIMIT ?`
	args = append(args, maxSlugCandidates)
	rows, err := s.d.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	type cand struct{ id, slug string }
	var cands []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.id, &c.slug); err != nil {
			rows.Close()
			return nil, false, err
		}
		cands = append(cands, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	out := []string{}
	for _, c := range cands {
		if slices.Contains(out, c.slug) {
			continue
		}
		if len(out) == maxCompletions {
			return out, true, nil
		}
		if _, err := svc.Get(ctx, c.id); err != nil {
			continue // not readable by this key (or gone meanwhile)
		}
		out = append(out, c.slug)
	}
	return out, len(cands) == maxSlugCandidates, nil
}

// likePrefix is a LIKE pattern matching values that start with p.
func likePrefix(p string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(p) + "%"
}
