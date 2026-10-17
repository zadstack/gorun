package api

import (
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/zadstack/gorun/internal/kernel"
)

// Version is the API version reported in the OpenAPI document. It is a var
// so main can set it from its build version (or -ldflags
// "-X github.com/zadstack/gorun/internal/api.Version=…").
var Version = "0.1.0"

// CoreVersion is the binary build version; Version remains the API family.
var CoreVersion = "dev"

const ContractVersion = "1"

// standardErrors are documented on every operation (components.responses).
var standardErrors = []struct {
	code, name, desc string
}{
	{"400", "BadRequest", "Malformed request (invalid_json, empty_body, bad_request)."},
	{"401", "Unauthorized", "Missing or invalid API key or session (unauthorized)."},
	{"403", "Forbidden", "Authenticated but missing a required scope, or CSRF token invalid (forbidden, csrf_invalid)."},
	{"404", "NotFound", "Resource not found (not_found)."},
	{"405", "MethodNotAllowed", "Method not supported on this path; see the Allow header (method_not_allowed)."},
	{"422", "ValidationFailed", "Input failed validation; error.fields maps field → message (validation_failed)."},
	{"429", "RateLimited", "Per-key rate limit exceeded; see Retry-After (rate_limited)."},
	{"500", "InternalError", "Unexpected server error (internal_error)."},
}

var (
	chiParamRe   = regexp.MustCompile(`\{([^}:]+)(:[^}]*)?\}`)
	operationIDs = regexp.MustCompile(`[^a-z0-9]+`)
)

// normalizePath returns the mount-relative path with the /v1 prefix
// ("/content" and "/v1/content" both become "/v1/content").
func normalizePath(p string) string {
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if p == "/v1" || strings.HasPrefix(p, "/v1/") {
		return p
	}
	return "/v1" + p
}

// openAPIPath converts a chi pattern to an OpenAPI path template:
// {id:[0-9]+} → {id}, a trailing /* → /{path}.
func openAPIPath(p string) string {
	p = chiParamRe.ReplaceAllString(p, "{$1}")
	if strings.HasSuffix(p, "/*") {
		p = strings.TrimSuffix(p, "*") + "{path}"
	}
	return p
}

func hasBody(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	}
	return false
}

// buildOpenAPI renders the OpenAPI 3.1 document for ops; baseURL is the
// site origin (the server URL is baseURL + "/api", paths start with /v1).
func buildOpenAPI(ops []kernel.APIOperation, baseURL string) map[string]any {
	g := newSchemaGen()
	for _, op := range ops {
		if hasBody(strings.ToUpper(op.Method)) {
			g.collect(reflect.TypeOf(op.Request))
		}
		g.collect(reflect.TypeOf(op.Response))
	}
	g.assignNames()

	paths := map[string]any{}
	tagSet := map[string]bool{}
	usedIDs := map[string]bool{}
	for _, op := range ops {
		method := strings.ToUpper(op.Method)
		p := openAPIPath(normalizePath(op.Path))
		item, _ := paths[p].(map[string]any)
		if item == nil {
			item = map[string]any{}
			paths[p] = item
		}
		for _, t := range op.Tags {
			tagSet[t] = true
		}
		item[strings.ToLower(method)] = operation(g, op, method, p, usedIDs)
	}

	schemas := g.components()
	schemas["Meta"] = map[string]any{
		"type":     "object",
		"required": []string{"total", "page", "per_page"},
		"properties": map[string]any{
			"total":    map[string]any{"type": "integer", "minimum": 0},
			"page":     map[string]any{"type": "integer", "minimum": 1},
			"per_page": map[string]any{"type": "integer", "minimum": 1, "maximum": MaxPerPage},
		},
	}
	schemas["Error"] = map[string]any{
		"type":     "object",
		"required": []string{"ok", "error"},
		"properties": map[string]any{
			"ok": map[string]any{"type": "boolean", "const": false},
			"error": map[string]any{
				"type":     "object",
				"required": []string{"code", "message"},
				"properties": map[string]any{
					"code":    map[string]any{"type": "string", "description": "Machine-readable error code."},
					"message": map[string]any{"type": "string"},
					"fields": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"},
						"description": "Field → message (or i18n key) for validation errors."},
				},
			},
		},
	}
	responses := map[string]any{}
	for _, e := range standardErrors {
		responses[e.name] = map[string]any{
			"description": e.desc,
			"content":     map[string]any{"application/json": map[string]any{"schema": ref("Error")}},
		}
	}
	responses["PayloadTooLarge"] = map[string]any{
		"description": "Request body exceeds the operation's size limit (payload_too_large).",
		"content":     map[string]any{"application/json": map[string]any{"schema": ref("Error")}},
	}
	responses["RateLimited"].(map[string]any)["headers"] = map[string]any{"Retry-After": map[string]any{"description": "Seconds until retry.", "schema": map[string]any{"type": "integer", "minimum": 1}}}
	responses["RateLimited"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["example"] = errorExample("rate_limited", "rate limit exceeded")

	tags := make([]string, 0, len(tagSet))
	for t := range tagSet {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	tagObjs := make([]any, len(tags))
	for i, t := range tags {
		tagObjs[i] = map[string]any{"name": t}
	}

	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "GoRun API",
			"version":     Version,
			"description": "REST API for GoRun. Responses use the envelope {ok, data, meta} or {ok:false, error}.",
		},
		"servers": []any{map[string]any{"url": strings.TrimRight(baseURL, "/") + "/api"}},
		"tags":    tagObjs,
		"paths":   paths,
		"components": map[string]any{
			"schemas":   schemas,
			"responses": responses,
			"securitySchemes": map[string]any{
				"bearerAuth": map[string]any{"type": "http", "scheme": "bearer", "description": "API key: Authorization: Bearer gp_…"},
				"cookieAuth": map[string]any{"type": "apiKey", "in": "cookie", "name": "gp_session",
					"description": "Admin session cookie; unsafe methods also need the X-CSRF-Token header."},
			},
		},
		"security": []any{map[string]any{"bearerAuth": []string{}}, map[string]any{"cookieAuth": []string{}}},
		"x-gorun":  discovery(ops),
	}
}

func operation(g *schemaGen, op kernel.APIOperation, method, path string, usedIDs map[string]bool) map[string]any {
	out := map[string]any{
		"operationId": operationID(method, path, usedIDs),
		"parameters":  parameters(op, path),
		"responses":   responsesFor(g, op, method),
	}
	if op.Summary != "" {
		out["summary"] = op.Summary
	}
	if len(op.Tags) > 0 {
		out["tags"] = op.Tags
	}
	if len(op.Scopes) > 0 {
		out["x-scopes"] = op.Scopes
		out["description"] = "Required scopes: " + strings.Join(op.Scopes, ", ") + "."
	}
	if hasBody(method) && op.Request != nil {
		contentType := "application/json"
		if op.Contract != nil && op.Contract.RequestContentType != "" {
			contentType = op.Contract.RequestContentType
		}
		out["requestBody"] = map[string]any{
			"required": true,
			"content":  map[string]any{contentType: map[string]any{"schema": g.schema(reflect.TypeOf(op.Request))}},
		}
	}
	if op.Contract != nil && op.Contract.MaxBodyBytes > 0 {
		out["x-max-body-bytes"] = op.Contract.MaxBodyBytes
	}
	return out
}

func operationID(method, path string, used map[string]bool) string {
	base := strings.Trim(operationIDs.ReplaceAllString(strings.ToLower(method+"_"+path), "_"), "_")
	id := base
	for n := 2; used[id]; n++ {
		id = fmt.Sprintf("%s_%d", base, n)
	}
	used[id] = true
	return id
}

// parameters merges declared params with path params detected from path.
func parameters(op kernel.APIOperation, path string) []any {
	out := []any{}
	declaredPath := map[string]bool{}
	for _, p := range op.Params {
		in := p.In
		if !slices.Contains([]string{"query", "path", "header", "cookie"}, in) {
			in = "query"
		}
		param := map[string]any{"name": p.Name, "in": in, "schema": paramSchema(p.Type)}
		if p.Description != "" {
			param["description"] = p.Description
		}
		if p.Required || in == "path" {
			param["required"] = true
		}
		if in == "path" {
			declaredPath[p.Name] = true
		}
		out = append(out, param)
	}
	for _, m := range chiParamRe.FindAllStringSubmatch(path, -1) {
		if !declaredPath[m[1]] {
			out = append(out, map[string]any{"name": m[1], "in": "path", "required": true, "schema": map[string]any{"type": "string"}})
			declaredPath[m[1]] = true
		}
	}
	return out
}

func paramSchema(typ string) map[string]any {
	switch typ {
	case "integer", "number", "boolean":
		return map[string]any{"type": typ}
	case "array":
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	default:
		return map[string]any{"type": "string"}
	}
}

func responsesFor(g *schemaGen, op kernel.APIOperation, method string) map[string]any {
	data := map[string]any{}
	if op.Response != nil {
		data = g.schema(reflect.TypeOf(op.Response))
	}
	out := map[string]any{
		"200": map[string]any{
			"description": "Success.",
			"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{
				"type":     "object",
				"required": []string{"ok", "data"},
				"properties": map[string]any{
					"ok":   map[string]any{"type": "boolean", "const": true},
					"data": data,
					"meta": ref("Meta"),
				},
			}}},
		},
	}
	for _, e := range standardErrors {
		out[e.code] = map[string]any{"$ref": "#/components/responses/" + e.name}
	}
	if hasBody(method) {
		if op.Contract == nil {
			out["413"] = map[string]any{"$ref": "#/components/responses/PayloadTooLarge"}
		}
	}
	if op.Contract != nil {
		if len(op.Contract.SuccessStatuses) > 0 {
			success := out["200"]
			delete(out, "200")
			for _, status := range op.Contract.SuccessStatuses {
				out[fmt.Sprint(status)] = success
			}
		}
		for _, e := range op.Contract.Errors {
			out[fmt.Sprint(e.Status)] = map[string]any{
				"description": e.Description,
				"content": map[string]any{"application/json": map[string]any{
					"schema": ref("Error"), "example": errorExample(e.Code, e.Description),
				}},
			}
		}
	}
	return out
}

func errorExample(code, message string) map[string]any {
	return map[string]any{"ok": false, "error": map[string]any{"code": code, "message": message}}
}
