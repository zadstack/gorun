package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"

	"github.com/zadstack/gorun/internal/kernel"
)

// AIConnectionSummary describes an AI destination for display in other
// modules (the optional identityai module): never a secret.
type AIConnectionSummary struct {
	ID, Name, Provider, Model, Host, TestStatus string
}

// ListAIConnections lists the valid saved AI connection profiles: id, name,
// provider, model, the host the request really goes to (the saved
// endpoint) and the last recorded test status.
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
		out = append(out, AIConnectionSummary{ID: c.ID, Name: c.Name, Provider: c.Provider, Model: c.Model, Host: hostOf(c.Endpoint), TestStatus: c.TestStatus})
	}
	return out, nil
}

// WritingAIDestination describes the site's writing settings (ai.*) as a
// destination: provider, model and host, without the key. ok is false when
// the writing settings are not configured or the key is unreadable.
func WritingAIDestination(ctx context.Context, d *kernel.Deps) (AIConnectionSummary, bool) {
	h := &handlers{d: d}
	c, err := h.loadAIConfig(ctx)
	if err != nil || c.Model == "" {
		return AIConnectionSummary{}, false
	}
	host := hostOf(c.BaseURL)
	if c.Provider == AIProviderAnthropic {
		host = hostOf(anthropicURL)
	}
	return AIConnectionSummary{Provider: c.Provider, Model: c.Model, Host: host}, true
}

// CompleteWorkAgentBounded is CompleteWorkAgent with a cap on the reply
// tokens, so an approved cost estimate bounds what is actually spent.
func CompleteWorkAgentBounded(ctx context.Context, d *kernel.Deps, connectionID, model, system, input string, maxTokens int) (string, error) {
	c, err := loadWorkAgentConfig(ctx, d, connectionID, model)
	if err != nil {
		return "", err
	}
	return aiCompleteBounded(ctx, c, system, input, maxTokens)
}

// AIDestinationFingerprint hashes everything that decides where an AI
// request goes and with which credential: for a saved connection its
// provider, endpoint, model, version and the encrypted secret as stored;
// for the writing settings (connectionID "") the ai.* rows including the
// encrypted key. No secret leaves this package; ok is false when the
// destination cannot be resolved.
func AIDestinationFingerprint(ctx context.Context, d *kernel.Deps, connectionID string) (string, bool) {
	h := &handlers{d: d}
	var parts []string
	if connectionID == "" {
		snap, err := aiSnapshot(ctx, d.DB)
		if err != nil || snap["ai.provider"] == "" {
			return "", false
		}
		parts = []string{"writing", snap["ai.provider"], snap["ai.base_url"], snap["ai.model"], snap["ai.api_key"]}
	} else {
		c, err := h.loadConnection(ctx, connectionID)
		if err != nil || c.Kind != "ai" {
			return "", false
		}
		parts = []string{"connection", c.ID, c.Version, c.Provider, c.Endpoint, c.Model, c.Secret}
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:]), true
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}
