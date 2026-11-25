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
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
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
type layoutRevisionInput struct {
	ID         string `json:"id"`
	RevisionID string `json:"revision_id"`
}
type layoutRestoreInput struct {
	ID         string `json:"id"`
	RevisionID string `json:"revision_id"`
	UpdatedAt  string `json:"updated_at" format:"-"`
}
type layoutRestoreRequest struct {
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
	var taken *FormSlugTakenError
	if errors.As(err, &taken) {
		return &layoutError{code: "validation_failed", message: "page layout cannot be published", fields: map[string]string{"document": taken.Error()}, cause: err}
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
	contract := &kernel.APIContract{MaxBodyBytes: 1 << 20, Limits: map[string]any{"builder_sections": maxSections, "builder_repeated_entries": maxRepeatedEntries, "builder_native_facts": maxNativeFacts, "builder_comparison_columns": maxComparisonColumns, "builder_comparison_rows": maxRepeatedEntries, "builder_native_label_bytes": maxNativeLabelBytes, "builder_native_text_bytes": maxNativeTextBytes}, Errors: []kernel.APIErrorResponse{
		{Status: 409, Code: "conflict", Description: "The page or draft changed; reload and review before retrying."},
		{Status: 413, Code: "payload_too_large", Description: "JSON body exceeds 1 MiB."},
	}}
	id := []kernel.APIParam{{Name: "id", In: "path", Type: "string", Required: true, Description: "page content ID"}}
	rev := []kernel.APIParam{{Name: "id", In: "path", Type: "string", Required: true, Description: "page content ID"}, {Name: "revision", In: "path", Type: "string", Required: true, Description: "layout revision ID"}}
	d.API.Add(kernel.APIOperation{Method: "GET", Path: "/v1/builder/schema", Summary: "Read the section schema: what each section kind accepts in each library, the enums and the limits", Tags: []string{"builder"}, Scopes: []string{"content.view"}, Response: SectionSchema{}, Handler: a.schema})
	d.API.Add(kernel.APIOperation{Method: "GET", Path: "/v1/builder/{id}", Summary: "Read a private structured page layout and optimistic draft token", Tags: []string{"builder"}, Scopes: []string{"content.edit"}, Params: id, Response: Draft{}, Handler: a.get})
	d.API.Add(kernel.APIOperation{Method: "POST", Path: "/v1/builder/{id}/plan", Summary: "Dry run of a draft save: validate a layout document and list what it would change in the private draft", Tags: []string{"builder"}, Scopes: []string{"content.edit"}, Params: id, Request: layoutPlanRequest{}, Response: LayoutPlan{}, Handler: a.planHTTP, Contract: contract})
	d.API.Add(kernel.APIOperation{Method: "PUT", Path: "/v1/builder/{id}/draft", Summary: "Save a validated page layout as a separate private draft", Tags: []string{"builder"}, Scopes: []string{"content.edit"}, Params: id, Request: saveRequest{}, Response: Draft{}, Handler: a.save, Contract: contract})
	d.API.Add(kernel.APIOperation{Method: "POST", Path: "/v1/builder/{id}/publish", Summary: "Publish a saved layout using its current opaque draft token", Tags: []string{"builder"}, Scopes: []string{"content.edit", "content.publish"}, Params: id, Request: layoutPublishRequest{}, Response: Draft{}, Handler: a.publish, Contract: contract})
	d.API.Add(kernel.APIOperation{Method: "GET", Path: "/v1/builder/{id}/revisions", Summary: "List page-builder layout revisions (newest first)", Tags: []string{"builder"}, Scopes: []string{"content.edit"}, Params: id, Response: []LayoutRevision{}, Handler: a.listRevisions})
	d.API.Add(kernel.APIOperation{Method: "GET", Path: "/v1/builder/{id}/revisions/{revision}", Summary: "Read one page-builder layout revision", Tags: []string{"builder"}, Scopes: []string{"content.edit"}, Params: rev, Response: LayoutRevision{}, Handler: a.getRevision})
	d.API.Add(kernel.APIOperation{Method: "POST", Path: "/v1/builder/{id}/revisions/{revision}/restore", Summary: "Restore a layout revision into the private draft", Tags: []string{"builder"}, Scopes: []string{"content.edit"}, Params: rev, Request: layoutRestoreRequest{}, Response: Draft{}, Handler: a.restoreRevision, Contract: contract})
	d.API.Add(kernel.APIOperation{Method: "POST", Path: "/v1/builder/pages", Summary: "Create a private draft page from a purpose preset (about, service, methodology, contact)", Tags: []string{"builder"}, Scopes: []string{"content.create", "content.edit"}, Request: PurposePageInput{}, Response: PurposePage{}, Handler: a.createPurposePage, Contract: contract})
	d.MCP.AddTool(kernel.MCPTool{Name: "create_purpose_page", Title: "Create purpose page draft", Toolset: kernel.MCPToolsetBuilder, Description: "Create a private draft page for a purpose (about, service, methodology or contact) with its editable section structure. The hero takes the title; other sections start empty and hidden. Nothing is published.", Input: PurposePageInput{}, Scopes: []string{"content.create", "content.edit"}, Handler: bindLayout(func(ctx context.Context, in PurposePageInput) (any, error) {
		return a.createPurpose(ctx, in)
	})})
	d.MCP.AddTool(kernel.MCPTool{Name: "get_section_schema", Title: "Get section schema", Toolset: kernel.MCPToolsetBuilder, ReadOnly: true, Idempotent: true, Description: "Read the page-builder section schema: every section kind, the fields, native options, entry fields, layouts and required values each accepts under the legacy and the section library, plus enums and limits. Read it before composing a layout document.", Input: schemaInput{}, Scopes: []string{"content.view"}, Handler: bindLayout(func(ctx context.Context, in schemaInput) (any, error) {
		return Schema(in.Locale), nil
	})})
	d.MCP.AddTool(kernel.MCPTool{Name: "get_page_layout", Title: "Get page layout", Toolset: kernel.MCPToolsetBuilder, ReadOnly: true, Description: "Read a private page-builder layout with its current opaque updated_at token. Requires ownership where applicable.", Input: layoutIDInput{}, Scopes: []string{"content.edit"}, Handler: bindLayout(func(ctx context.Context, in layoutIDInput) (any, error) { return a.load(ctx, in.ID) })})
	d.MCP.AddTool(kernel.MCPTool{Name: "plan_page_layout", Title: "Plan page layout changes", Toolset: kernel.MCPToolsetBuilder, ReadOnly: true, Idempotent: true, Description: "Dry run of save_page_layout: validates a layout document against the schema and returns what it would change in the current private draft (a readable list, section by section), whether it could be published, and the base_updated_at a following save must carry. Nothing is written. Call it before save_page_layout and show the plan to the person.", Input: layoutPlanInput{}, Scopes: []string{"content.edit"}, Handler: bindLayout(func(ctx context.Context, in layoutPlanInput) (any, error) {
		return a.plan(ctx, in.ID, in.Document)
	})})
	d.MCP.AddTool(kernel.MCPTool{Name: "save_page_layout", Title: "Save page layout draft", Toolset: kernel.MCPToolsetBuilder, Destructive: true, Description: "Import or update validated structured sections as a private page-builder draft; public content stays unchanged. Pass updated_at from get_page_layout as base_updated_at.", Input: layoutSaveInput{}, Scopes: []string{"content.edit"}, Handler: bindLayout(func(ctx context.Context, in layoutSaveInput) (any, error) {
		return a.saveLayout(ctx, in.ID, in.Document, in.BaseUpdatedAt)
	})})
	d.MCP.AddTool(kernel.MCPTool{Name: "publish_page_layout", Title: "Publish page layout", Toolset: kernel.MCPToolsetBuilder, Destructive: true, Description: "Publish a saved page-builder draft, replacing public page content. Requires human approval and the current draft updated_at token.", Input: layoutPublishInput{}, Scopes: []string{"content.edit", "content.publish"}, Sensitive: true, Handler: bindLayout(func(ctx context.Context, in layoutPublishInput) (any, error) {
		return a.publishLayout(ctx, in.ID, in.UpdatedAt)
	}), Preview: layoutPreview(func(ctx context.Context, in layoutPublishInput) (string, error) {
		return a.publishPreview(ctx, in.ID, in.UpdatedAt)
	})})
	d.MCP.AddTool(kernel.MCPTool{Name: "list_page_layout_revisions", Title: "List page layout revisions", Toolset: kernel.MCPToolsetBuilder, ReadOnly: true, Description: "List private page-builder layout revisions for a page, newest first. Does not change the draft or public page.", Input: layoutIDInput{}, Scopes: []string{"content.edit"}, Handler: bindLayout(func(ctx context.Context, in layoutIDInput) (any, error) {
		return a.revisions(ctx, in.ID)
	})})
	d.MCP.AddTool(kernel.MCPTool{Name: "get_page_layout_revision", Title: "Get page layout revision", Toolset: kernel.MCPToolsetBuilder, ReadOnly: true, Description: "Read one private page-builder layout revision document for preview or comparison.", Input: layoutRevisionInput{}, Scopes: []string{"content.edit"}, Handler: bindLayout(func(ctx context.Context, in layoutRevisionInput) (any, error) {
		return a.revision(ctx, in.ID, in.RevisionID)
	})})
	d.MCP.AddTool(kernel.MCPTool{Name: "restore_page_layout_revision", Title: "Restore page layout revision", Toolset: kernel.MCPToolsetBuilder, Destructive: true, Description: "Restore a prior page-builder layout revision into the private draft. Public HTML is unchanged until publish. Requires human approval and the current draft updated_at token.", Input: layoutRestoreInput{}, Scopes: []string{"content.edit"}, Sensitive: true, Handler: bindLayout(func(ctx context.Context, in layoutRestoreInput) (any, error) {
		return a.restore(ctx, in.ID, in.RevisionID, in.UpdatedAt)
	}), Preview: layoutPreview(func(ctx context.Context, in layoutRestoreInput) (string, error) {
		return a.restorePreview(ctx, in.ID, in.RevisionID, in.UpdatedAt)
	})})
}

type layoutPlanInput struct {
	ID       string   `json:"id"`
	Document Document `json:"document"`
}
type layoutPlanRequest struct {
	Document Document `json:"document"`
}

// LayoutPlan is what a save would do, without doing it.
type LayoutPlan struct {
	// Valid reports whether the document passes validation; Errors says why
	// not. Publishable reports whether, once saved, it could be published.
	Valid       bool              `json:"valid"`
	Errors      map[string]string `json:"errors,omitempty"`
	Publishable bool              `json:"publishable"`
	// Diff lists what the document changes against the current draft.
	Diff LayoutDiff `json:"diff"`
	// Plan is Diff as text.
	Plan string `json:"plan"`
	// BaseUpdatedAt is the token a following save_page_layout must pass.
	BaseUpdatedAt string `json:"base_updated_at" format:"-"`
	// DraftInSync reports whether the current draft equals the published
	// layout, so this change is the only unpublished work once saved.
	DraftInSync bool `json:"draft_in_sync"`
}

// layoutPreview adapts a typed preview to kernel.MCPTool.Preview, decoding
// the parked arguments the way the handler would.
func layoutPreview[T any](fn func(ctx context.Context, in T) (string, error)) func(context.Context, json.RawMessage) (string, error) {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		var in T
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			return "", err
		}
		return fn(ctx, in)
	}
}

// plan is the dry run behind plan_page_layout and POST /v1/builder/{id}/plan.
func (a *layoutAPI) plan(ctx context.Context, id string, doc Document) (*LayoutPlan, error) {
	draft, err := a.load(ctx, id)
	if err != nil {
		return nil, err
	}
	out := &LayoutPlan{Valid: true, BaseUpdatedAt: draft.UpdatedAt, DraftInSync: draft.InSync()}
	if err := Validate(doc); err != nil {
		out.Valid, out.Errors = false, map[string]string{"document": err.Error()}
	} else if err := ValidatePublication(doc); err == nil {
		out.Publishable = true
	}
	out.Diff = Diff(draft.Document, doc)
	out.Plan = out.Diff.Text()
	return out, nil
}

func (a *layoutAPI) planHTTP(w http.ResponseWriter, r *http.Request) {
	var req layoutPlanRequest
	if err := decodeBuilder(w, r, &req); err != nil {
		httpx.Error(w, 400, "validation_failed", "invalid JSON body", map[string]string{"body": err.Error()})
		return
	}
	plan, err := a.plan(r.Context(), chi.URLParam(r, "id"), req.Document)
	if err != nil {
		a.writeError(w, err)
		return
	}
	httpx.JSON(w, 200, plan, nil)
}

// publishPreview says what publishing the draft now would change on the
// public page: the published layout against the draft, and whether the
// parked token still matches.
func (a *layoutAPI) publishPreview(ctx context.Context, id, token string) (string, error) {
	draft, err := a.load(ctx, id)
	if err != nil {
		return "", err
	}
	var published Document
	if draft.HasPublished {
		if err := json.Unmarshal([]byte(draft.published), &published); err != nil {
			return "", err
		}
	}
	text := Diff(published, draft.Document).Text()
	if !draft.HasPublished {
		text = "first publication from the builder: " + text
	}
	if draft.UpdatedAt != token {
		text += "\n! the draft changed since this was requested; the call will be refused (conflict)"
	}
	if err := ValidatePublication(draft.Document); err != nil {
		text += "\n! the draft cannot be published: " + err.Error()
	}
	return text, nil
}

// restorePreview says what restoring a revision into the draft would change.
func (a *layoutAPI) restorePreview(ctx context.Context, id, revisionID, token string) (string, error) {
	draft, err := a.load(ctx, id)
	if err != nil {
		return "", err
	}
	rev, err := a.revision(ctx, id, revisionID)
	if err != nil {
		return "", err
	}
	text := Diff(draft.Document, rev.Document).Text()
	if draft.UpdatedAt != token {
		text += "\n! the draft changed since this was requested; the call will be refused (conflict)"
	}
	return text, nil
}

type schemaInput struct {
	Locale string `json:"locale,omitempty" enum:",ar,en" doc:"Locale of the kind labels; empty for English."`
}

func (a *layoutAPI) schema(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, 200, Schema(i18n.Locale(r.Context())), nil)
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
func (a *layoutAPI) revisions(ctx context.Context, id string) ([]LayoutRevision, error) {
	if err := layoutPermission(ctx, false); err != nil {
		return nil, err
	}
	if err := validateLayoutID(id); err != nil {
		return nil, err
	}
	list, err := a.svc.ListRevisions(ctx, id)
	if err != nil {
		return nil, layoutFailure(err)
	}
	return list, nil
}
func (a *layoutAPI) revision(ctx context.Context, id, revisionID string) (*LayoutRevision, error) {
	if err := layoutPermission(ctx, false); err != nil {
		return nil, err
	}
	if err := validateLayoutID(id); err != nil {
		return nil, err
	}
	if revisionID == "" || len(revisionID) > 80 {
		return nil, invalidLayout("invalid revision ID", map[string]string{"revision_id": "revision ID required"})
	}
	rev, err := a.svc.GetRevision(ctx, id, revisionID)
	if err != nil {
		return nil, layoutFailure(err)
	}
	return rev, nil
}
func (a *layoutAPI) restore(ctx context.Context, id, revisionID, token string) (*Draft, error) {
	if err := layoutPermission(ctx, false); err != nil {
		return nil, err
	}
	if err := validateLayoutID(id); err != nil {
		return nil, err
	}
	if revisionID == "" || len(revisionID) > 80 {
		return nil, invalidLayout("invalid revision ID", map[string]string{"revision_id": "revision ID required"})
	}
	draft, err := a.svc.RestoreRevision(ctx, id, revisionID, token)
	if err != nil {
		return nil, layoutFailure(err)
	}
	return draft, nil
}
func (a *layoutAPI) createPurpose(ctx context.Context, in PurposePageInput) (*PurposePage, error) {
	page, err := a.svc.CreatePurposePage(ctx, in)
	if errors.Is(err, ErrPreset) {
		return nil, invalidLayout("invalid purpose page", map[string]string{"purpose": "use about, service, methodology or contact", "title": "1 to 200 characters", "locale": "ar, en or empty"})
	}
	if err != nil {
		var ve *content.ValidationError
		if errors.As(err, &ve) {
			return nil, invalidLayout("invalid purpose page", map[string]string(ve.Fields))
		}
		return nil, layoutFailure(err)
	}
	return page, nil
}
func (a *layoutAPI) createPurposePage(w http.ResponseWriter, r *http.Request) {
	var in PurposePageInput
	if err := decodeBuilder(w, r, &in); err != nil {
		writeDecodeError(w, err)
		return
	}
	page, err := a.createPurpose(r.Context(), in)
	if err != nil {
		a.writeError(w, err)
		return
	}
	httpx.JSON(w, 201, page, nil)
}
func (a *layoutAPI) get(w http.ResponseWriter, r *http.Request) {
	draft, err := a.load(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		a.writeError(w, err)
		return
	}
	httpx.JSON(w, 200, draft, nil)
}
func (a *layoutAPI) listRevisions(w http.ResponseWriter, r *http.Request) {
	list, err := a.revisions(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		a.writeError(w, err)
		return
	}
	httpx.JSON(w, 200, list, nil)
}
func (a *layoutAPI) getRevision(w http.ResponseWriter, r *http.Request) {
	rev, err := a.revision(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "revision"))
	if err != nil {
		a.writeError(w, err)
		return
	}
	httpx.JSON(w, 200, rev, nil)
}
func (a *layoutAPI) restoreRevision(w http.ResponseWriter, r *http.Request) {
	var in layoutRestoreRequest
	if err := decodeBuilder(w, r, &in); err != nil {
		writeDecodeError(w, err)
		return
	}
	draft, err := a.restore(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "revision"), in.UpdatedAt)
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
