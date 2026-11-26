package services

import (
	"context"
	"database/sql"
	"slices"

	"github.com/zadstack/gorun/internal/chat"
	"github.com/zadstack/gorun/internal/kernel"
)

// chatService is the live-chat widget (chat.*), read by the chat module.
// It has no secret: the website token is printed in every page.
type chatService struct{ d *kernel.Deps }

// Chat is the chat service: provider (chatwoot), base_url (a bare https
// origin, stored without a trailing slash) and website_token.
func Chat(d *kernel.Deps) Service { return chatService{d: d} }

func (chatService) Name() string         { return "chat" }
func (chatService) Providers() []string  { return slices.Clone(chat.Providers) }
func (chatService) Secret(string) string { return "" }

func (chatService) Fields() []Field {
	return []Field{
		{Name: "provider", Type: "enum", Required: true, Values: slices.Clone(chat.Providers)},
		{Name: "base_url", Type: "string", Required: true},
		{Name: "website_token", Type: "string", Required: true},
	}
}

// chatKeys maps each field to its setting.
var chatKeys = map[string]string{
	"provider":      chat.SettingProvider,
	"base_url":      chat.SettingBaseURL,
	"website_token": chat.SettingWebsiteToken,
}

// stored is the three fields as saved.
func (c chatService) stored(ctx context.Context) map[string]string {
	out := map[string]string{}
	for f, k := range chatKeys {
		out[f] = c.d.Settings.GetString(ctx, k, "")
	}
	return out
}

// Status is off without a provider, incomplete while a field is missing,
// else configured. The token is public and shown as is.
func (c chatService) Status(ctx context.Context) (Status, error) {
	v := c.stored(ctx)
	st := Status{Name: "chat", Provider: v["provider"], State: StateOff, Missing: []string{}, Source: "settings",
		Fields: map[string]any{"base_url": v["base_url"], "website_token": v["website_token"]}}
	if v["provider"] == "" {
		return st, nil
	}
	for _, f := range []string{"base_url", "website_token"} {
		if v[f] == "" {
			st.Missing = append(st.Missing, f)
		}
	}
	st.State = StateConfigured
	if len(st.Missing) > 0 {
		st.State = StateIncomplete
	}
	return st, nil
}

// Apply merges values over what is stored and validates the result. An
// empty value clears that field. A secret is refused: chat has none.
func (c chatService) Apply(ctx context.Context, tx *sql.Tx, values map[string]string, secret *string) (map[string]string, error) {
	errs := map[string]string{}
	if secret != nil {
		errs["secret"] = "unsupported"
	}
	v := c.stored(ctx)
	for f, val := range values {
		if _, ok := chatKeys[f]; !ok {
			errs[f] = "unknown"
			continue
		}
		v[f] = val
	}
	if p := v["provider"]; p != "" && !slices.Contains(chat.Providers, p) {
		errs["provider"] = "invalid"
	}
	if b := v["base_url"]; b != "" {
		if n, ok := chat.NormalizeBaseURL(b); ok {
			v["base_url"] = n
		} else {
			errs["base_url"] = "invalid"
		}
	}
	if t := v["website_token"]; t != "" && !chat.ValidToken(t) {
		errs["website_token"] = "invalid"
	}
	if len(errs) > 0 {
		return errs, nil
	}
	for f, k := range chatKeys {
		if err := c.d.Settings.SetTx(ctx, tx, k, v[f]); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// Test has nothing safe to try from the server: the widget runs in the
// visitor's browser.
func (chatService) Test(context.Context, string) (TestResult, error) {
	return TestResult{OK: true, Message: "no test"}, nil
}

// Off removes every chat setting; the widget and its CSP sources go with it.
func (c chatService) Off(ctx context.Context, tx *sql.Tx) error {
	return c.d.Settings.DeletePrefixTx(ctx, tx, chat.SettingPrefix)
}
