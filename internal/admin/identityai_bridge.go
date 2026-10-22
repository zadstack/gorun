package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"

	"github.com/zadstack/gorun/internal/kernel"
)

// AIConnectionSummary describes an AI destination for display in other
// modules (the optional identityai module): never a secret.
type AIConnectionSummary struct {
	ID, Name, Provider, Model, Host, TestStatus string
}

// ErrAIDestinationChanged is returned by CompleteWorkAgentBounded when the
// destination no longer matches the fingerprint that was reviewed.
var ErrAIDestinationChanged = errors.New("admin: ai destination changed since it was reviewed")

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

// aiFingerprintParts are the stored values that decide where a request goes
// and with which credential. Secrets enter only in their encrypted stored
// form; the plaintext key is never part of the input.
func aiFingerprintParts(ctx context.Context, d *kernel.Deps, connectionID string) ([]string, bool) {
	h := &handlers{d: d}
	if connectionID == "" {
		snap, err := aiSnapshot(ctx, d.DB)
		if err != nil || snap["ai.provider"] == "" {
			return nil, false
		}
		return []string{"writing", snap["ai.provider"], snap["ai.base_url"], snap["ai.model"], snap["ai.api_key"]}, true
	}
	c, err := h.loadConnection(ctx, connectionID)
	if err != nil || c.Kind != "ai" {
		return nil, false
	}
	return []string{"connection", c.ID, c.Version, c.Provider, c.Endpoint, c.Model, c.Secret}, true
}

func fingerprintOf(parts []string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// AIDestinationFingerprint hashes everything that decides where an AI
// request goes and with which credential: for a saved connection its
// provider, endpoint, model, version and the encrypted secret as stored;
// for the writing settings (connectionID "") the ai.* rows including the
// encrypted key. ok is false when the destination cannot be resolved.
func AIDestinationFingerprint(ctx context.Context, d *kernel.Deps, connectionID string) (string, bool) {
	parts, ok := aiFingerprintParts(ctx, d, connectionID)
	if !ok {
		return "", false
	}
	return fingerprintOf(parts), true
}

// CompleteWorkAgentBounded is CompleteWorkAgent with a cap on the reply
// tokens and, when expectFingerprint is not empty, a check that the
// destination still matches what was reviewed, taken from the same read
// that builds the request (no window between the check and the send).
func CompleteWorkAgentBounded(ctx context.Context, d *kernel.Deps, connectionID, model, system, input string, maxTokens int, expectFingerprint string) (string, error) {
	c, err := loadWorkAgentConfig(ctx, d, connectionID, model)
	if err != nil {
		return "", err
	}
	if expectFingerprint != "" {
		parts, ok := aiFingerprintParts(ctx, d, connectionID)
		if !ok || fingerprintOf(parts) != expectFingerprint {
			return "", ErrAIDestinationChanged
		}
	}
	return aiCompleteBounded(ctx, c, system, input, maxTokens)
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}
