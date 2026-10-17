package admin

import (
	"context"
	"errors"

	"github.com/zadstack/gorun/internal/kernel"
)

// CompleteWorkAgent reuses the configured server-side provider and encrypted
// connection registry. It executes a text-only turn, never tool calls.
func CompleteWorkAgent(ctx context.Context, d *kernel.Deps, connectionID, model, system, input string) (string, error) {
	c, err := loadWorkAgentConfig(ctx, d, connectionID, model)
	if err != nil {
		return "", err
	}
	return aiComplete(ctx, c, system, input)
}

// WorkAgentProviderReady verifies configuration and credential readability,
// without sending a request or claiming the provider is reachable.
func WorkAgentProviderReady(ctx context.Context, d *kernel.Deps, connectionID, model string) bool {
	_, err := loadWorkAgentConfig(ctx, d, connectionID, model)
	return err == nil
}

func loadWorkAgentConfig(ctx context.Context, d *kernel.Deps, connectionID, model string) (aiConfig, error) {
	h := &handlers{d: d}
	c, err := h.loadAIConfig(ctx)
	if connectionID != "" {
		connection, e := h.loadConnection(ctx, connectionID)
		if e != nil || connection.Kind != "ai" || validateConnection(connection) != nil {
			return aiConfig{}, errors.New("AI connection unavailable")
		}
		secret, e := h.connectionSecrets(connection)
		if e != nil {
			return aiConfig{}, errors.New("AI credential unavailable")
		}
		c = aiConfig{Provider: connection.Provider, BaseURL: connection.Endpoint, Model: connection.Model, APIKey: secret.Token, NamedConnection: true}
		err = nil
	}
	if err != nil {
		return aiConfig{}, errAINotConfigured
	}
	if model != "" {
		if !aiModelRe.MatchString(model) {
			return aiConfig{}, errors.New("invalid AI model")
		}
		c.Model = model
	}
	if c.Model == "" {
		return aiConfig{}, errAINotConfigured
	}
	return c, nil
}
