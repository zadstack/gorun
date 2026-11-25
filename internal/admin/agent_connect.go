package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

// ServiceMCPProbe is the kernel service name of the in-process MCP
// connection test (an MCPProbe), set by the mcp module when it is enabled.
const ServiceMCPProbe = "mcp.probe"

// MCPProbe runs an MCP initialize + tools/list against this site's own MCP
// server, in process, acting as principal (an API key context).
type MCPProbe interface {
	Probe(ctx, principal context.Context) (MCPProbeResult, error)
}

// MCPProbeResult is what one probe saw.
type MCPProbeResult struct {
	ServerName, ServerVersion, ProtocolVersion string
	// Tools are the visible tool names (sorted); Toolsets the toolsets
	// they belong to (sorted, unique).
	Tools, Toolsets []string
	// Scoped counts the visible tools that need at least one scope (the
	// others are listed for any authenticated key).
	Scoped int
}

// JSON-RPC codes the /mcp endpoint answers for a refused key (mirrors
// internal/mcp: rpcUnauthorized and rpcForbidden).
const (
	rpcUnauthorized = -32001
	rpcForbidden    = -32003
)

// connectToolPreview is how many tool names the result lists.
const connectToolPreview = 12

// connectSnippets are the ready-to-copy client configurations for one key.
type connectSnippets struct {
	ClaudeCode, Desktop, HTTP string
}

// connectTest is the result of one "Test connection".
type connectTest struct {
	// KeyID is the tested key; Scope the panel that asked ("new": the
	// created-key panel, "row": the key's row in the list).
	KeyID, Scope string
	OK           bool
	// Code is the failure code (catalog agentconnect.hint_<code>), RPC the
	// JSON-RPC error code /mcp would answer, 0 when it would not refuse.
	Code              string
	RPC               int
	Server, Version   string
	Protocol          string
	Kind              string
	Scopes            []string
	Toolsets, Preview []string
	ToolCount, More   int
}

// mcpBase is the base URL the client snippets use: the stored site.base_url
// setting, else the origin of this request (else the configured base URL).
func (h *handlers) mcpBase(r *http.Request) string {
	if v := strings.TrimSpace(h.d.Settings.GetString(r.Context(), kernel.SettingBaseURL, "")); v != "" {
		return strings.TrimRight(v, "/")
	}
	if r.Host != "" {
		scheme := "http"
		if httpx.RequestIsHTTPS(r) {
			scheme = "https"
		}
		return scheme + "://" + r.Host
	}
	return kernel.BaseURL(r.Context(), h.d)
}

// newConnectSnippets builds the three client configurations for key on base.
func newConnectSnippets(base, key string) connectSnippets {
	endpoint := base + "/mcp"
	indent := func(v any) string {
		b, _ := json.MarshalIndent(v, "", "  ")
		return string(b)
	}
	type stdio struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}
	type remote struct {
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	}
	return connectSnippets{
		ClaudeCode: `claude mcp add --transport http gorun ` + endpoint + ` --header "Authorization: Bearer ` + key + `"`,
		Desktop: indent(map[string]any{"mcpServers": map[string]stdio{"gorun": {
			Command: "/usr/local/bin/gorun", Args: []string{"mcp", "--key", key},
			Env: map[string]string{"GORUN_DATA": "/var/lib/gorun", "GORUN_THEMES": "/opt/gorun/themes"},
		}}}),
		HTTP: indent(map[string]any{"mcpServers": map[string]remote{"gorun": {
			URL: endpoint, Headers: map[string]string{"Authorization": "Bearer " + key},
		}}}),
	}
}

// keyTest answers "Test connection" for the key in the form (id, and the
// freshly created secret when the page still has it): it re-renders the
// keys page with the result (status 200 either way, so htmx swaps it in).
// The created-key panel posts its secret back and is shown again, so its
// snippets stay copyable after the test.
func (h *handlers) keyTest(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(r.PostFormValue("id"))
	secret := strings.TrimSpace(r.PostFormValue("secret"))
	res, k := h.testKey(r.Context(), id, secret)
	data := keysPage{Form: newKeyForm(), Test: res}
	res.Scope = "row"
	if r.PostFormValue("scope") == "new" && k != nil && strings.HasPrefix(secret, "gp_"+k.Prefix) {
		// The created-key panel posted its own secret back: keep showing it.
		res.Scope, data.Plaintext, data.Created = "new", secret, k
	}
	h.renderKeys(w, r, http.StatusOK, data)
}

// testKey resolves the key principal and probes the MCP server as it. k is
// the stored key when it exists.
func (h *handlers) testKey(ctx context.Context, id, secret string) (res *connectTest, k *auth.APIKey) {
	res = &connectTest{KeyID: id}
	fail := func(code string, rpc int) (*connectTest, *auth.APIKey) {
		res.Code, res.RPC = code, rpc
		return res, k
	}
	k, err := h.keys.Get(ctx, id)
	if err != nil {
		k = nil
		if errors.Is(err, auth.ErrNotFound) {
			return fail("not_found", rpcUnauthorized)
		}
		h.d.Log.Error("admin: key test", "err", err)
		return fail("internal", 0)
	}
	res.Kind, res.Scopes = k.Kind, k.Scopes
	// The stored record gives the precise reason a key is refused; /mcp
	// itself only says "invalid API key".
	principal, err := auth.WithAPIKeyActor(context.Background(), h.d, k.ID)
	switch {
	case errors.Is(err, auth.ErrKeyRevoked):
		return fail("revoked", rpcUnauthorized)
	case errors.Is(err, auth.ErrKeyExpired):
		return fail("expired", rpcUnauthorized)
	case errors.Is(err, auth.ErrKeyOwnerInactive):
		return fail("owner_inactive", rpcUnauthorized)
	case errors.Is(err, auth.ErrOnboardingRequired):
		return fail("onboarding", rpcUnauthorized)
	case err != nil:
		h.d.Log.Error("admin: key test", "err", err)
		return fail("internal", 0)
	}
	if secret != "" {
		// The secret goes through the same authentication as a bearer key
		// on /mcp (hash, revocation, expiry, owner, rate limit).
		p, status := h.bearerPrincipal(secret)
		switch {
		case status == http.StatusTooManyRequests:
			return fail("rate_limited", rpcUnauthorized)
		case p == nil || auth.ActorFromContext(p).ID != k.ID:
			return fail("unauthorized", rpcUnauthorized)
		}
		principal = p
	}
	probe, ok := kernel.Get[MCPProbe](h.d, ServiceMCPProbe)
	if !ok {
		return fail("unavailable", 0)
	}
	pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := probe.Probe(pctx, principal)
	if err != nil {
		h.d.Log.Error("admin: key test probe", "err", err)
		return fail("internal", 0)
	}
	res.Server, res.Version, res.Protocol = out.ServerName, out.ServerVersion, out.ProtocolVersion
	res.Toolsets, res.ToolCount = out.Toolsets, len(out.Tools)
	res.Preview = out.Tools[:min(len(out.Tools), connectToolPreview)]
	res.More = len(out.Tools) - len(res.Preview)
	if out.Scoped == 0 {
		// Only the tools every key sees: the scopes grant nothing usable.
		return fail("no_tools", rpcForbidden)
	}
	res.OK = true
	return res, k
}

// bearerPrincipal authenticates secret exactly like an Authorization:
// Bearer header on /mcp and returns the key principal (nil when refused)
// and the HTTP status of a refusal.
func (h *handlers) bearerPrincipal(secret string) (context.Context, int) {
	var principal context.Context
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { principal = r.Context() })
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", nil)
	if err != nil {
		return nil, http.StatusInternalServerError
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	rec := &statusOnly{header: http.Header{}, status: http.StatusOK}
	auth.MCPKeyMiddleware(h.d)(next).ServeHTTP(rec, req)
	if principal == nil || auth.ActorFromContext(principal).Kind != "api_key" {
		return nil, rec.status
	}
	return principal, rec.status
}

// statusOnly is a ResponseWriter that keeps only the status code.
type statusOnly struct {
	header http.Header
	status int
}

func (s *statusOnly) Header() http.Header         { return s.header }
func (s *statusOnly) Write(b []byte) (int, error) { return len(b), nil }
func (s *statusOnly) WriteHeader(code int)        { s.status = code }
