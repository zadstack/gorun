package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLayoutAdvisorPinsProviderAndUsesBoundedTextOnlyCompletion(t *testing.T) {
	h, ctx, _, _ := canvasOperationFixture(t)
	advisor := &builderLayoutAdvisor{h: h}
	if _, _, err := advisor.DescribeLayout(ctx); err == nil {
		t.Fatal("unconfigured provider appeared ready")
	}
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected provider path %s", r.URL.Path)
		}
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if input["max_tokens"] != float64(64) || input["tools"] != nil {
			t.Error("completion is not bounded text-only")
		}
		if !strings.Contains(string(mustMarshalJSON(t, input)), "candidate_ids") {
			t.Error("layout metadata missing")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"features:compact_rows"}}]}`))
	}))
	defer provider.Close()
	for key, value := range map[string]string{"ai.provider": "openai", "ai.base_url": provider.URL + "/v1", "ai.model": "test-model"} {
		if err := h.d.Settings.Set(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	destination, binding, err := advisor.DescribeLayout(ctx)
	if err != nil || destination == "" || binding == "" {
		t.Fatal("configured provider not described", err)
	}
	if calls.Load() != 0 {
		t.Fatal("describing provider transmitted data")
	}
	output, err := advisor.SuggestLayout(ctx, binding, `{"kind":"features","candidate_ids":["features:compact_rows"]}`)
	if err != nil || output != "features:compact_rows" || calls.Load() != 1 {
		t.Fatal("configured completion failed", err)
	}
	if err := h.d.Settings.Set(ctx, "ai.model", "changed-model"); err != nil {
		t.Fatal(err)
	}
	if _, err = advisor.SuggestLayout(ctx, binding, `{"kind":"features"}`); err == nil || calls.Load() != 1 {
		t.Fatal("changed destination consent was reused")
	}
}
func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
