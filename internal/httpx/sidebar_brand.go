package httpx

import "context"

type SidebarBrand struct {
	Title    string
	LogoPath string
}

// SetSidebarBrandResolver connects the site's identity service during module registration.
func (t *Templates) SetSidebarBrandResolver(resolve func(context.Context) SidebarBrand) {
	t.mu.Lock()
	t.brand = resolve
	t.mu.Unlock()
}
func (t *Templates) sidebarBrand(ctx context.Context) SidebarBrand {
	t.mu.Lock()
	resolve := t.brand
	t.mu.Unlock()
	if resolve == nil {
		return SidebarBrand{}
	}
	return resolve(ctx)
}
