package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/mailer"
)

// AIConnectionSummary describes an AI destination for display in other
// modules (the optional identityai module): never a secret.
type AIConnectionSummary struct {
	ID, Name, Provider, Model, Host, TestStatus string
}

// ErrAIDestinationChanged is returned by CompleteWorkAgentBounded when the
// destination no longer matches the fingerprint that was reviewed.
var ErrAIDestinationChanged = errors.New("admin: ai destination changed since it was reviewed")

// ErrAIModelUnsupported is returned before any request for a model the
// shared client cannot drive: OpenAI reasoning models (gpt-5 family,
// o-series) require max_completion_tokens on chat/completions, which the
// client does not send.
var ErrAIModelUnsupported = errors.New("admin: the ai client cannot drive this model")

// AIClientCannotDrive reports such a model.
func AIClientCannotDrive(provider, model string) bool {
	if strings.ToLower(provider) != AIProviderOpenAI {
		return false
	}
	m := strings.ToLower(model)
	for _, prefix := range []string{"gpt-5", "o1", "o3", "o4"} {
		if strings.HasPrefix(m, prefix) {
			return true
		}
	}
	return false
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
// destination: provider, model and host, without the key.
func WritingAIDestination(ctx context.Context, d *kernel.Deps) (AIConnectionSummary, bool) {
	summary, _, ok := AIDestination(ctx, d, "")
	return summary, ok
}

// AIDestination returns, from one read, what the operator is shown for
// connectionID ("" is the writing settings) and the fingerprint of exactly
// those stored values. ok is false when the destination cannot be resolved
// (missing, invalid, or an unreadable credential).
func AIDestination(ctx context.Context, d *kernel.Deps, connectionID string) (AIConnectionSummary, string, bool) {
	dest, err := loadAIDestination(ctx, d, connectionID, "")
	if err != nil {
		return AIConnectionSummary{}, "", false
	}
	return dest.summary, dest.fingerprint(), true
}

// aiDestination is one destination as read in a single pass: the config a
// request is built from and the fingerprint of the very same stored values,
// so no settings change can slip between the check and the send.
type aiDestination struct {
	config  aiConfig
	summary AIConnectionSummary // what the operator is shown, from the same read
	parts   []string            // the stored values the fingerprint hashes (secrets in their encrypted form only)
}

// afterAIDestinationLoad is a test seam called after the single read and
// before the request is built; nil in production.
var afterAIDestinationLoad func()

// loadAIDestination reads the destination once. connectionID "" reads the
// ai.* rows in one statement (aiSnapshot) and builds the config from that
// snapshot; a saved connection is read once and both the config and the
// fingerprint come from that record. model overrides the model when set.
func loadAIDestination(ctx context.Context, d *kernel.Deps, connectionID, model string) (aiDestination, error) {
	h := &handlers{d: d}
	var dest aiDestination
	if connectionID == "" {
		snap, err := aiSnapshot(ctx, d.DB)
		if err != nil {
			return dest, err
		}
		get := func(k string) string {
			var v string
			_ = json.Unmarshal([]byte(snap[k]), &v)
			return v
		}
		c := aiConfig{Provider: get("ai.provider"), BaseURL: get("ai.base_url"), Model: get("ai.model")}
		if c.Provider != AIProviderOpenAI && c.Provider != AIProviderAnthropic {
			return dest, errAINotConfigured
		}
		if enc := get("ai.api_key"); enc != "" {
			key, err := mailer.DecryptSecret(d.Config.SecretKey, enc)
			if err != nil {
				return dest, errAINotConfigured
			}
			c.APIKey = key
		}
		if c.Provider == AIProviderOpenAI {
			base, ok := normalizeAIBaseURL(c.BaseURL)
			if !ok {
				return dest, errAINotConfigured
			}
			c.BaseURL = base
		}
		if c.Provider == AIProviderAnthropic && c.Model == "" {
			c.Model = defaultAnthropicModel
		}
		host := hostOf(c.BaseURL)
		if c.Provider == AIProviderAnthropic {
			host = hostOf(anthropicURL)
		}
		dest = aiDestination{config: c, summary: AIConnectionSummary{Provider: c.Provider, Model: c.Model, Host: host},
			parts: []string{"writing", snap["ai.provider"], snap["ai.base_url"], snap["ai.model"], snap["ai.api_key"]}}
	} else {
		c, err := h.loadConnection(ctx, connectionID)
		if err != nil || c.Kind != "ai" || validateConnection(c) != nil {
			return dest, errors.New("AI connection unavailable")
		}
		secret, err := h.connectionSecrets(c)
		if err != nil {
			return dest, errors.New("AI credential unavailable")
		}
		dest = aiDestination{config: aiConfig{Provider: c.Provider, BaseURL: c.Endpoint, Model: c.Model, APIKey: secret.Token, NamedConnection: true},
			summary: AIConnectionSummary{ID: c.ID, Name: c.Name, Provider: c.Provider, Model: c.Model, Host: hostOf(c.Endpoint), TestStatus: c.TestStatus},
			parts:   []string{"connection", c.ID, c.Version, c.Provider, c.Endpoint, c.Model, c.Secret}}
	}
	if model != "" {
		if !aiModelRe.MatchString(model) {
			return dest, errors.New("invalid AI model")
		}
		dest.config.Model = model
		dest.summary.Model = model
	}
	if dest.config.Model == "" {
		return dest, errAINotConfigured
	}
	if afterAIDestinationLoad != nil {
		afterAIDestinationLoad()
	}
	return dest, nil
}

func (a aiDestination) fingerprint() string {
	sum := sha256.Sum256([]byte(strings.Join(a.parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// AIDestinationFingerprint is the fingerprint part of AIDestination.
func AIDestinationFingerprint(ctx context.Context, d *kernel.Deps, connectionID string) (string, bool) {
	_, fp, ok := AIDestination(ctx, d, connectionID)
	return fp, ok
}

// CompleteWorkAgentBounded sends one bounded request to the destination
// read in a single pass. When expectFingerprint is not empty, the
// fingerprint of that same read must match it, so the request that goes
// out is built from exactly the values that were reviewed.
func CompleteWorkAgentBounded(ctx context.Context, d *kernel.Deps, connectionID, model, system, input string, maxTokens int, expectFingerprint string) (string, error) {
	dest, err := loadAIDestination(ctx, d, connectionID, "")
	if err != nil {
		return "", err
	}
	if expectFingerprint != "" {
		// The approval covers the stored values only: a different model
		// override is outside what was reviewed.
		if dest.fingerprint() != expectFingerprint || (model != "" && model != dest.config.Model) {
			return "", ErrAIDestinationChanged
		}
	} else if model != "" {
		if !aiModelRe.MatchString(model) {
			return "", errors.New("invalid AI model")
		}
		dest.config.Model = model
	}
	if AIClientCannotDrive(dest.config.Provider, dest.config.Model) {
		return "", ErrAIModelUnsupported
	}
	return aiCompleteBounded(ctx, dest.config, system, input, maxTokens)
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}
