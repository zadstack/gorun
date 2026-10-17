package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/httpx"
)

func TestAIHeaderBudgetDoesNotAlterSafeClientDefaults(t *testing.T) {
	client := aiHTTPClient("https://models.example.test/v1/chat/completions")
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("AI client lost guarded transport")
	}
	if client.Timeout != aiTimeout || transport.ResponseHeaderTimeout != client.Timeout {
		t.Fatalf("inference header budget=%v whole=%v", transport.ResponseHeaderTimeout, client.Timeout)
	}
	if transport.Proxy != nil || transport.DialContext == nil || transport.TLSHandshakeTimeout != 10*time.Second {
		t.Fatal("AI timeout update changed transport protection")
	}
	safe := httpx.NewSafeClient(false).Transport.(*http.Transport)
	if safe.ResponseHeaderTimeout != 20*time.Second {
		t.Fatal("generic outbound clients were broadened")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("private server reached by nonlocal AI endpoint")
	}))
	defer server.Close()
	if _, e := client.Get(server.URL); e == nil || !strings.Contains(e.Error(), "blocked") {
		t.Fatalf("guarded dial changed: %v", e)
	}
	if client.CheckRedirect(&http.Request{}, nil) != http.ErrUseLastResponse {
		t.Fatal("redirect policy changed")
	}
}

// Regression: local non-streaming providers can take longer than SafeClient's 20s header deadline.
func TestAILocalInferenceWaitsBeyondGenericHeaderDeadline(t *testing.T) {
	if testing.Short() {
		t.Skip("21s non-streaming inference regression")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(21 * time.Second):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"choices":[{"message":{"content":"completed delayed inference"}}]}`))
		case <-r.Context().Done():
			return
		}
	}))
	defer server.Close()
	result, e := aiComplete(context.Background(), aiConfig{Provider: AIProviderOpenAI, BaseURL: server.URL + "/v1", Model: "local-test"}, "System", "Input")
	if e != nil || result != "completed delayed inference" {
		t.Fatalf("model headers still hit generic deadline: %q %v", result, e)
	}
}
func TestAILocalRedirectNeverForwardsKeyAndContextStillBoundsInference(t *testing.T) {
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls++
		t.Error("AI credentials followed a redirect")
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer source.Close()
	request, _ := http.NewRequest("GET", source.URL, nil)
	request.Header.Set("Authorization", "Bearer synthetic-test-key")
	response, e := aiHTTPClient(source.URL).Do(request)
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 307 || targetCalls != 0 {
		t.Fatal("AI followed redirect")
	}
	waiting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer waiting.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	request, _ = http.NewRequestWithContext(ctx, "GET", waiting.URL, nil)
	if _, e = aiHTTPClient(waiting.URL).Do(request); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("caller cancellation was ignored: %v", e)
	}
}
