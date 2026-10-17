package admin

import (
	"context"
	"errors"
	"strings"

	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
)

func (h *handlers) sidebarBrand(ctx context.Context) httpx.SidebarBrand {
	brand := httpx.SidebarBrand{Title: strings.TrimSpace(h.d.Settings.GetString(ctx, "site.title", ""))}
	id := h.d.Settings.GetString(ctx, "site.logo", "")
	if id == "" {
		return brand
	}
	svc, ok := kernel.Get[media.Service](h.d, kernel.ServiceMedia)
	if !ok {
		return brand
	}
	logo, err := svc.Get(ctx, id)
	if err != nil {
		if !errors.Is(err, media.ErrNotFound) {
			h.d.Log.Error("admin: resolve sidebar logo", "err", err)
		}
		return brand
	}
	switch logo.Mime {
	case "image/png", "image/jpeg", "image/webp", "image/gif", "image/svg+xml":
		brand.LogoPath = media.PublicPath(logo.StorageKey)
	}
	return brand
}
