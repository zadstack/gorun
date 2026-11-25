package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/menus"
)

// Resource templates. Each one reads through the matching tool (same
// scopes, same service checks, same {"ok":true,"data":…} envelope as the
// tool's result); menus have no tool and read like get_site_structure.
const (
	tmplContent       = "gorun://content/{type}/{slug}{?locale}"
	tmplContentLocale = "gorun://content/{type}/{slug}/{locale}"
	tmplMedia         = "gorun://media/{id}"
	tmplMenu          = "gorun://menu/{slug}"
)

// resourceTemplate is one template: the tool whose scopes gate it and a
// reader that turns the URI's variables into data.
type resourceTemplate struct {
	uri, name, title, desc string
	// tool gates the template: the key must be allowed to call it.
	tool string
	// host is the URI's host part ("content", "media", "menu"); segments
	// is the number of path segments the template takes.
	host     string
	segments int
	// query lists the query parameters the template accepts.
	query []string
	// complete maps a variable to the completion source for it.
	complete map[string]string
	read     func(ctx context.Context, vars map[string]string) (any, error)
}

// resourceTemplates lists the templates whose services are present.
func (s *server) resourceTemplates() []resourceTemplate {
	var out []resourceTemplate
	if get, ok := s.tool("get_content"); ok {
		read := func(ctx context.Context, vars map[string]string) (any, error) {
			args, _ := json.Marshal(getContentInput{Type: vars["type"], Slug: vars["slug"], Locale: vars["locale"]})
			return s.execute(ctx, get, args)
		}
		complete := map[string]string{"type": completeTypes, "slug": completeContentSlugs, "locale": completeLocales}
		out = append(out,
			resourceTemplate{uri: tmplContent, name: "content", title: "Content item by type and slug",
				desc: "One content item (any status the key may read, like get_content); ?locale= picks the language. Same envelope as get_content.",
				tool: "get_content", host: "content", segments: 2, query: []string{"locale"}, complete: complete, read: read},
			resourceTemplate{uri: tmplContentLocale, name: "content-locale", title: "Content item by type, slug and locale",
				desc: "One content item in one language, like get_content with type, slug and locale. Same envelope as get_content.",
				tool: "get_content", host: "content", segments: 3, complete: complete, read: read})
	}
	if get, ok := s.tool("get_media"); ok {
		out = append(out, resourceTemplate{uri: tmplMedia, name: "media", title: "Media item metadata",
			desc: "Metadata of one media item (dimensions, alt, caption, variants; never the file bytes). Same envelope as get_media.",
			tool: "get_media", host: "media", segments: 1,
			read: func(ctx context.Context, vars map[string]string) (any, error) {
				args, _ := json.Marshal(mediaIDInput{ID: vars["id"]})
				return s.execute(ctx, get, args)
			}})
	}
	if svc, ok := kernel.Get[menus.Service](s.d, kernel.ServiceMenus); ok {
		if _, ok := s.tool("get_site_structure"); ok {
			out = append(out, resourceTemplate{uri: tmplMenu, name: "menu", title: "Menu by slug",
				desc: "One navigation menu with its items (the menus of get_site_structure, one at a time).",
				tool: "get_site_structure", host: "menu", segments: 1, complete: map[string]string{"slug": completeMenus},
				read: func(ctx context.Context, vars map[string]string) (any, error) { return svc.Get(ctx, vars["slug"]) }})
		}
	}
	return out
}

// resourceTemplate returns the template with URI template uri.
func (s *server) resourceTemplate(uri string) (resourceTemplate, bool) {
	for _, rt := range s.resourceTemplates() {
		if rt.uri == uri {
			return rt, true
		}
	}
	return resourceTemplate{}, false
}

// canRead reports whether ctx may read rt: the key holds the scopes of its
// tool (toolsets and readonly narrow tools, not resources).
func (s *server) canRead(ctx context.Context, rt resourceTemplate) bool {
	t, ok := s.tool(rt.tool)
	return ok && allowed(ctx, t)
}

func (s *server) addResourceTemplates(srv *sdk.Server, with func(context.Context) (context.Context, error), gates map[string]func(context.Context) bool) {
	for _, rt := range s.resourceTemplates() {
		gates[rt.uri] = func(ctx context.Context) bool { return s.canRead(ctx, rt) }
		srv.AddResourceTemplate(&sdk.ResourceTemplate{URITemplate: rt.uri, Name: rt.name, Title: rt.title, Description: rt.desc,
			MIMEType: "application/json"},
			func(ctx context.Context, req *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
				ctx, err := with(ctx)
				if err != nil {
					return nil, &jsonrpc.Error{Code: rpcUnauthorized, Message: err.Error()}
				}
				uri := req.Params.URI
				if !s.canRead(ctx, rt) {
					t, _ := s.tool(rt.tool)
					return nil, &jsonrpc.Error{Code: rpcForbidden, Message: "forbidden: " + strings.Join(t.Scopes, " and ") + " scope required"}
				}
				vars, err := rt.parse(uri)
				if err != nil {
					return nil, err
				}
				data, err := rt.read(ctx, vars)
				if err != nil {
					return nil, s.resourceError(rt.tool, uri, err)
				}
				b, err := json.Marshal(newEnvelope(data, nil))
				if err != nil {
					return nil, &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "result is not JSON-serialisable"}
				}
				return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(b)}}}, nil
			})
	}
}

// parse splits uri into rt's variables. It rejects empty segments, the
// wrong number of segments and unknown or repeated query parameters.
func (rt resourceTemplate) parse(uri string) (map[string]string, error) {
	bad := func(msg string) error {
		return &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "invalid resource URI: " + msg}
	}
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "gorun" || u.Host != rt.host {
		return nil, bad("expected " + rt.uri)
	}
	names := templateVars(rt.uri)
	segs := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
	if len(segs) != rt.segments {
		return nil, bad("expected " + rt.uri)
	}
	vars := map[string]string{}
	for i, seg := range segs {
		v, err := url.PathUnescape(seg)
		if err != nil || strings.TrimSpace(v) == "" {
			return nil, bad(names[i] + " is empty or not valid percent-encoding")
		}
		vars[names[i]] = v
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, bad("query is not valid")
	}
	for k, vs := range q {
		if !slices.Contains(rt.query, k) {
			return nil, bad("unknown query parameter " + k)
		}
		if len(vs) != 1 || strings.TrimSpace(vs[0]) == "" {
			return nil, bad(k + " must be given once and not be empty")
		}
		vars[k] = vs[0]
	}
	return vars, nil
}

// templateVars lists the path variables of a URI template in order.
func templateVars(tmpl string) []string {
	var out []string
	for rest := tmpl; ; {
		i := strings.Index(rest, "{")
		if i < 0 {
			return out
		}
		j := strings.Index(rest[i:], "}")
		if j < 0 {
			return out
		}
		if name := rest[i+1 : i+j]; !strings.HasPrefix(name, "?") {
			out = append(out, name)
		}
		rest = rest[i+j+1:]
	}
}

// resourceError maps a reader error to the JSON-RPC error of resources/read:
// not_found → the SDK's resource-not-found error (-32602 "Resource not
// found" with data.uri, per SEP-2164), forbidden → -32003, a bad
// argument → -32602, anything else a generic -32603 (details logged).
func (s *server) resourceError(tool, uri string, err error) error {
	if errors.Is(err, content.ErrForbidden) {
		return &jsonrpc.Error{Code: rpcForbidden, Message: "forbidden"}
	}
	te := s.classify(tool, err)
	switch te.Code {
	case codeNotFound:
		return sdk.ResourceNotFoundError(uri)
	case codeForbidden:
		return &jsonrpc.Error{Code: rpcForbidden, Message: te.Message}
	case codeValidation:
		return &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: te.Message}
	}
	return &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "internal error"}
}
