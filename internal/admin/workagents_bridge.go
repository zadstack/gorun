package admin

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/zadstack/gorun/internal/kernel"
)

// ReviewCompletionRequest pins the approved destination and credentials to a
// single resolved configuration, rather than reloading it after checking consent.
type ReviewCompletionRequest struct {
	ConnectionID, Model, Binding, System, Input string
}

func reviewConfigBinding(d *kernel.Deps, c aiConfig, revision any) string {
	raw, _ := json.Marshal(struct {
		Config   aiConfig
		Revision any
	}{c, revision})
	mac := hmac.New(sha256.New, []byte(d.Config.SecretKey))
	mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}

func resolvedReviewConfig(ctx context.Context, d *kernel.Deps, connectionID, model string) (aiConfig, string, error) {
	h := &handlers{d: d}
	var c aiConfig
	var revision any
	if connectionID == "" {
		cfg, version, err := h.canvasAIConfig(ctx)
		if err != nil {
			return c, "", err
		}
		c, revision = cfg, version
	} else {
		connection, err := h.loadConnection(ctx, connectionID)
		if err != nil || connection.Kind != "ai" || validateConnection(connection) != nil {
			return c, "", errAINotConfigured
		}
		secret, err := h.connectionSecrets(connection)
		if err != nil {
			return c, "", errAINotConfigured
		}
		c = aiConfig{Provider: connection.Provider, BaseURL: connection.Endpoint, Model: connection.Model, APIKey: secret.Token, NamedConnection: true}
		revision = connection.ID + ":" + connection.Version
	}
	if model != "" {
		if !aiModelRe.MatchString(model) {
			return c, "", errAINotConfigured
		}
		c.Model = model
	}
	if c.Model == "" {
		return c, "", errAINotConfigured
	}
	return c, reviewConfigBinding(d, c, revision), nil
}

// WorkAgentReviewBinding reads configuration without contacting the provider.
// The opaque MAC includes credentials but cannot disclose them to API consumers.
func WorkAgentReviewBinding(ctx context.Context, d *kernel.Deps, connectionID, model string) (string, string, error) {
	c, binding, err := resolvedReviewConfig(ctx, d, connectionID, model)
	if err != nil {
		return "", "", err
	}
	destination := c.BaseURL + "/chat/completions"
	if c.Provider == AIProviderAnthropic {
		destination = anthropicURL
		if c.NamedConnection && c.BaseURL != "" {
			destination = strings.TrimRight(c.BaseURL, "/") + "/messages"
		}
	}
	return destination, binding, nil
}

func CompleteWorkAgentReview(ctx context.Context, d *kernel.Deps, request ReviewCompletionRequest) (string, error) {
	c, binding, err := resolvedReviewConfig(ctx, d, request.ConnectionID, request.Model)
	if err != nil {
		return "", err
	}
	if request.Binding == "" || !hmac.Equal([]byte(request.Binding), []byte(binding)) {
		return "", errors.New("review_config_changed")
	}
	output, err := aiComplete(ctx, c, request.System, request.Input)
	return redact(output, c.APIKey), err
}

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
