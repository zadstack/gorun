package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

const (
	envKey          = "GORUN_MCP_KEY"
	rpcUnauthorized = -32001
	rpcForbidden    = -32003
	serviceName     = "mcp"
	maxRequestBytes = 16 << 20 // base64 uploads up to 10 MB plus JSON overhead
	instructions    = "GoRun CMS. Start with get_site_structure. Sensitive tools (publish, unpublish, delete, settings) " +
		"may return status pending_approval: poll check_approval with the approval_id until a human decides."
)

// server holds the tool pipeline shared by the HTTP and stdio transports.
type server struct {
	d         *kernel.Deps
	keys      auth.KeyService
	users     auth.Service
	approvals *approvalStore
	log       *slog.Logger
	// dynamic holds per-call sensitivity rules (tool name → rule), set
	// during Register.
	dynamic map[string]func(ctx context.Context, args json.RawMessage) bool
}

// principalFunc returns the acting context for one request or call: the
// HTTP request's key principal, or (stdio) a freshly re-validated one.
type principalFunc func(ctx context.Context) (context.Context, error)

func newServer(d *kernel.Deps, keys auth.KeyService, users auth.Service) *server {
	s := &server{d: d, keys: keys, users: users, log: logger(d), dynamic: map[string]func(context.Context, json.RawMessage) bool{}}
	s.approvals = &approvalStore{s: s}
	d.Services.Set(serviceName, s)
	return s
}

// tools returns every registered tool (core + other modules), read at call
// time so modules booting after mcp (ext) are included.
func (s *server) tools() []kernel.MCPTool { return s.d.MCP.Tools() }

func (s *server) tool(name string) (kernel.MCPTool, bool) {
	for _, t := range s.tools() {
		if t.Name == name {
			return t, true
		}
	}
	return kernel.MCPTool{}, false
}

// valuesCtx takes its values (the auth principal, client IP) from values
// and its deadline and cancellation from the embedded SDK context.
type valuesCtx struct {
	context.Context
	values context.Context
}

func (c valuesCtx) Value(k any) any {
	if v := c.values.Value(k); v != nil {
		return v
	}
	return c.Context.Value(k)
}

// newSDKServer builds an MCP server acting as the principal returned by
// principal (an API key context). Every tool is registered; tools/list is
// filtered to those the key's scopes allow and calls to the others answer
// "forbidden". A principal error (stdio: key revoked) fails the request.
// limit, when set, is consulted before each tool call (stdio rate limit).
func (s *server) newSDKServer(principal principalFunc, limit func() error) *sdk.Server {
	srv := sdk.NewServer(&sdk.Implementation{Name: serverName, Title: "GoRun", Version: serverVersion},
		&sdk.ServerOptions{Instructions: instructions, Capabilities: &sdk.ServerCapabilities{}})
	with := func(ctx context.Context) (context.Context, error) {
		p, err := principal(ctx)
		if err != nil {
			return nil, err
		}
		return valuesCtx{Context: ctx, values: p}, nil
	}
	for _, t := range s.tools() {
		schema, err := schemaFor(t.Input)
		if err != nil {
			s.log.Error("mcp: tool schema", "tool", t.Name, "err", err)
			schema = emptyObject
		}
		tool := t
		srv.AddTool(&sdk.Tool{Name: t.Name, Description: s.describe(t), InputSchema: schema},
			func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				ctx, err := with(ctx)
				if err != nil {
					return s.toolResult(tool.Name, nil, err), nil
				}
				if limit != nil {
					if err := limit(); err != nil {
						return s.toolResult(tool.Name, nil, err), nil
					}
				}
				data, err := s.call(ctx, tool, req.Params.Arguments)
				return s.toolResult(tool.Name, data, err), nil
			})
	}
	srv.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
			res, err := next(ctx, method, req)
			if lr, ok := res.(*sdk.ListToolsResult); ok && err == nil && method == "tools/list" {
				pctx, perr := with(ctx)
				if perr != nil {
					return nil, &jsonrpc.Error{Code: rpcUnauthorized, Message: perr.Error()}
				}
				lr.Tools = s.visible(pctx, lr.Tools)
			}
			return res, err
		}
	})
	s.addResources(srv, with)
	return srv
}

// visible filters list to the tools ctx may call.
func (s *server) visible(ctx context.Context, list []*sdk.Tool) []*sdk.Tool {
	byName := map[string]kernel.MCPTool{}
	for _, t := range s.tools() {
		byName[t.Name] = t
	}
	out := make([]*sdk.Tool, 0, len(list))
	for _, tl := range list {
		if t, ok := byName[tl.Name]; ok && allowed(ctx, t) {
			out = append(out, tl)
		}
	}
	return out
}

func (s *server) describe(t kernel.MCPTool) string {
	d := t.Description
	if len(t.Scopes) > 0 {
		d += " [scopes: " + strings.Join(t.Scopes, ", ") + "]"
	}
	switch {
	case t.Sensitive:
		d += " SENSITIVE: requires human approval unless the key lists mcp.approve_self."
	case s.dynamic[t.Name] != nil:
		d += " Sensitive on live items: requires human approval unless the key lists mcp.approve_self."
	}
	return d
}

// toolResult renders the envelope as text and structured content.
func (s *server) toolResult(tool string, data any, err error) *sdk.CallToolResult {
	env := s.envelope(tool, data, err)
	b, mErr := json.Marshal(env)
	if mErr != nil {
		env = newEnvelope(nil, &ToolError{Code: codeInternal, Message: "result is not JSON-serialisable"})
		b, _ = json.Marshal(env)
	}
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(b)}},
		StructuredContent: json.RawMessage(b), IsError: !env.OK}
}

// addResources registers gorun://types, gorun://site, and grant-scoped canvases.
func (s *server) addResources(srv *sdk.Server, with func(context.Context) (context.Context, error)) {
	add := func(uri, name, desc string, read func(context.Context) (any, error)) {
		srv.AddResource(&sdk.Resource{URI: uri, Name: name, Description: desc, MIMEType: "application/json"},
			func(ctx context.Context, req *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
				ctx, err := with(ctx)
				if err != nil {
					return nil, &jsonrpc.Error{Code: rpcUnauthorized, Message: err.Error()}
				}
				if !auth.Can(ctx, "content.view") {
					return nil, &jsonrpc.Error{Code: rpcForbidden, Message: "forbidden: content.view scope required"}
				}
				v, err := read(ctx)
				if err != nil {
					return nil, err
				}
				b, err := json.MarshalIndent(v, "", "  ")
				if err != nil {
					return nil, err
				}
				return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(b)}}}, nil
			})
	}
	add("gorun://types", "types", "Content types and their field schemas", func(ctx context.Context) (any, error) {
		return s.contentTypes(ctx)
	})
	add("gorun://site", "site", "Site structure: identity, types, taxonomies, menus, theme, templates", s.siteStructure)
	if reader, ok := kernel.Get[admin.CanvasReader](s.d, admin.ServiceCanvasRead); ok {
		srv.AddResourceTemplate(&sdk.ResourceTemplate{
			URITemplate: "gorun://canvases/{id}",
			Name:        "canvas",
			Description: "Grant-scoped canvas board document (same payload as get_canvas)",
			MIMEType:    "application/json",
		}, func(ctx context.Context, req *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
			ctx, err := with(ctx)
			if err != nil {
				return nil, &jsonrpc.Error{Code: rpcUnauthorized, Message: err.Error()}
			}
			if !auth.Can(ctx, "canvas.read") || !auth.Can(ctx, "content.view") {
				return nil, &jsonrpc.Error{Code: rpcForbidden, Message: "forbidden: canvas.read and content.view scopes required"}
			}
			uri := req.Params.URI
			id, ok := strings.CutPrefix(uri, "gorun://canvases/")
			if !ok || id == "" || strings.Contains(id, "/") {
				return nil, &jsonrpc.Error{Code: rpcForbidden, Message: "invalid canvas resource URI"}
			}
			doc, err := reader.Get(ctx, id)
			if err != nil {
				var coded interface {
					ErrorCode() string
					Error() string
				}
				if errors.As(err, &coded) {
					switch coded.ErrorCode() {
					case "forbidden":
						return nil, &jsonrpc.Error{Code: rpcForbidden, Message: coded.Error()}
					case "not_found":
						return nil, sdk.ResourceNotFoundError(uri)
					}
				}
				return nil, err
			}
			b, err := json.MarshalIndent(doc, "", "  ")
			if err != nil {
				return nil, err
			}
			return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(b)}}}, nil
		})
	}
}

// httpHandler serves /mcp: streamable HTTP in stateless JSON mode, one MCP
// server per request bound to the request's API key principal. Requests
// without "Authorization: Bearer gp_…" get a JSON-RPC 401; session cookies
// are never accepted. Keys (API and agent kinds) are authenticated and rate
// limited per key by auth.MCPKeyMiddleware. DNS-rebinding protection is disabled because every
// request carries a bearer key (no ambient credentials) and reverse proxies
// on the same host connect from 127.0.0.1 with the public Host header.
func (s *server) httpHandler() http.Handler {
	h := sdk.NewStreamableHTTPHandler(func(r *http.Request) *sdk.Server {
		p := r.Context()
		return s.newSDKServer(func(context.Context) (context.Context, error) { return p, nil }, nil)
	},
		&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: maxRequestBytes,
			DisableLocalhostProtection: true})
	keyed := auth.MCPKeyMiddleware(s.d)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.ActorFromContext(r.Context()).Kind != "api_key" {
			unauthorized(w, "API key required")
			return
		}
		h.ServeHTTP(w, r)
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hasBearerKey(r) {
			unauthorized(w, "Authorization: Bearer gp_… API key required")
			return
		}
		keyed.ServeHTTP(w, r)
	})
}

func hasBearerKey(r *http.Request) bool {
	scheme, token, ok := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	return ok && strings.EqualFold(scheme, "Bearer") && strings.HasPrefix(strings.TrimSpace(token), "gp_")
}

// unauthorized writes a JSON-RPC error response with HTTP 401.
func unauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="gorun-mcp"`)
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": nil,
		"error": map[string]any{"code": rpcUnauthorized, "message": "unauthorized: " + msg}})
}

// RunStdio serves MCP over stdin/stdout for `gorun mcp --key`, acting as
// the API key apiKey (or, when empty, the GORUN_MCP_KEY environment
// variable). The key is validated like a bearer key (hash lookup,
// revocation, expiry, active owner) at start and again before every call,
// so revoking it or disabling its owner ends access mid-session. Calls are
// rate limited per process at the key's rate_per_min.
func RunStdio(ctx context.Context, app *kernel.App, apiKey string) error {
	if strings.TrimSpace(apiKey) == "" {
		apiKey = os.Getenv(envKey)
	}
	return run(ctx, app, apiKey, &sdk.StdioTransport{})
}

// RunIO is RunStdio over the given streams (newline-delimited JSON-RPC),
// for embedding and tests; apiKey falls back to GORUN_MCP_KEY too.
func RunIO(ctx context.Context, app *kernel.App, apiKey string, in io.ReadCloser, out io.WriteCloser) error {
	if strings.TrimSpace(apiKey) == "" {
		apiKey = os.Getenv(envKey)
	}
	return run(ctx, app, apiKey, &sdk.IOTransport{Reader: in, Writer: out})
}

// run serves one MCP session on t as apiKey.
func run(ctx context.Context, app *kernel.App, apiKey string, t sdk.Transport) error {
	s, ok := kernel.Get[*server](app.Deps, serviceName)
	if !ok {
		return errors.New("mcp: module not booted")
	}
	principal, err := authenticate(app.Deps, apiKey)
	if err != nil {
		return err
	}
	rate := 60
	if k, err := s.findKey(ctx, auth.ActorFromContext(principal).ID); err == nil && k.RatePerMin > 0 {
		rate = k.RatePerMin
	}
	limiter := newBucket(rate, time.Now)
	fresh := func(context.Context) (context.Context, error) {
		p, err := authenticate(app.Deps, apiKey)
		if err != nil {
			return nil, &ToolError{Code: codeUnauthorized, Message: "the API key is no longer valid (revoked, expired or owner disabled)"}
		}
		return p, nil
	}
	return s.newSDKServer(fresh, limiter.take).Run(ctx, t)
}

// authenticate validates a plaintext key (API or agent kind) through
// auth.MCPKeyMiddleware and returns a context carrying its principal.
func authenticate(d *kernel.Deps, apiKey string) (context.Context, error) {
	apiKey = strings.TrimSpace(apiKey)
	if !strings.HasPrefix(apiKey, "gp_") {
		return nil, errors.New("mcp: invalid API key (expected gp_…)")
	}
	var principal context.Context
	h := auth.MCPKeyMiddleware(d)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { principal = r.Context() }))
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	rec := &statusRecorder{header: http.Header{}}
	h.ServeHTTP(rec, req)
	if principal == nil || auth.ActorFromContext(principal).Kind != "api_key" {
		return nil, fmt.Errorf("mcp: API key rejected (HTTP %d)", rec.status)
	}
	return principal, nil
}

type statusRecorder struct {
	header http.Header
	status int
}

func (r *statusRecorder) Header() http.Header         { return r.header }
func (r *statusRecorder) Write(b []byte) (int, error) { return len(b), nil }
func (r *statusRecorder) WriteHeader(code int)        { r.status = code }

// bucket is a token bucket: perMin tokens per minute, burst perMin.
type bucket struct {
	mu     sync.Mutex
	tokens float64
	max    float64
	rate   float64 // tokens per second
	last   time.Time
	now    func() time.Time
}

func newBucket(perMin int, now func() time.Time) *bucket {
	return &bucket{tokens: float64(perMin), max: float64(perMin), rate: float64(perMin) / 60, last: now(), now: now}
}

func (b *bucket) take() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	b.tokens = min(b.max, b.tokens+now.Sub(b.last).Seconds()*b.rate)
	b.last = now
	if b.tokens < 1 {
		return &ToolError{Code: codeRateLimit, Message: "rate limit exceeded"}
	}
	b.tokens--
	return nil
}
