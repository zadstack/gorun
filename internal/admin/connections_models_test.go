package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConnectionModelDiscoveryBeforeSaveAndLargeCatalog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer private-key" {
			t.Error("unexpected request")
		}
		data := make([]map[string]string, 712)
		for i := range data {
			data[i] = map[string]string{"id": fmt.Sprintf("model-%03d", i)}
		}
		data[711]["id"] = "zad-deepseek"
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer srv.Close()
	rows, e := discoverAIModels(context.Background(), srv.Client(), connectionRecord{Provider: "openai", Endpoint: srv.URL + "/v1"}, connectionSecrets{Token: "private-key"})
	if e != nil || len(rows) != 712 || rows[711].ID != "zad-deepseek" {
		t.Fatalf("catalog count=%d err=%v", len(rows), e)
	}
}
func TestConnectionModelDiscoveryInvalidProviderResponses(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"invalid id", `{"data":[{"id":"<script>"}]}`, 200},
		{"secret echoed", `{"data":[{"id":"private-key"}]}`, 200},
		{"bad label", `{"data":[{"id":"good","display_name":"bad\u0000label"}]}`, 200},
		{"missing data", `{}`, 200}, {"empty data", `{"data":[]}`, 200},
		{"pagination incomplete", `{"data":[{"id":"good"}],"has_more":true}`, 200},
		{"provider private error", `{"error":"private-key"}`, 401},
		{"oversize", strings.Repeat("x", maxConnectionResponse+1), 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer srv.Close()
			_, e := discoverAIModels(context.Background(), srv.Client(), connectionRecord{Provider: "anthropic", Endpoint: srv.URL}, connectionSecrets{Token: "private-key"})
			if e == nil || strings.Contains(e.Error(), "private-key") {
				t.Fatalf("unsafe/missing error %v", e)
			}
		})
	}
}
func TestConnectionModelDiscoveryBoundAndAnthropicHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-key" || r.Header.Get("anthropic-version") != "2023-06-01" || r.Header.Get("Authorization") != "" {
			t.Error("wrong anthropic headers")
		}
		rows := make([]map[string]string, 1001)
		for i := range rows {
			rows[i] = map[string]string{"id": fmt.Sprint("m", i)}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": rows})
	}))
	defer srv.Close()
	_, e := discoverAIModels(context.Background(), srv.Client(), connectionRecord{Provider: "anthropic", Endpoint: srv.URL}, connectionSecrets{Token: "test-key"})
	if e == nil {
		t.Fatal("silently truncated large catalog")
	}
}
func TestConnectionModelDiscoveryAnthropicPagination(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			fmt.Fprint(w, `{"data":[{"id":"a","display_name":"Alpha"}],"has_more":true,"last_id":"a"}`)
		} else {
			if r.URL.Query().Get("after_id") != "a" {
				t.Error("wrong cursor")
			}
			fmt.Fprint(w, `{"data":[{"id":"b"}],"has_more":false}`)
		}
	}))
	defer srv.Close()
	rows, e := discoverAIModels(context.Background(), srv.Client(), connectionRecord{Provider: "anthropic", Endpoint: srv.URL}, connectionSecrets{})
	if e != nil || calls != 2 || len(rows) != 2 || rows[0].Label != "Alpha" {
		t.Fatalf("pagination calls=%d rows=%d err=%v", calls, len(rows), e)
	}
}

func TestConnectionModelCatalogMixedSupportedIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := make([]map[string]string, 0, 247)
		for i := 0; i < 142; i++ {
			data = append(data, map[string]string{"id": fmt.Sprintf("supported-%03d", i)})
		}
		data = append(data, map[string]string{"id": "zad-deepseek"}, map[string]string{"id": "zad-deepseek-flash"})
		for i := 0; i < 103; i++ {
			data = append(data, map[string]string{"id": fmt.Sprintf("unsupported model+%03d", i)})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer srv.Close()
	catalog, e := discoverAIModelCatalog(context.Background(), srv.Client(), connectionRecord{Provider: "openai", Endpoint: srv.URL}, connectionSecrets{Token: "private-token"})
	if e != nil || len(catalog.Models) != 144 || catalog.ExcludedCount != 103 {
		t.Fatalf("models=%d excluded=%d err=%v", len(catalog.Models), catalog.ExcludedCount, e)
	}
	if catalog.Models[142].ID != "zad-deepseek" || catalog.Models[143].ID != "zad-deepseek-flash" {
		t.Fatal("required models absent")
	}
	rows, e := discoverAIModels(context.Background(), srv.Client(), connectionRecord{Provider: "openai", Endpoint: srv.URL}, connectionSecrets{})
	if e != nil || len(rows) != 144 {
		t.Fatal("agent validation wrapper rejected mixed catalog", e)
	}
}
func TestConnectionModelCatalogFilteringCannotHideUnsafeResponse(t *testing.T) {
	for _, body := range []string{
		`{"data":[{"id":"okay"},{"id":"bad private-token"}]}`,
		`{"data":[{"id":"okay"},{"id":"bad id","display_name":"bad\u0000label"}]}`,
		`{"data":[{"id":"bad id"}]}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		_, e := discoverAIModelCatalog(context.Background(), srv.Client(), connectionRecord{Provider: "openai", Endpoint: srv.URL}, connectionSecrets{Token: "private-token"})
		srv.Close()
		if e == nil {
			t.Fatal("unsafe or empty filtered catalog accepted")
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := make([]map[string]string, 1001)
		for i := range data {
			data[i] = map[string]string{"id": "unsupported id"}
		}
		data[0]["id"] = "okay"
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer srv.Close()
	_, e := discoverAIModelCatalog(context.Background(), srv.Client(), connectionRecord{Provider: "openai", Endpoint: srv.URL}, connectionSecrets{})
	if e == nil {
		t.Fatal("filter concealed raw record bound")
	}
}
