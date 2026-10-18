package admin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/mailer"
)

// AI writing providers (setting ai.provider; "" turns the helper off).
const (
	AIProviderOpenAI    = "openai"
	AIProviderAnthropic = "anthropic"

	exampleAIBaseURL      = "https://api.openai.com/v1"
	defaultAnthropicModel = "claude-sonnet-5-5"
	maxAIBaseURLLen       = 300
	aiSettingAPIKey       = "ai.api_key" // encrypted (mailer.EncryptSecret), write-only
)

var (
	aiProviders      = []string{"", AIProviderOpenAI, AIProviderAnthropic}
	aiProviderLabels = map[string]string{"": "admin.ai.provider_off", AIProviderOpenAI: "admin.ai.provider_openai",
		AIProviderAnthropic: "admin.ai.provider_anthropic"}
	aiModelRe = regexp.MustCompile(`^[A-Za-z0-9._:/@-]{1,128}$`)
	// aiKeyRe keeps the key a single printable header token.
	aiKeyRe = regexp.MustCompile(`^[\x21-\x7e]{1,512}$`)
)

// aiInput is the Settings → AI form. A blank APIKey keeps the stored one;
// ClearKey removes it.
type aiInput struct {
	EditToken string `form:"edit_token"`
	Provider  string `form:"provider"`
	BaseURL   string `form:"base_url"`
	Model     string `form:"model"`
	APIKey    string `form:"api_key"`
	ClearKey  bool   `form:"clear_key"`
}

type aiOption struct{ Value, LabelKey string }

type aiPage struct {
	Form       aiInput
	Fields     map[string]string
	Providers  []aiOption
	KeySet     bool
	Configured bool
	DefaultURL string
}

// normalizeAIBaseURL accepts an https URL (http only for localhost) with an
// optional path and no credentials, query or fragment; it returns it without
// a trailing slash.
func normalizeAIBaseURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || len(raw) > maxAIBaseURLLen || u.Host == "" || u.Hostname() == "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return "", false
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLocalHost(u.Hostname()) {
			return "", false
		}
	default:
		return "", false
	}
	return u.Scheme + "://" + u.Host + strings.TrimRight(u.EscapedPath(), "/"), true
}

// isLocalHost reports the loopback names an AI endpoint may use over http
// (and may be dialled although private addresses are otherwise refused).
func isLocalHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// validateAI returns the settings to store and field errors (i18n keys).
// A provider is only stored together with a model (and, for Anthropic, an
// API key), so a non-empty ai.provider means the helper menu can appear.
// Saving never proves generation works.
func (h *handlers) validateAI(ctx context.Context, in aiInput) (map[string]any, map[string]string) {
	keyStored := h.d.Settings.GetString(ctx, aiSettingAPIKey, "") != ""
	if in.ClearKey {
		vals, fields := AIConfig(in.Provider, in.BaseURL, in.Model, "", false, h.d.Config.SecretKey)
		vals[aiSettingAPIKey] = ""
		return vals, fields
	}
	return AIConfig(in.Provider, in.BaseURL, in.Model, in.APIKey, keyStored, h.d.Config.SecretKey)
}

// AIConfig validates an AI writing configuration exactly as Settings → AI
// does and returns the settings to store (the API key encrypted with
// secretKey; omitted when apiKey is blank) and field errors as i18n keys.
// keyStored says whether a key is already stored. The CLI uses it too.
func AIConfig(provider, baseURL, model, apiKey string, keyStored bool, secretKey []byte) (map[string]any, map[string]string) {
	fields := map[string]string{}
	vals := map[string]any{}
	provider = strings.TrimSpace(provider)
	if !slices.Contains(aiProviders, provider) {
		fields["provider"] = "admin.settings.error_ai_provider"
	}
	vals["ai.provider"] = provider

	base := strings.TrimSpace(baseURL)
	if base == "" && provider == AIProviderOpenAI {
		fields["base_url"] = "admin.settings.error_ai_base_url_required"
	}
	if base != "" {
		if u, ok := normalizeAIBaseURL(base); ok {
			base = u
		} else {
			fields["base_url"] = "admin.settings.error_ai_base_url"
		}
	}
	vals["ai.base_url"] = base

	model = strings.TrimSpace(model)
	if model == "" && provider == AIProviderAnthropic {
		model = defaultAnthropicModel
	}
	switch {
	case model == "" && provider != "":
		fields["model"] = "admin.settings.error_ai_model_required"
	case model != "" && !aiModelRe.MatchString(model):
		fields["model"] = "admin.settings.error_ai_model"
	}
	vals["ai.model"] = model

	key := strings.TrimSpace(apiKey)
	switch {
	case key != "" && !aiKeyRe.MatchString(key):
		fields["api_key"] = "admin.settings.error_ai_key"
	case key != "":
		enc, err := mailer.EncryptSecret(secretKey, key)
		if err != nil {
			fields["api_key"] = "admin.settings.error_encrypt"
		} else {
			vals[aiSettingAPIKey] = enc
			keyStored = true
		}
	}
	// A local OpenAI-compatible server may need no key; Anthropic always does.
	if provider == AIProviderAnthropic && !keyStored && fields["api_key"] == "" {
		fields["api_key"] = "admin.settings.error_ai_key_required"
	}
	return vals, fields
}

func (h *handlers) renderAI(w http.ResponseWriter, r *http.Request, status int, form aiInput, fields map[string]string) {
	form.APIKey = ""
	opts := make([]aiOption, 0, len(aiProviders))
	for _, p := range aiProviders {
		opts = append(opts, aiOption{Value: p, LabelKey: aiProviderLabels[p]})
	}
	ctx := r.Context()
	h.tpl.RenderWith(w, r, status, "admin", "pages/settings/ai.html", aiPage{Form: form, Fields: fields, Providers: opts,
		KeySet:     h.d.Settings.GetString(ctx, aiSettingAPIKey, "") != "",
		Configured: h.d.Settings.GetString(ctx, "ai.provider", "") != "", DefaultURL: exampleAIBaseURL})
}

func (h *handlers) settingsAI(w http.ResponseWriter, r *http.Request) {
	snap, err := aiSnapshot(r.Context(), h.d.DB)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.renderAI(w, r, http.StatusOK, aiFromSnapshot(snap), nil)
}

func (h *handlers) settingsAISave(w http.ResponseWriter, r *http.Request) {
	var in aiInput
	if err := httpx.Bind(r, &in); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	vals, fields := h.validateAI(r.Context(), in)
	if len(fields) > 0 {
		h.renderAI(w, r, http.StatusUnprocessableEntity, in, fields)
		return
	}
	if err := h.saveAISettings(r.Context(), vals, in.EditToken); err != nil {
		if errors.Is(err, errAIConflict) {
			h.renderAI(w, r, http.StatusConflict, in, map[string]string{"conflict": "connectionsetup.ai_conflict"})
			return
		}
		h.serverError(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "admin.ai.saved")
	redirect(w, r, "/admin/settings/ai")
}
