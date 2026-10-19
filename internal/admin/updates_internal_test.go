package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zadstack/gorun/internal/config"
)

func TestUpdateEndpointOnlyAcceptsOperatorHTTPSOrigin(t *testing.T) {
	valid := config.Config{UpdateURL: "https://coolify.example", UpdateToken: "test-token", UpdateResource: "site_123"}
	if got, err := updateEndpoint(valid); err != nil || got != "https://coolify.example/api/v1/deploy" {
		t.Fatalf("valid origin: %q %v", got, err)
	}
	for _, value := range []string{"http://coolify.example", "https://user:pass@coolify.example", "https://coolify.example/path", "https://coolify.example?uuid=other", "https://coolify.example#fragment", "https://"} {
		invalid := valid
		invalid.UpdateURL = value
		if _, err := updateEndpoint(invalid); err == nil {
			t.Errorf("accepted unsafe origin %q", value)
		}
	}
	for _, resource := range []string{"", "one,two", "../other", "site?force=true"} {
		invalid := valid
		invalid.UpdateResource = resource
		if _, err := updateEndpoint(invalid); err == nil {
			t.Errorf("accepted resource %q", resource)
		}
	}
}

func TestUpdateDispatchRequiresExplicitResourceAcceptance(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		reply    string
		accepted bool
	}{
		{"queued", 200, `{"deployments":[{"resource_uuid":"site_123","deployment_uuid":"job123"}]}`, true},
		{"unrelated resource", 200, `{"deployments":[{"resource_uuid":"other","deployment_uuid":"job123"}]}`, false},
		{"no deployment", 200, `{"deployments":[{"resource_uuid":"site_123"}]}`, false},
		{"invalid json", 200, `not json`, false},
		{"unauthorized", 401, `secret upstream message`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/api/v1/deploy" || r.Header.Get("Authorization") != "Bearer fake-token" || r.Header.Get("Content-Type") != "application/json" {
					t.Error("wrong deployment protocol")
				}
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 1 || body["uuid"] != "site_123" {
					t.Error("request did not exclusively target configured resource")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.reply))
			}))
			defer server.Close()
			c := config.Config{UpdateURL: server.URL, UpdateToken: "fake-token", UpdateResource: "site_123"}
			endpoint, err := updateEndpoint(c)
			if err != nil {
				t.Fatal(err)
			}
			if got := dispatchUpdate(context.Background(), c, endpoint, server.Client()); got != tc.accepted {
				t.Errorf("acceptance=%v", got)
			}
		})
	}
}
