package builder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

type layoutIDInput struct {
	ID string `json:"id"`
}
type layoutSaveInput struct {
	ID            string   `json:"id"`
	Document      Document `json:"document"`
	BaseUpdatedAt string   `json:"base_updated_at"`
}
type layoutPublishRequest struct {
	UpdatedAt string `json:"updated_at" format:"-"`
}
type layoutPublishInput struct {
	ID        string `json:"id"`
	UpdatedAt string `json:"updated_at" format:"-"`
}
type layoutAPI struct {
	svc *service
	d   *kernel.Deps
}

// layoutError exposes only deliberate transport messages; internal causes remain in logs.
type layoutError struct {
	code, message string
	fields        map[string]string
	cause         error
}

func (e *layoutError) Error() string                  { return e.message }
func (e *layoutError) ErrorCode() string              { return e.code }
func (e *layoutError) FieldErrors() map[string]string { return e.fields }
func (e *layoutError) Unwrap() error                  { return e.cause }
func layoutFailure(err error) *layoutError {
	var known *layoutError
	if errors.As(err, &known) {
		return known
	}
	switch {
	case errors.Is(err, ErrForbidden):
		return &layoutError{code: "forbidden", message: "page layout access forbidden", cause: err}
	case errors.Is(err, ErrNotFound):
		return &layoutError{code: "not_found", message: "page layout not found", cause: err}
	case errors.Is(err, ErrConflict):
		return &layoutError{code: "conflict", message: "page or draft changed; reload its layout before retrying", cause: err}
	default:
		return &layoutError{code: "internal", message: "internal error", cause: err}
	}
}
func invalidLayout(message string, fields map[string]string) *layoutError {
	return &layoutError{code: "validation_failed", message: message, fields: fields}
}
func validateLayoutID(id string) error {
	if id == "" || len(id) > 80 {
		return invalidLayout("invalid page ID", map[string]string{"id": "page ID required"})
	}
	return nil
}
func layoutPermission(ctx context.Context, publish bool) error {
	if !auth.Can(ctx, "content.edit") || (publish && !auth.Can(ctx, "content.publish")) {
		return layoutFailure(ErrForbidden)
	}
	return nil
}

func registerLayoutAPI(d *kernel.Deps, svc *service) {
	a := &layoutAPI{svc: svc, d: d}
	contract := &kernel.APIContract{MaxBodyBytes: 1 << 20, Limits: map[string]any{"builder_sections": maxSections, "builder_repeated_entries": maxRepeatedEntries}, Errors: []kernel.APIErrorResponse{
		{Status: 409, Code: "conflict", Description: "The page or draft changed; reload and review before retrying."},
		{Status: 413, Code: "payload_too_large", Description: "JSON body exceeds 1 MiB."},
	}}
	id := []kernel.APIParam{{Name: "id", In: "path", Type: "string", Required: true, Description: "page content ID"}}
	d.API.Add(kernel.APIOperation{Method: "GET", Path: "/v1/builder/{id}", Summary: "Read a private structured page layout and optimistic draft token", Tags: []string{"builder"}, Scopes: []string{"content.edit"}, Params: id, Response: Draft{}, Handler: a.get})
	d.API.Add(kernel.APIOperation{Method: "PUT", Path: "/v1/builder/{id}/draft", Summary: "Save a validated page layout as a separate private draft", Tags: []string{"builder"}, Scopes: []string{"content.edit"}, Params: id, Request: saveRequest{}, Response: Draft{}, Handler: a.save, Contract: contract})
	d.API.Add(kernel.APIOperation{Method: "POST", Path: "/v1/builder/{id}/publish", Summary: "Publish a saved layout using its current opaque draft token", Tags: []string{"builder"}, Scopes: []string{"content.edit", "content.publish"}, Params: id, Request: layoutPublishRequest{}, Response: Draft{}, Handler: a.publish, Contract: contract})
	d.MCP.AddTool(kernel.MCPTool{Name: "get_page_layout", Description: "Read a private page-builder layout with its current opaque updated_at token. Requires ownership where applicable.", Input: layoutIDInput{}, Scopes: []string{"content.edit"}, Handler: bindLayout(func(ctx context.Context, in layoutIDInput) (any, error) { return a.load(ctx, in.ID) })})
	d.MCP.AddTool(kernel.MCPTool{Name: "save_page_layout", Description: "Import or update validated structured sections as a private page-builder draft; public content stays unchanged. Pass updated_at from get_page_layout as base_updated_at.", Input: layoutSaveInput{}, Scopes: []string{"content.edit"}, Handler: bindLayout(func(ctx context.Context, in layoutSaveInput) (any, error) {
		return a.saveLayout(ctx, in.ID, in.Document, in.BaseUpdatedAt)
	})})
	d.MCP.AddTool(kernel.MCPTool{Name: "publish_page_layout", Description: "Publish a saved page-builder draft, replacing public page content. Requires human approval and the current draft updated_at token.", Input: layoutPublishInput{}, Scopes: []string{"content.edit", "content.publish"}, Sensitive: true, Handler: bindLayout(func(ctx context.Context, in layoutPublishInput) (any, error) {
		return a.publishLayout(ctx, in.ID, in.UpdatedAt)
	})})
}
func (a *layoutAPI) load(ctx context.Context, id string) (*Draft, error) {
	if err := layoutPermission(ctx, false); err != nil {
		return nil, err
	}
	if err := validateLayoutID(id); err != nil {
		return nil, err
	}
	draft, err := a.svc.Load(ctx, id)
	if err != nil {
		return nil, layoutFailure(err)
	}
	return draft, nil
}
func (a *layoutAPI) saveLayout(ctx context.Context, id string, doc Document, token string) (*Draft, error) {
	if err := layoutPermission(ctx, false); err != nil {
		return nil, err
	}
	if err := validateLayoutID(id); err != nil {
		return nil, err
	}
	if err := Validate(doc); err != nil {
		return nil, invalidLayout("invalid page layout", map[string]string{"document": err.Error()})
	}
	draft, err := a.svc.Save(ctx, id, doc, token)
	if err != nil {
		return nil, layoutFailure(err)
	}
	return draft, nil
}
func (a *layoutAPI) publishLayout(ctx context.Context, id, token string) (*Draft, error) {
	if err := layoutPermission(ctx, true); err != nil {
		return nil, err
	}
	if err := validateLayoutID(id); err != nil {
		return nil, err
	}

	draft, err := a.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if draft.UpdatedAt != token {
		return nil, layoutFailure(ErrConflict)
	}
	if err := ValidatePublication(draft.Document); err != nil {
		return nil, invalidLayout("page layout cannot be published", map[string]string{"document": err.Error()})
	}
	if err := a.svc.Publish(ctx, id, token); err != nil {
		return nil, layoutFailure(err)
	}
	return a.load(ctx, id)
}
func (a *layoutAPI) writeError(w http.ResponseWriter, err error) {
	e := layoutFailure(err)
	status := 500
	switch e.code {
	case "forbidden":
		status = 403
	case "not_found":
		status = 404
	case "conflict":
		status = 409
	case "validation_failed":
		status = 422
	}
	if status == 500 {
		a.d.Log.Error("builder API request failed", "error", err)
	}
	httpx.Error(w, status, e.code, e.message, e.fields)
}
func (a *layoutAPI) get(w http.ResponseWriter, r *http.Request) {
	draft, err := a.load(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		a.writeError(w, err)
		return
	}
	httpx.JSON(w, 200, draft, nil)
}
func (a *layoutAPI) save(w http.ResponseWriter, r *http.Request) {
	if err := layoutPermission(r.Context(), false); err != nil {
		a.writeError(w, err)
		return
	}
	var in saveRequest
	if err := decodeBuilder(w, r, &in); err != nil {
		writeDecodeError(w, err)
		return
	}
	draft, err := a.saveLayout(r.Context(), chi.URLParam(r, "id"), in.Document, in.BaseUpdatedAt)
	if err != nil {
		a.writeError(w, err)
		return
	}
	httpx.JSON(w, 200, draft, nil)
}
func (a *layoutAPI) publish(w http.ResponseWriter, r *http.Request) {
	if err := layoutPermission(r.Context(), true); err != nil {
		a.writeError(w, err)
		return
	}
	var in layoutPublishRequest
	if err := decodeBuilder(w, r, &in); err != nil {
		writeDecodeError(w, err)
		return
	}
	draft, err := a.publishLayout(r.Context(), chi.URLParam(r, "id"), in.UpdatedAt)
	if err != nil {
		a.writeError(w, err)
		return
	}
	httpx.JSON(w, 200, draft, nil)
}
func bindLayout[In any](fn func(context.Context, In) (any, error)) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, args json.RawMessage) (any, error) {
		if len(args) > 1<<20 {
			return nil, invalidLayout("layout arguments too large", nil)
		}
		if len(bytes.TrimSpace(args)) == 0 {
			args = json.RawMessage(`{}`)
		}
		dec := json.NewDecoder(bytes.NewReader(args))
		dec.DisallowUnknownFields()
		var in In
		if err := dec.Decode(&in); err != nil {
			return nil, invalidLayout("invalid layout arguments", nil)
		}
		if err := dec.Decode(&struct{}{}); err != io.EOF {
			return nil, invalidLayout("invalid layout arguments", nil)
		}
		return fn(ctx, in)
	}
}

func writeDecodeError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		httpx.Error(w, 413, "payload_too_large", "JSON body exceeds 1 MiB", nil)
		return
	}
	httpx.Error(w, 400, "invalid_json", "invalid JSON body", nil)
}
