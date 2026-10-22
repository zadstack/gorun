package admin

import (
	"context"
	"net/url"

	"github.com/zadstack/gorun/internal/kernel"
)

// AIConnectionSummary describes a saved AI connection for display in other
// modules (the optional identityai module): never a secret.
type AIConnectionSummary struct {
	ID, Name, Provider, Model, Host, TestStatus string
}

// ListAIConnections lists the valid saved AI connection profiles: id, name,
// provider, model, host and the last recorded test status.
func ListAIConnections(ctx context.Context, d *kernel.Deps) ([]AIConnectionSummary, error) {
	h := &handlers{d: d}
	rows, err := h.listConnections(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AIConnectionSummary, 0, len(rows))
	for _, c := range rows {
		if c.Kind != "ai" || validateConnection(c) != nil {
			continue
		}
		host := ""
		if u, err := url.Parse(c.Endpoint); err == nil {
			host = u.Host
		}
		if c.Provider == AIProviderAnthropic {
			if u, err := url.Parse(anthropicURL); err == nil {
				host = u.Host
			}
		}
		out = append(out, AIConnectionSummary{ID: c.ID, Name: c.Name, Provider: c.Provider, Model: c.Model, Host: host, TestStatus: c.TestStatus})
	}
	return out, nil
}
