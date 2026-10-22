package admin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/mailer"
)

func seedAIConnection(t *testing.T, h *handlers, ctx context.Context, endpoint string) connectionRecord {
	t.Helper()
	secret, err := h.encryptConnectionSecrets(connectionSecrets{Token: "hidden-provider-key"})
	if err != nil {
		t.Fatal(err)
	}
	c := connectionRecord{ID: db.NewID(), Version: db.NewID(), Name: "Provider", Kind: "ai", Provider: "openai", Endpoint: endpoint, Model: "gpt-4o-mini", Secret: secret}
	if err := h.d.Settings.Set(ctx, connectionPrefix+c.ID, c); err != nil {
		t.Fatal(err)
	}
	return c
}

// The fingerprint changes with every stored value that decides the
// destination or the credential, and never contains a plaintext secret.
func TestAIDestinationFingerprintTracksConnectionAndWritingSettings(t *testing.T) {
	h, ctx, _, _ := canvasOperationFixture(t)
	c := seedAIConnection(t, h, ctx, "https://provider.example/v1")
	base, ok := AIDestinationFingerprint(ctx, h.d, c.ID)
	if !ok || base == "" {
		t.Fatal("no fingerprint for a valid connection")
	}
	dest, _ := loadAIDestination(ctx, h.d, c.ID, "")
	if strings.Contains(strings.Join(dest.parts, ""), "hidden-provider-key") {
		t.Fatal("plaintext secret in the fingerprint input")
	}
	newSecret, _ := h.encryptConnectionSecrets(connectionSecrets{Token: "another-key"})
	for name, mutate := range map[string]func(*connectionRecord){
		"endpoint": func(r *connectionRecord) { r.Endpoint = "https://other.example/v1" },
		"model":    func(r *connectionRecord) { r.Model = "gpt-4o" },
		"secret":   func(r *connectionRecord) { r.Secret = newSecret },
		"version":  func(r *connectionRecord) { r.Version = db.NewID() },
		"provider": func(r *connectionRecord) {
			r.Provider = "anthropic"
			r.Endpoint = "https://api.anthropic.com/v1"
			r.Model = "claude-sonnet-5-5"
		},
	} {
		next := c
		mutate(&next)
		if err := h.d.Settings.Set(ctx, connectionPrefix+c.ID, next); err != nil {
			t.Fatal(err)
		}
		got, ok := AIDestinationFingerprint(ctx, h.d, c.ID)
		if !ok || got == base {
			t.Fatalf("%s change not reflected (ok=%v)", name, ok)
		}
		if err := h.d.Settings.Set(ctx, connectionPrefix+c.ID, c); err != nil {
			t.Fatal(err)
		}
	}
	if again, _ := AIDestinationFingerprint(ctx, h.d, c.ID); again != base {
		t.Fatal("fingerprint not stable for unchanged values")
	}
	if _, ok := AIDestinationFingerprint(ctx, h.d, "missing"); ok {
		t.Fatal("unknown connection must not fingerprint")
	}

	// Writing settings path: provider, base url, model and the encrypted key.
	enc, _ := mailer.EncryptSecret(h.d.Config.SecretKey, "writing-plain-key")
	for k, v := range map[string]any{"ai.provider": "openai", "ai.base_url": "https://writing.example/v1", "ai.model": "gpt-4o-mini", "ai.api_key": enc} {
		if err := h.d.Settings.Set(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	wbase, ok := AIDestinationFingerprint(ctx, h.d, "")
	if !ok {
		t.Fatal("no fingerprint for writing settings")
	}
	wdest, _ := loadAIDestination(ctx, h.d, "", "")
	if strings.Contains(strings.Join(wdest.parts, ""), "writing-plain-key") {
		t.Fatal("plaintext writing key in the fingerprint input")
	}
	wd, ok := WritingAIDestination(ctx, h.d)
	if !ok || wd.Provider != "openai" || wd.Model != "gpt-4o-mini" || wd.Host != "writing.example" {
		t.Fatalf("writing destination = %+v ok=%v", wd, ok)
	}
	enc2, _ := mailer.EncryptSecret(h.d.Config.SecretKey, "writing-plain-key-2")
	for k, v := range map[string]any{"ai.provider": "anthropic", "ai.base_url": "https://writing2.example/v1", "ai.model": "gpt-4o", "ai.api_key": enc2} {
		prev := h.d.Settings.GetString(ctx, k, "")
		if err := h.d.Settings.Set(ctx, k, v); err != nil {
			t.Fatal(err)
		}
		got, _ := AIDestinationFingerprint(ctx, h.d, "")
		if got == wbase {
			t.Fatalf("%s change not reflected", k)
		}
		if err := h.d.Settings.Set(ctx, k, prev); err != nil {
			t.Fatal(err)
		}
	}
}

// The bounded call sends the cap it was given and refuses when the
// destination changed since the reviewed fingerprint, from one read. The
// writing-settings path is used because it accepts a local http server;
// saved connections must be https.
func TestCompleteWorkAgentBoundedCapsAndChecksFingerprint(t *testing.T) {
	h, ctx, _, _ := canvasOperationFixture(t)
	var seen map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &seen)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": `{"accent":"#c2410c"}`}}}})
	}))
	defer srv.Close()
	enc, _ := mailer.EncryptSecret(h.d.Config.SecretKey, "writing-plain-key")
	for k, v := range map[string]any{"ai.provider": "openai", "ai.base_url": srv.URL, "ai.model": "gpt-4o-mini", "ai.api_key": enc} {
		if err := h.d.Settings.Set(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	fp, ok := AIDestinationFingerprint(ctx, h.d, "")
	if !ok {
		t.Fatal("no fingerprint")
	}
	out, err := CompleteWorkAgentBounded(ctx, h.d, "", "", "sys", "user", 1500, fp)
	if err != nil || !strings.Contains(out, "#c2410c") {
		t.Fatalf("bounded call: %v %q", err, out)
	}
	if got, _ := seen["max_tokens"].(float64); got != 1500 {
		t.Fatalf("max_tokens sent = %v", seen["max_tokens"])
	}
	if err := h.d.Settings.Set(ctx, "ai.model", "gpt-4o"); err != nil {
		t.Fatal(err)
	}
	seen = nil
	if _, err := CompleteWorkAgentBounded(ctx, h.d, "", "", "sys", "user", 1500, fp); err != ErrAIDestinationChanged {
		t.Fatalf("changed destination err = %v", err)
	}
	if seen != nil {
		t.Fatal("request sent despite a changed destination")
	}
}

// The fingerprint and the request come from one read: a settings change
// after that read cannot make an unreviewed configuration pass the check.
func TestCompleteWorkAgentBoundedFingerprintAndRequestShareOneRead(t *testing.T) {
	h, ctx, _, _ := canvasOperationFixture(t)
	var sentModels []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		sentModels = append(sentModels, body["model"].(string))
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "{}"}}}})
	}))
	defer srv.Close()
	enc, _ := mailer.EncryptSecret(h.d.Config.SecretKey, "writing-plain-key")
	set := func(model string) {
		for k, v := range map[string]any{"ai.provider": "openai", "ai.base_url": srv.URL, "ai.model": model, "ai.api_key": enc} {
			if err := h.d.Settings.Set(ctx, k, v); err != nil {
				t.Fatal(err)
			}
		}
	}
	set("gpt-4o-mini") // configuration A, reviewed
	approved, _ := AIDestinationFingerprint(ctx, h.d, "")
	defer func() { afterAIDestinationLoad = nil }()

	// Stale read: the call reads B (changed after the review); restoring A
	// between the read and the send must not rescue it, because the check
	// uses the values the request would be built from.
	set("gpt-4o")
	afterAIDestinationLoad = func() { set("gpt-4o-mini") }
	if _, err := CompleteWorkAgentBounded(ctx, h.d, "", "", "sys", "user", 100, approved); err != ErrAIDestinationChanged || len(sentModels) != 0 {
		t.Fatalf("stale configuration sent: err=%v sent=%v", err, sentModels)
	}

	// Change after the read: the request that goes out is the reviewed one.
	afterAIDestinationLoad = func() { set("gpt-4o") }
	if _, err := CompleteWorkAgentBounded(ctx, h.d, "", "", "sys", "user", 100, approved); err != nil || len(sentModels) != 1 || sentModels[0] != "gpt-4o-mini" {
		t.Fatalf("request not built from the reviewed read: err=%v sent=%v", err, sentModels)
	}
}

// Shaped after the reviewer's reproduction: with ai.model flipping between
// the approved value and another one concurrently, no request may ever
// carry the unapproved model; calls that read the other value are refused.
func TestCompleteWorkAgentBoundedNeverSendsUnapprovedModelUnderConcurrentChange(t *testing.T) {
	h, ctx, _, _ := canvasOperationFixture(t)
	var wrong, sent atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		sent.Add(1)
		if body["model"] != "gpt-4o-mini" {
			wrong.Add(1)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "{}"}}}})
	}))
	defer srv.Close()
	enc, _ := mailer.EncryptSecret(h.d.Config.SecretKey, "fixture-key")
	for k, v := range map[string]any{"ai.provider": "openai", "ai.base_url": srv.URL, "ai.model": "gpt-4o-mini", "ai.api_key": enc} {
		if err := h.d.Settings.Set(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	approved, _ := AIDestinationFingerprint(ctx, h.d, "")
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = h.d.Settings.Set(ctx, "ai.model", "gpt-4o")
			_ = h.d.Settings.Set(ctx, "ai.model", "gpt-4o-mini")
		}
	}()
	refused := 0
	for i := 0; i < 300; i++ {
		if _, err := CompleteWorkAgentBounded(ctx, h.d, "", "", "system", "input", 100, approved); err == ErrAIDestinationChanged {
			refused++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	if wrong.Load() != 0 {
		t.Fatalf("%d requests carried the unapproved model (sent=%d refused=%d)", wrong.Load(), sent.Load(), refused)
	}
	t.Logf("sent=%d refused=%d, none with the unapproved model", sent.Load(), refused)
}

// An approval covers the stored model: a different model override is
// refused without a request.
func TestCompleteWorkAgentBoundedRefusesModelOverrideOutsideApproval(t *testing.T) {
	h, ctx, _, _ := canvasOperationFixture(t)
	var seen map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&seen)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "{}"}}}})
	}))
	defer srv.Close()
	enc, _ := mailer.EncryptSecret(h.d.Config.SecretKey, "fixture-key")
	for k, v := range map[string]any{"ai.provider": "openai", "ai.base_url": srv.URL, "ai.model": "gpt-4o-mini", "ai.api_key": enc} {
		if err := h.d.Settings.Set(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	fp, _ := AIDestinationFingerprint(ctx, h.d, "")
	if _, err := CompleteWorkAgentBounded(ctx, h.d, "", "gpt-4o", "system", "input", 100, fp); err != ErrAIDestinationChanged || seen != nil {
		t.Fatalf("override accepted: err=%v seen=%v", err, seen)
	}
	if _, err := CompleteWorkAgentBounded(ctx, h.d, "", "gpt-4o-mini", "system", "input", 100, fp); err != nil || seen["model"] != "gpt-4o-mini" {
		t.Fatalf("matching override refused: err=%v seen=%v", err, seen)
	}
}
