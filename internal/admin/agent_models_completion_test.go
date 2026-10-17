package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAgentNamedCompletionUsesChosenModelAndDestination(t *testing.T) {
	for _, provider := range []string{"openai", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			models := []string{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Model string `json:"model"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("bad payload")
				}
				models = append(models, body.Model)
				if provider == "anthropic" {
					if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "private-test-token" {
						t.Error("wrong destination/auth")
					}
					w.Write([]byte(`{"content":[{"type":"text","text":"draft"}]}`))
				} else {
					if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer private-test-token" {
						t.Error("wrong destination/auth")
					}
					w.Write([]byte(`{"choices":[{"message":{"content":"draft"}}]}`))
				}
			}))
			defer srv.Close()
			for _, model := range []string{"agent-one", "agent-two"} {
				text, e := aiComplete(context.Background(), aiConfig{Provider: provider, BaseURL: srv.URL + "/v1", Model: model, APIKey: "private-test-token", NamedConnection: true}, "system", "source")
				if e != nil || text != "draft" {
					t.Fatal(text, e)
				}
			}
			if len(models) != 2 || models[0] != "agent-one" || models[1] != "agent-two" {
				t.Fatal(models)
			}
		})
	}
}
