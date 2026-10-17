package theme

import (
	"context"
	"strings"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/seo"
	"github.com/zadstack/gorun/internal/taxonomy"
)

// HookMenuResolveURL is the filter (on *menus.Item) that fills URL for
// menu items of kind content, term and archive.
const HookMenuResolveURL = "menus.resolve_url"

const maxParentDepth = 16

// Linker computes public paths (permalinks). It is the single source of
// URLs for the site router, the url template func and menu items:
//
//	page (hierarchical)   /parent/child         (the front page is /)
//	other content types   /{type}/{slug}
//	term                  /{taxonomy}/{term}
//	type archive          /{archive_slug or type}/
//
// Paths are returned unescaped (Arabic slugs stay UTF-8); html/template
// percent-encodes them in attributes.
type Linker struct{ d *kernel.Deps }

// NewLinker returns a Linker reading services lazily from d.
func NewLinker(d *kernel.Deps) *Linker { return &Linker{d: d} }

func (l *Linker) content() (content.Service, bool) {
	return kernel.Get[content.Service](l.d, kernel.ServiceContent)
}

// FrontPageID returns the static front page's content id (setting
// site.front_page), or "".
func (l *Linker) FrontPageID(ctx context.Context) string {
	if l.d.Settings == nil {
		return ""
	}
	return l.d.Settings.GetString(ctx, SettingFront, "")
}

// Item returns the public path of it.
func (l *Linker) Item(ctx context.Context, it *content.Item) string {
	return l.ItemWithFront(ctx, it, l.FrontPageID(ctx))
}

// SiteLocale is the public site locale (setting site.locale): its URLs carry no locale prefix.
func (l *Linker) SiteLocale(ctx context.Context) string {
	if l.d.Settings == nil {
		return defaultSiteLocale
	}
	return l.d.Settings.GetString(ctx, SettingLocale, defaultSiteLocale)
}

// ItemWithFront is Item with the front page id already known (renders
// look it up once). An item in another locale than the site's lives under
// /<locale>/, and a translation of the front page is that locale's /.
func (l *Linker) ItemWithFront(ctx context.Context, it *content.Item, frontID string) string {
	if it == nil {
		return "/"
	}
	return i18n.LocalizePath(it.Locale, l.SiteLocale(ctx), l.itemPath(ctx, it, frontID))
}

// itemPath is the unprefixed path of it.
func (l *Linker) itemPath(ctx context.Context, it *content.Item, frontID string) string {
	if it.ID != "" && it.ID == frontID {
		return "/"
	}
	if it.TranslationGroup != "" && frontID != "" && l.inFrontGroup(ctx, it, frontID) {
		return "/"
	}
	if it.Type != "page" {
		return "/" + it.Type + "/" + it.Slug
	}
	segs := []string{it.Slug}
	svc, ok := l.content()
	parent := it.ParentID
	for i := 0; ok && parent != "" && i < maxParentDepth; i++ {
		p, err := svc.Get(ctx, parent)
		if err != nil {
			break
		}
		segs = append(segs, p.Slug)
		parent = p.ParentID
	}
	for i, j := 0, len(segs)-1; i < j; i, j = i+1, j-1 {
		segs[i], segs[j] = segs[j], segs[i]
	}
	return "/" + strings.Join(segs, "/")
}

// inFrontGroup reports whether it is a translation of the front page.
func (l *Linker) inFrontGroup(ctx context.Context, it *content.Item, frontID string) bool {
	svc, ok := l.content()
	if !ok {
		return false
	}
	front, err := svc.Get(ctx, frontID)
	return err == nil && front.TranslationGroup == it.TranslationGroup
}

// Term returns the public path of t.
func (l *Linker) Term(t *taxonomy.Term) string {
	if t == nil {
		return "/"
	}
	return "/" + t.Taxonomy + "/" + t.Slug
}

// Archive returns the archive path of content type typ.
func (l *Linker) Archive(ctx context.Context, typ string) string {
	if svc, ok := l.content(); ok {
		if t, err := svc.GetType(ctx, typ); err == nil {
			return archivePath(t)
		}
	}
	return "/" + typ + "/"
}

// archivePath is the archive path of t (ArchiveSlug, else the type slug).
func archivePath(t *content.Type) string {
	if t.ArchiveSlug != "" {
		return "/" + t.ArchiveSlug + "/"
	}
	return "/" + t.Slug + "/"
}

// MenuItem sets it.URL (and its children's) for items that reference a
// record: kind content (RefID = content id) and term (RefID = term id)
// always get the permalink, overwriting the menus module's fallback path
// ("/"+slug is wrong for every type but top-level pages); kind archive
// (RefID = type) is filled only when empty, so a stored URL wins.
func (l *Linker) MenuItem(ctx context.Context, it *menus.Item) {
	if it.RefID != "" {
		switch it.Kind {
		case menus.KindContent:
			if svc, ok := l.content(); ok {
				if c, err := svc.Get(ctx, it.RefID); err == nil {
					it.URL = l.Item(ctx, c)
				}
			}
		case menus.KindTerm:
			if svc, ok := kernel.Get[taxonomy.Service](l.d, kernel.ServiceTaxonomy); ok {
				if t, err := svc.GetTerm(ctx, it.RefID); err == nil {
					it.URL = l.Term(t)
				}
			}
		case menus.KindArchive:
			if it.URL == "" {
				it.URL = l.Archive(ctx, it.RefID)
			}
		}
	}
	for i := range it.Children {
		l.MenuItem(ctx, &it.Children[i])
	}
}

// resolveMenuURL is the HookMenuResolveURL filter.
func (l *Linker) resolveMenuURL(ctx context.Context, v any) (any, error) {
	if it, ok := v.(*menus.Item); ok && it != nil {
		l.MenuItem(ctx, it)
	}
	return v, nil
}

// sitemapEntry is the seo.FilterSitemapEntry filter: it replaces the seo
// module's fallback Loc with the permalink of the entry's item, term or
// type archive (posts → /post/<slug>, child pages → /parent/child, the
// static front page → /). An entry whose Loc is already empty stays
// dropped.
func (l *Linker) sitemapEntry(ctx context.Context, v any) (any, error) {
	e, ok := v.(*seo.SitemapEntry)
	if !ok || e == nil || e.Loc == "" {
		return v, nil
	}
	switch {
	case e.Item != nil:
		e.Loc = l.Item(ctx, e.Item)
	case e.Term != nil:
		e.Loc = l.Term(e.Term)
	case e.Type != nil:
		e.Loc = archivePath(e.Type)
	}
	return v, nil
}
