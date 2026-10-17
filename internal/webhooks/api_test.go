package webhooks_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/webhooks"
)

func TestWebhooksAPI(t *testing.T) {
	app := apitest.Boot(t, webhooks.New())
	c := apitest.NewClient(t, app, webhooks.Perm)
	if res := apitest.NewClient(t, app, "content.view").Do("GET", "/v1/webhooks", nil, nil); res.Code != http.StatusForbidden {
		t.Fatalf("without permission: %d", res.Code)
	}
	if res := c.Do("POST", "/v1/webhooks", map[string]any{"name": "x", "url": "ftp://x", "events": []string{"nope"}}, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid create: %d %s", res.Code, res.Body)
	}
	res := c.Do("POST", "/v1/webhooks", map[string]any{"name": "CRM", "url": "https://crm.example.com/hook", "events": []string{"content.published"}}, nil)
	var created struct {
		Data struct {
			Webhook webhooks.Webhook `json:"webhook"`
			Secret  string           `json:"secret"`
		} `json:"data"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &created)
	if res.Code != http.StatusCreated || created.Data.Secret == "" || created.Data.Webhook.ID == "" {
		t.Fatalf("create: %d %s", res.Code, res.Body)
	}
	id := created.Data.Webhook.ID
	if res := c.Do("GET", "/v1/webhooks", nil, nil); res.Code != http.StatusOK || containsSecret(res.Body.String(), created.Data.Secret) {
		t.Fatalf("list must not expose the secret: %d", res.Code)
	}
	if res := c.Do("PATCH", "/v1/webhooks/"+id, map[string]any{"active": false}, nil); res.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", res.Code, res.Body)
	}
	if res := c.Do("POST", "/v1/webhooks/"+id+"/test", nil, nil); res.Code != http.StatusConflict {
		t.Fatalf("testing a disabled webhook: %d %s", res.Code, res.Body)
	}
	if res := c.Do("POST", "/v1/webhook-deliveries/missing/redeliver", nil, nil); res.Code != http.StatusNotFound {
		t.Fatalf("redelivering an unknown delivery: %d %s", res.Code, res.Body)
	}
	if res := c.Do("POST", "/v1/webhooks/"+id+"/secret", nil, nil); res.Code != http.StatusOK || containsSecret(res.Body.String(), created.Data.Secret) {
		t.Fatalf("rotate: %d %s", res.Code, res.Body)
	}
	if res := c.Do("GET", "/v1/webhooks/events", nil, nil); res.Code != http.StatusOK {
		t.Fatalf("events: %d", res.Code)
	}
	if res := c.Do("DELETE", "/v1/webhooks/"+id, nil, nil); res.Code != http.StatusOK {
		t.Fatalf("delete: %d", res.Code)
	}
	if res := c.Do("GET", "/v1/webhooks/"+id, nil, nil); res.Code != http.StatusNotFound {
		t.Fatalf("get deleted: %d", res.Code)
	}
}

func containsSecret(body, secret string) bool { return secret != "" && strings.Contains(body, secret) }
