package menus

import (
	"context"
	"errors"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/i18n"
)

// resolve prepares items for display: content/term/archive items get their
// URL (and an empty label) from the referenced record — items whose record
// is gone or not published are dropped — then FilterResolveURL runs for
// every item (custom included) and may override.
// Without the content/taxonomy services the stored values are kept.
func (s *service) resolve(ctx context.Context, items []Item) []Item {
	out := make([]Item, 0, len(items))
	for _, it := range items {
		if !s.resolveOne(ctx, &it) {
			continue
		}
		it.Children = s.resolve(ctx, it.Children)
		out = append(out, it)
	}
	return out
}

func (s *service) resolveOne(ctx context.Context, it *Item) bool {
	if it.OpenInNew {
		it.Target = "_blank"
	}
	switch it.Kind {
	case KindContent:
		if svc, ok := s.content(); ok {
			c, err := svc.Get(ctx, it.RefID)
			if err != nil || c.Status != "published" || !content.PubliclyReadable(ctx, svc, c.ID) {
				if err != nil && !errors.Is(err, content.ErrNotFound) {
					s.log.Error("menus: resolve content", "id", it.RefID, "err", err)
				}
				return false
			}
			it.URL = ContentPath(c)
			if it.Label == "" {
				it.Label = c.Title
			}
		}
	case KindTerm:
		if svc, ok := s.taxonomy(); ok {
			t, err := svc.GetTerm(ctx, it.RefID)
			if err != nil {
				return false
			}
			it.URL = TermPath(t)
			if it.Label == "" {
				it.Label = t.Name
			}
		}
	case KindArchive:
		if svc, ok := s.content(); ok && it.URL == "" {
			if t, err := svc.GetType(ctx, it.RefID); err == nil {
				it.URL = ArchivePath(t)
				if it.Label == "" {
					it.Label = localized(t.Plural, i18n.Locale(ctx))
				}
			}
		}
	}
	if s.hooks != nil {
		v, err := s.hooks.Apply(ctx, FilterResolveURL, it)
		if err != nil {
			s.log.Error("menus: resolve_url filter", "item", it.ID, "err", err)
		} else if p, ok := v.(*Item); ok && p != nil && p != it {
			*it = *p
		}
	}
	return it.URL != ""
}

// localized picks m[locale], then English, then any value.
func localized(m map[string]string, locale string) string {
	if v := m[locale]; v != "" {
		return v
	}
	if v := m["en"]; v != "" {
		return v
	}
	for _, v := range m {
		return v
	}
	return ""
}
