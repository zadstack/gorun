package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/mailer"
)

const (
	aiTimeout          = 90 * time.Second
	aiMaxTokens        = 8192
	aiMaxResponse      = 4 << 20
	aiMaxUpstreamError = 300 // runes of an upstream error message shown to the user
	anthropicVersion   = "2023-06-01"
)

// anthropicURL is the Messages API endpoint (tests point it at a fake).
var anthropicURL = "https://api.anthropic.com/v1/messages"

// errAINotConfigured means ai.provider is off (or its key unreadable).
var errAINotConfigured = errors.New("admin: AI writing is not configured")

// aiConfig is the decrypted AI provider configuration.
type aiConfig struct {
	Provider, BaseURL, Model, APIKey string
	NamedConnection                  bool
}

// upstreamError is a non-2xx answer from the provider; Message is already
// redacted and truncated.
type upstreamError struct {
	Status  int
	Message string
}

func (e *upstreamError) Error() string {
	return fmt.Sprintf("AI provider status %d: %s", e.Status, e.Message)
}

// loadAIConfig reads the ai.* settings and decrypts the key.
func (h *handlers) loadAIConfig(ctx context.Context) (aiConfig, error) {
	get := func(k string) string { return h.d.Settings.GetString(ctx, k, "") }
	c := aiConfig{Provider: get("ai.provider"), BaseURL: get("ai.base_url"), Model: get("ai.model")}
	if c.Provider != AIProviderOpenAI && c.Provider != AIProviderAnthropic {
		return c, errAINotConfigured
	}
	if enc := get(aiSettingAPIKey); enc != "" {
		key, err := mailer.DecryptSecret(h.d.Config.SecretKey, enc)
		if err != nil { // SecretKey changed since the key was saved
			h.d.Log.Warn("admin: ai key unreadable", "err", err)
			return c, errAINotConfigured
		}
		c.APIKey = key
	}
	if c.Provider == AIProviderOpenAI {
		base, ok := normalizeAIBaseURL(c.BaseURL)
		if !ok {
			return c, errAINotConfigured
		}
		c.BaseURL = base
	}
	if c.Provider == AIProviderAnthropic && c.Model == "" {
		c.Model = defaultAnthropicModel
	}
	return c, nil
}

// aiComplete sends one system + user turn to the provider and returns the
// reply text.
func aiComplete(ctx context.Context, c aiConfig, system, user string) (string, error) {
	return aiCompleteBounded(ctx, c, system, user, aiMaxTokens)
}

// aiCompleteBounded is aiComplete with an explicit output token cap.
func aiCompleteBounded(ctx context.Context, c aiConfig, system, user string, maxTokens int) (string, error) {
	var endpoint string
	var payload any
	headers := http.Header{"Content-Type": {"application/json"}}
	switch c.Provider {
	case AIProviderAnthropic:
		endpoint = anthropicURL
		if c.NamedConnection && c.BaseURL != "" {
			endpoint = strings.TrimRight(c.BaseURL, "/") + "/messages"
		}
		payload = map[string]any{"model": c.Model, "max_tokens": maxTokens, "system": system,
			"messages": []map[string]string{{"role": "user", "content": user}}}
		headers.Set("x-api-key", c.APIKey)
		headers.Set("anthropic-version", anthropicVersion)
	case AIProviderOpenAI:
		endpoint = c.BaseURL + "/chat/completions"
		payload = map[string]any{"model": c.Model, "max_tokens": maxTokens, "messages": []map[string]string{
			{"role": "system", "content": system}, {"role": "user", "content": user}}}
		if c.APIKey != "" {
			headers.Set("Authorization", "Bearer "+c.APIKey)
		}
	default:
		return "", errAINotConfigured
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("admin: ai request: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, aiTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("admin: ai request: %w", err)
	}
	req.Header = headers
	resp, err := aiHTTPClient(endpoint).Do(req)
	if err != nil {
		return "", fmt.Errorf("admin: ai call: %w", redactErr(err, c.APIKey))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, aiMaxResponse))
	if err != nil {
		return "", fmt.Errorf("admin: ai read: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", &upstreamError{Status: resp.StatusCode, Message: upstreamMessage(raw, c.APIKey)}
	}
	if c.Provider == AIProviderAnthropic {
		return parseAnthropic(raw)
	}
	return parseOpenAI(raw)
}

// aiHTTPClient refuses private addresses except for a loopback endpoint
// (a local model server) and never follows redirects, so the key headers
// cannot be forwarded to another host.
func aiHTTPClient(endpoint string) *http.Client {
	local := false
	if u, err := url.Parse(endpoint); err == nil {
		local = isLocalHost(u.Hostname())
	}
	c := httpx.NewSafeClientOpts(httpx.SafeClientOptions{AllowPrivate: local, Timeout: aiTimeout})
	// Non-streaming model inference may not send headers until generation completes.
	// Align this client only with the existing whole-request budget, preserving
	// SafeClient's guarded dialer, TLS budget and disabled proxy.
	if transport, ok := c.Transport.(*http.Transport); ok {
		transport.ResponseHeaderTimeout = aiTimeout
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}

func parseOpenAI(raw []byte) (string, error) {
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Choices) == 0 {
		return "", errors.New("admin: ai: unexpected OpenAI response")
	}
	return out.Choices[0].Message.Content, nil
}

func parseAnthropic(raw []byte) (string, error) {
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", errors.New("admin: ai: unexpected Anthropic response")
	}
	var b strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	if b.Len() == 0 {
		return "", errors.New("admin: ai: empty Anthropic response")
	}
	return b.String(), nil
}

// upstreamMessage extracts error.message (OpenAI and Anthropic both use it)
// or else the raw body, removes the key and truncates the result.
func upstreamMessage(raw []byte, key string) string {
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	msg := string(raw)
	if json.Unmarshal(raw, &env) == nil && env.Error.Message != "" {
		msg = env.Error.Message
	}
	return truncateRunes(strings.TrimSpace(redact(msg, key)), aiMaxUpstreamError)
}

func redact(s, key string) string {
	if key == "" {
		return s
	}
	return strings.ReplaceAll(s, key, "…")
}

func redactErr(err error, key string) error {
	if key == "" || !strings.Contains(err.Error(), key) {
		return err
	}
	return errors.New(redact(err.Error(), key))
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}
