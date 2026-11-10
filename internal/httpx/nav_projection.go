package httpx

import (
	"context"
	"net/http"
	"sync"
)

// navProjection loads the navigation, pins and shortcut projection for one
// request at most once. The page and every dashboard widget render through
// separate template executions; sharing the projection keeps them on one set
// of navigation and pin queries.
type navProjection struct {
	t         *Templates
	ctx       context.Context
	path      string
	translate func(string, ...any) string

	baseOnce   sync.Once
	items      []NavItem
	authorized []NavItem
	preferred  []string

	groupsOnce   sync.Once
	groups       []NavGroup
	sectionsOnce sync.Once
	sections     []NavigationSection
	pinsOnce     sync.Once
	pins         []QuickAccessItem
	toolsOnce    sync.Once
	tools        WorkspaceTools
}

type navCacheKey struct{}

type navCache struct {
	mu   sync.Mutex
	proj *navProjection
}

// NavigationCache lets every template render within one request share the
// navigation projection. Install it after authentication so the projection
// sees the final user and locale.
func NavigationCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), navCacheKey{}, &navCache{})))
	})
}

// navigation returns the request's shared projection, or a projection local
// to this render when no NavigationCache middleware is installed.
func (t *Templates) navigation(r *http.Request, translate func(string, ...any) string) *navProjection {
	ctx := context.Background()
	if r != nil {
		ctx = r.Context()
	}
	fresh := func() *navProjection {
		return &navProjection{t: t, ctx: ctx, path: pathOf(r), translate: translate}
	}
	cache, ok := ctx.Value(navCacheKey{}).(*navCache)
	if !ok {
		return fresh()
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	path := pathOf(r)
	if cache.proj == nil {
		cache.proj = fresh()
	}
	// Active styling depends on the path, so a render for another path (a
	// projection for a different page) never reuses the shared projection.
	if cache.proj.t != t || cache.proj.path != path {
		return fresh()
	}
	return cache.proj
}

func (p *navProjection) load() {
	p.baseOnce.Do(func() {
		if p.t.funcs.Nav != nil {
			p.items = p.t.funcs.Nav(p.ctx)
		}
		p.authorized = p.items
		if p.t.funcs.AllNav != nil {
			p.authorized = p.t.funcs.AllNav(p.ctx)
		}
		p.preferred = p.t.preferredNavGroups(p.ctx)
	})
}

func (p *navProjection) labelled(groups []NavGroup) []NavGroup {
	for i := range groups {
		groups[i].Label = groupLabel(func(k string) string { return p.translate(k) }, groups[i].LabelKey)
	}
	return groups
}

func (p *navProjection) Groups() []NavGroup {
	p.groupsOnce.Do(func() {
		if p.t.funcs.Nav == nil {
			return
		}
		p.load()
		p.groups = p.labelled(groupNav(p.items, p.preferred))
	})
	return p.groups
}

func (p *navProjection) Sections() []NavigationSection {
	p.sectionsOnce.Do(func() {
		if p.t.funcs.Nav == nil {
			return
		}
		p.load()
		p.sections = navigationSections(groupNav(p.items, p.preferred), p.authorized, p.path, p.preferred)
	})
	return p.sections
}

func (p *navProjection) Pins() []QuickAccessItem {
	p.pinsOnce.Do(func() {
		if p.t.funcs.PinnedNav != nil {
			p.pins = quickAccessItems(p.t.funcs.PinnedNav(p.ctx), p.path)
		}
	})
	return p.pins
}

func (p *navProjection) Tools() WorkspaceTools {
	p.toolsOnce.Do(func() {
		p.tools = workspaceTools(p.Groups(), p.Sections(), p.Pins())
	})
	return p.tools
}

// AllGroups lists every authorized item before personal visibility
// preferences, grouped and labelled.
func (p *navProjection) AllGroups() []NavGroup {
	if p.t.funcs.AllNav == nil {
		return nil
	}
	p.load()
	return p.labelled(groupNav(p.authorized, p.preferred))
}

// AllSections is the section projection over every authorized item.
func (p *navProjection) AllSections() []NavigationSection {
	if p.t.funcs.AllNav == nil {
		return nil
	}
	p.load()
	return navigationSections(groupNav(p.authorized, p.preferred), p.authorized, p.path, p.preferred)
}
