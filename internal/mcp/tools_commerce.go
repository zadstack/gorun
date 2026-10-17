package mcp

import (
	"context"

	"github.com/zadstack/gorun/internal/kernel"
)

type listProductsInput struct {
	ActiveOnly bool `json:"active_only,omitempty"`
}

// commerceTools exposes the store catalog (commerce.Service.Products(ctx,
// activeOnly)) when the commerce module is booted.
func commerceTools(d *kernel.Deps, s *server) []kernel.MCPTool {
	svc, ok := kernel.Get[any](d, kernel.ServiceCommerce)
	if !ok {
		return nil
	}
	products, ok := bindMethod[bool](svc, "Products")
	if !ok {
		s.log.Warn("mcp: commerce service has no Products(ctx, bool) (T, error); list_products disabled")
		return nil
	}
	return []kernel.MCPTool{{Name: "list_products", Scopes: []string{scopeCommerce}, Input: listProductsInput{},
		Description: "List store products (MCP server plans) with prices in minor units.",
		Handler: bind(func(ctx context.Context, in listProductsInput) (any, error) {
			list, err := products(ctx, in.ActiveOnly)
			if err != nil {
				return nil, err
			}
			return map[string]any{"products": list}, nil
		})}}
}
