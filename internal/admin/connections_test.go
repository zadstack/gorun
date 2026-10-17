package admin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestConnectionEndpointRejectsCredentialsAndPrivateNetworks(t *testing.T) {
	for _, endpoint := range []string{"http://example.com", "https://user:pass@example.com", "https://example.com/?key=secret", "https://127.0.0.1", "https://169.254.169.254", "https://[::1]", "https://localhost", "https://example.com/#secret"} {
		if _, err := normalizeConnectionEndpoint(endpoint); err == nil {
			t.Errorf("accepted %q", endpoint)
		}
	}
	if _, err := normalizeConnectionEndpoint("https://api.example.com/v1"); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionAIProbeChecksModelWithoutGenerating(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer private-token" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"id":"example-model"}]}`)
	}))
	defer srv.Close()
	c := connectionRecord{Kind: "ai", Provider: "openai", Endpoint: srv.URL + "/v1", Model: "example-model"}
	result, err := probeAIConnection(context.Background(), srv.Client(), c, connectionSecrets{Token: "private-token"})
	if err != nil || result.Count != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	c.Model = "missing"
	if _, err = probeAIConnection(context.Background(), srv.Client(), c, connectionSecrets{Token: "private-token"}); err == nil {
		t.Fatal("missing model passed")
	}
}

func TestConnectionMCPProbeOnlyDiscoversTools(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Method string `json:"method"`
		}
		if err := decodeConnectionJSON(r.Body, &body); err != nil {
			t.Error(err)
		}
		methods = append(methods, body.Method)
		w.Header().Set("Content-Type", "application/json")
		switch body.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "example-session")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"fake","version":"1"}}}`)
		case "notifications/initialized":
			w.WriteHeader(202)
		case "tools/list":
			if r.Header.Get("Mcp-Session-Id") != "example-session" {
				t.Error("missing session")
			}
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"read_status","inputSchema":{"type":"object"}}]}}`)
		default:
			t.Fatal("unexpected method", body.Method)
		}
	}))
	defer srv.Close()
	result, err := probeMCPConnection(context.Background(), srv.Client(), connectionRecord{Endpoint: srv.URL}, connectionSecrets{})
	if err != nil || result.Count != 1 || result.Protocol != "2025-11-25" {
		t.Fatalf("%+v %v", result, err)
	}
	if strings.Join(methods, ",") != "initialize,notifications/initialized,tools/list" {
		t.Fatal(methods)
	}
}

func TestConnectionSSEAcceptsMatchingEventBeforeStreamEnd(t *testing.T) {
	reader, writer := io.Pipe()
	go func() { fmt.Fprint(writer, "data: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"tools\":[]}}\n\n") }()
	defer reader.Close()
	defer writer.Close()
	resp := &http.Response{Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: reader}
	done := make(chan error, 1)
	go func() { _, err := connectionRPCResponse(resp, 2); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("waited for SSE stream to close")
	}
}

func TestConnectionClientBlocksPrivateDialAndRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("private server reached") }))
	defer srv.Close()
	if _, err := connectionHTTPClient().Get(srv.URL); err == nil {
		t.Fatal("private dial permitted")
	}
	client := connectionHTTPClient()
	if client.CheckRedirect(&http.Request{}, nil) != http.ErrUseLastResponse {
		t.Fatal("redirect allowed")
	}
}

func TestConnectionResponseLimitsAndUntrustedErrors(t *testing.T) {
	var out any
	if decodeConnectionJSON(strings.NewReader(strings.Repeat(" ", maxConnectionResponse+1)), &out) == nil {
		t.Fatal("accepted oversized response")
	}
	resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":1,"error":{"message":"private-key-secret"}}`))}
	_, err := connectionRPCResponse(resp, 1)
	if err == nil || strings.Contains(err.Error(), "private-key-secret") {
		t.Fatal("provider message leaked")
	}
}
