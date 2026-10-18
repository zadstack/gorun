package admin_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/auth"
)

const testAIKey = "sk-test-SECRET-0123456789"

// fakeAI is an httptest provider; check inspects each request and reply is
// the response body for status.
type fakeAI struct {
	srv   *httptest.Server
	calls atomic.Int32
}

func newFakeAI(t *testing.T, status int, reply string, check func(t *testing.T, r *http.Request, body map[string]any)) *fakeAI {
	t.Helper()
	f := &fakeAI{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("provider got invalid JSON: %v", err)
		}
		if check != nil {
			check(t, r, body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, reply)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func openAIReply(text string) string {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": text}}}})
	return string(b)
}

// aiWrite posts to the endpoint and decodes the JSON answer.
func (s *site) aiWrite(form url.Values) (int, map[string]string) {
	s.t.Helper()
	resp, body := s.post("/admin/x/ai/write", form)
	out := map[string]string{}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		s.t.Fatalf("status %d: not JSON: %s", resp.StatusCode, body)
	}
	return resp.StatusCode, out
}

func configureAI(t *testing.T, s *site, form url.Values) {
	t.Helper()
	form.Set("edit_token", aiFormToken(t, s))
	if resp, page := s.post("/admin/settings/ai", form); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("ai settings save: %d\n%s", resp.StatusCode, page)
	}
}

func TestAISettingsValidateAndEncryptKey(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	ctx := context.Background()
	get := func(k string) string { return s.app.Deps.Settings.GetString(ctx, k, "") }

	resp, page := s.do("GET", "/admin/settings/ai", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("form: %d", resp.StatusCode)
	}
	mustContain(t, page, `href="/admin/settings/ai" aria-current="page"`, "No key stored.")

	for _, tc := range []struct {
		name string
		form url.Values
		want string
	}{
		{"unknown provider", url.Values{"provider": {"gemini"}, "model": {"m"}, "api_key": {"k"}}, "Choose a provider"},
		{"http remote", url.Values{"provider": {"openai"}, "base_url": {"http://example.com/v1"}, "model": {"m"}, "api_key": {"k"}}, "Enter an https URL"},
		{"query", url.Values{"provider": {"openai"}, "base_url": {"https://example.com/v1?x=1"}, "model": {"m"}, "api_key": {"k"}}, "Enter an https URL"},
		{"credentials", url.Values{"provider": {"openai"}, "base_url": {"https://u:p@example.com/v1"}, "model": {"m"}, "api_key": {"k"}}, "Enter an https URL"},
		{"bad model", url.Values{"provider": {"openai"}, "model": {"gpt 4"}, "api_key": {"k"}}, "Use up to 128 letters"},
		{"missing model", url.Values{"provider": {"openai"}, "api_key": {"k"}}, "Enter the model name"},
		{"missing key", url.Values{"provider": {"anthropic"}}, "Enter an API key"},
		{"key with space", url.Values{"provider": {"anthropic"}, "api_key": {"a b"}}, "Enter the key without spaces"},
	} {
		resp, page := s.post("/admin/settings/ai", tc.form)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status %d", tc.name, resp.StatusCode)
		}
		mustContain(t, page, "field-error", tc.want)
		if get("ai.provider") != "" || get("ai.api_key") != "" {
			t.Fatalf("%s: nothing may be stored", tc.name)
		}
	}

	configureAI(t, s, url.Values{"provider": {"openai"}, "base_url": {"https://example.com/v1/"}, "model": {"gpt-4o-mini"}, "api_key": {testAIKey}})
	enc := get("ai.api_key")
	if enc == "" || strings.Contains(enc, testAIKey) {
		t.Fatalf("key must be stored encrypted: %q", enc)
	}
	if got := get("ai.base_url"); got != "https://example.com/v1" {
		t.Fatalf("base_url = %q", got)
	}
	_, page = s.do("GET", "/admin/settings/ai", nil)
	mustContain(t, page, "A key is stored (encrypted)", "Generation has not been verified")
	mustNotContain(t, page, testAIKey, enc)
	var meta string
	s.app.Deps.DB.QueryRow(`SELECT meta_json FROM audit_log WHERE action = 'settings.update' ORDER BY rowid DESC LIMIT 1`).Scan(&meta)
	if !strings.Contains(meta, "ai.api_key") || strings.Contains(meta, testAIKey) || strings.Contains(meta, enc) {
		t.Fatalf("audit meta must list keys, not values: %s", meta)
	}

	// A blank key keeps the stored one; anthropic defaults the model and an
	// empty base URL is fine.
	configureAI(t, s, url.Values{"provider": {"anthropic"}, "model": {""}, "api_key": {""}})
	if get("ai.api_key") != enc || get("ai.model") != "claude-sonnet-5-5" {
		t.Fatalf("blank key must keep %q; model %q", get("ai.api_key"), get("ai.model"))
	}
	// Removing the key while a provider is set is refused; turning off works.
	if resp, _ := s.post("/admin/settings/ai", url.Values{"provider": {"anthropic"}, "clear_key": {"on"}}); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("clear key with provider: %d", resp.StatusCode)
	}
	configureAI(t, s, url.Values{"provider": {""}, "clear_key": {"on"}})
	if get("ai.provider") != "" || get("ai.api_key") != "" {
		t.Fatal("turning off and clearing must store empty values")
	}
	// Local servers need no key, but the destination must always be explicit.
	configureAI(t, s, url.Values{"provider": {"openai"}, "base_url": {"http://localhost:11434/v1"}, "model": {"llama3.2:3b"}})
	if get("ai.provider") != "openai" || get("ai.api_key") != "" {
		t.Fatal("an OpenAI-compatible endpoint may be saved without a key")
	}
	if resp, _ := s.post("/admin/settings/ai", url.Values{"provider": {"openai"}, "base_url": {""}, "model": {"m"}}); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("missing destination accepted: %d", resp.StatusCode)
	}
	if got := get("ai.base_url"); got != "http://localhost:11434/v1" {
		t.Fatalf("refused configuration changed destination: %q", got)
	}
}

func TestAIWriteWithoutStoredEndpointIsDisabled(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	for key, value := range map[string]string{"ai.provider": "openai", "ai.model": "model", "ai.base_url": ""} {
		if err := s.app.Deps.Settings.Set(context.Background(), key, value); err != nil {
			t.Fatal(err)
		}
	}
	if status, _ := s.aiWrite(url.Values{"action": {"improve"}, "text": {"Private draft"}}); status != http.StatusServiceUnavailable {
		t.Fatalf("configuration without destination should be disabled: %d", status)
	}
}

func TestAISettingsRequirePermission(t *testing.T) {
	s := boot(t)
	s.login("ed@example.com", "editor", "en")
	if resp, _ := s.do("GET", "/admin/settings/ai", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("editor ai settings: %d", resp.StatusCode)
	}
	if resp, _ := s.post("/admin/settings/ai", url.Values{"provider": {""}}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("editor ai save: %d", resp.StatusCode)
	}
}

func TestAIWriteOpenAI(t *testing.T) {
	f := newFakeAI(t, http.StatusOK, openAIReply("```html\n<p>Better <strong>text</strong></p><script>alert(1)</script>\n```"),
		func(t *testing.T, r *http.Request, body map[string]any) {
			if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
				t.Errorf("request %s %s", r.Method, r.URL.Path)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer "+testAIKey {
				t.Errorf("Authorization = %q", got)
			}
			if body["model"] != "gpt-4o-mini" {
				t.Errorf("model = %v", body["model"])
			}
			msgs, _ := body["messages"].([]any)
			if len(msgs) != 2 {
				t.Fatalf("messages = %v", body["messages"])
			}
			sys, user := msgs[0].(map[string]any), msgs[1].(map[string]any)
			if sys["role"] != "system" || !strings.Contains(sys["content"].(string), "ONLY the resulting text") {
				t.Errorf("system message = %v", sys)
			}
			if user["role"] != "user" || !strings.Contains(user["content"].(string), "<p>draft text</p>") || !strings.Contains(user["content"].(string), "My page") {
				t.Errorf("user message = %v", user)
			}
		})
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	configureAI(t, s, url.Values{"provider": {"openai"}, "base_url": {f.srv.URL + "/v1"}, "model": {"gpt-4o-mini"}, "api_key": {testAIKey}})

	// A contributor (content.create/content.edit) may use it.
	s.login("writer@example.com", "contributor", "en")
	status, out := s.aiWrite(url.Values{"action": {"improve"}, "text": {"<p>draft text</p>"}, "title": {"My page"}})
	if status != http.StatusOK {
		t.Fatalf("improve: %d %v", status, out)
	}
	if out["text"] != "<p>Better <strong>text</strong></p>" {
		t.Fatalf("body result must be unfenced and sanitized: %q", out["text"])
	}
}

func TestAIWriteTitleIsPlainText(t *testing.T) {
	f := newFakeAI(t, http.StatusOK, openAIReply("\"<b>A   title</b>\nsecond line\""), nil)
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	configureAI(t, s, url.Values{"provider": {"openai"}, "base_url": {f.srv.URL}, "model": {"m"}, "api_key": {testAIKey}})
	status, out := s.aiWrite(url.Values{"action": {"title"}, "text": {"<p>body</p>"}})
	if status != http.StatusOK || out["text"] != "A title second line" {
		t.Fatalf("title: %d %q", status, out["text"])
	}
}

func TestAIWriteAnthropic(t *testing.T) {
	f := newFakeAI(t, http.StatusOK, `{"content":[{"type":"text","text":"<p>مرحبا</p>"},{"type":"text","text":"<p>بالعالم</p>"}]}`,
		func(t *testing.T, r *http.Request, body map[string]any) {
			if got := r.Header.Get("x-api-key"); got != testAIKey {
				t.Errorf("x-api-key = %q", got)
			}
			if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
				t.Errorf("anthropic-version = %q", got)
			}
			if r.Header.Get("Authorization") != "" {
				t.Error("Anthropic must not get an Authorization header")
			}
			if body["model"] != "claude-sonnet-5-5" {
				t.Errorf("model = %v", body["model"])
			}
			if n, _ := body["max_tokens"].(float64); n <= 0 {
				t.Errorf("max_tokens = %v", body["max_tokens"])
			}
			if sys, _ := body["system"].(string); !strings.Contains(sys, "ONLY the resulting text") {
				t.Errorf("system = %q", sys)
			}
			msgs, _ := body["messages"].([]any)
			if len(msgs) != 1 || !strings.Contains(msgs[0].(map[string]any)["content"].(string), "Translate the content into Modern Standard Arabic") {
				t.Errorf("messages = %v", body["messages"])
			}
		})
	defer admin.SetAnthropicURL(f.srv.URL + "/v1/messages")()
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	configureAI(t, s, url.Values{"provider": {"anthropic"}, "api_key": {testAIKey}})
	status, out := s.aiWrite(url.Values{"action": {"translate_ar"}, "text": {"<p>Hello world</p>"}})
	if status != http.StatusOK || out["text"] != "<p>مرحبا</p><p>بالعالم</p>" {
		t.Fatalf("anthropic: %d %v", status, out)
	}
	if f.calls.Load() != 1 {
		t.Fatalf("calls = %d", f.calls.Load())
	}
}

func TestAIWriteRefusals(t *testing.T) {
	f := newFakeAI(t, http.StatusOK, openAIReply("<p>ok</p>"), nil)
	s := boot(t)
	ctx := context.Background()
	db := s.app.Deps.DB
	db.Exec(`INSERT INTO roles (slug, name_json, system) VALUES ('viewer', '{"en":"Viewer"}', 0)`)
	db.Exec(`INSERT INTO role_permissions (role, permission) VALUES ('viewer', 'content.view')`)
	if err := auth.ReloadRoles(ctx, db); err != nil {
		t.Fatal(err)
	}
	s.login("admin@example.com", "admin", "en")

	if status, out := s.aiWrite(url.Values{"action": {"improve"}, "text": {"x"}}); status != http.StatusServiceUnavailable || !strings.Contains(out["error"], "not configured") {
		t.Fatalf("not configured: %d %v", status, out)
	}
	configureAI(t, s, url.Values{"provider": {"openai"}, "base_url": {f.srv.URL}, "model": {"m"}, "api_key": {testAIKey}})

	for _, tc := range []struct {
		name   string
		form   url.Values
		status int
	}{
		{"unknown action", url.Values{"action": {"poem"}, "text": {"x"}}, http.StatusBadRequest},
		{"empty text", url.Values{"action": {"improve"}, "text": {"  "}}, http.StatusBadRequest},
		{"too long", url.Values{"action": {"improve"}, "text": {strings.Repeat("ب", 20001)}}, http.StatusRequestEntityTooLarge},
	} {
		if status, out := s.aiWrite(tc.form); status != tc.status || out["error"] == "" {
			t.Fatalf("%s: %d %v", tc.name, status, out)
		}
	}
	if f.calls.Load() != 0 {
		t.Fatalf("invalid input must not reach the provider (%d calls)", f.calls.Load())
	}

	// CSRF: no token, no call.
	resp, _ := s.do("POST", "/admin/x/ai/write", url.Values{"action": {"improve"}, "text": {"x"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing csrf: %d", resp.StatusCode)
	}

	// A staff user without content.create / content.edit.
	s.login("viewer@example.com", "viewer", "en")
	if status, out := s.aiWrite(url.Values{"action": {"improve"}, "text": {"x"}}); status != http.StatusForbidden || out["error"] == "" {
		t.Fatalf("viewer: %d %v", status, out)
	}
	if f.calls.Load() != 0 {
		t.Fatalf("refused requests reached the provider (%d calls)", f.calls.Load())
	}
}

func TestAIWriteRateLimitPerUser(t *testing.T) {
	f := newFakeAI(t, http.StatusOK, openAIReply("<p>ok</p>"), nil)
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	configureAI(t, s, url.Values{"provider": {"openai"}, "base_url": {f.srv.URL}, "model": {"m"}, "api_key": {testAIKey}})
	form := url.Values{"action": {"fix"}, "text": {"<p>x</p>"}}
	for i := range 20 {
		if status, out := s.aiWrite(form); status != http.StatusOK {
			t.Fatalf("request %d: %d %v", i+1, status, out)
		}
	}
	if status, _ := s.aiWrite(form); status != http.StatusTooManyRequests {
		t.Fatalf("21st request: %d", status)
	}
	// Another user has their own budget.
	s.login("ed@example.com", "editor", "en")
	if status, _ := s.aiWrite(form); status != http.StatusOK {
		t.Fatalf("other user: %d", status)
	}
}

func TestAIWriteUpstreamErrorDoesNotLeak(t *testing.T) {
	long := `{"error":{"type":"authentication_error","message":"Incorrect API key provided: ` + testAIKey + `. ` + strings.Repeat("details ", 100) + `"}}`
	f := newFakeAI(t, http.StatusUnauthorized, long, nil)
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	configureAI(t, s, url.Values{"provider": {"openai"}, "base_url": {f.srv.URL}, "model": {"m"}, "api_key": {testAIKey}})
	status, out := s.aiWrite(url.Values{"action": {"improve"}, "text": {"<p>x</p>"}})
	if status != http.StatusBadGateway {
		t.Fatalf("status %d", status)
	}
	msg := out["error"]
	if !strings.Contains(msg, "401") || !strings.Contains(msg, "Incorrect API key provided") {
		t.Fatalf("error should explain the refusal: %q", msg)
	}
	if strings.Contains(msg, testAIKey) || strings.Contains(msg, "authentication_error") || len([]rune(msg)) > 420 {
		t.Fatalf("error leaks the key or the raw body: %q", msg)
	}
}

func TestAIWriteOpenAIWithoutKey(t *testing.T) {
	f := newFakeAI(t, http.StatusOK, openAIReply("<p>ok</p>"), func(t *testing.T, r *http.Request, _ map[string]any) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("no key configured, but Authorization = %q", got)
		}
	})
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	configureAI(t, s, url.Values{"provider": {"openai"}, "base_url": {f.srv.URL}, "model": {"m"}})
	if status, out := s.aiWrite(url.Values{"action": {"shorten"}, "text": {"<p>x</p>"}}); status != http.StatusOK || out["text"] != "<p>ok</p>" {
		t.Fatalf("no key: %d %v", status, out)
	}
}
