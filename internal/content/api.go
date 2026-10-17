package content

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

const maxAPIBody = 4 << 20

type api struct {
	svc  *service
	i18n *i18n.Bundle
	d    *kernel.Deps
}

// registerAPI adds the /v1/types and /v1/content operations to the API
// registry (the api module routes them and builds OpenAPI). Handlers also
// check permissions themselves.
func registerAPI(d *kernel.Deps, svc *service) {
	a := &api{svc: svc, i18n: d.I18n, d: d}
	slug := kernel.APIParam{Name: "slug", In: "path", Type: "string", Required: true, Description: "content type slug"}
	id := kernel.APIParam{Name: "id", In: "path", Type: "string", Required: true, Description: "content id"}
	ops := []kernel.APIOperation{
		{Method: "GET", Path: "/v1/types", Summary: "List content types", Tags: []string{"types"}, Scopes: []string{"content.view"},
			Response: []Type{}, Handler: a.listTypes},
		{Method: "POST", Path: "/v1/types", Summary: "Create or replace a content type", Tags: []string{"types"}, Scopes: []string{"types.manage"},
			Request: Type{}, Response: Type{}, Handler: a.createType},
		{Method: "GET", Path: "/v1/types/{slug}", Summary: "Get a content type", Tags: []string{"types"}, Scopes: []string{"content.view"},
			Params: []kernel.APIParam{slug}, Response: Type{}, Handler: a.getType},
		{Method: "PATCH", Path: "/v1/types/{slug}", Summary: "Update a content type (fields given replace)", Tags: []string{"types"},
			Scopes: []string{"types.manage"}, Params: []kernel.APIParam{slug}, Request: Type{}, Response: Type{}, Handler: a.patchType},
		{Method: "DELETE", Path: "/v1/types/{slug}", Summary: "Delete a content type", Tags: []string{"types"}, Scopes: []string{"types.manage"},
			Params: []kernel.APIParam{slug}, Handler: a.deleteType},
		{Method: "GET", Path: "/v1/content", Summary: "Query content", Tags: []string{"content"}, Scopes: []string{"content.view"},
			Params: listParams(), Response: []Item{}, Handler: a.listContent},
		{Method: "POST", Path: "/v1/content/query", Summary: "Query content with the full query DSL", Tags: []string{"content"},
			Scopes: []string{"content.view"}, Request: Query{}, Response: []Item{}, Handler: a.queryContent},
		{Method: "POST", Path: "/v1/content", Summary: "Create content", Tags: []string{"content"}, Scopes: []string{"content.create"},
			Request: CreateInput{}, Response: Item{}, Handler: a.createContent},
		{Method: "GET", Path: "/v1/content/{id}", Summary: "Get content", Tags: []string{"content"}, Scopes: []string{"content.view"},
			Params: []kernel.APIParam{id}, Response: Item{}, Handler: a.getContent},
		{Method: "PATCH", Path: "/v1/content/{id}", Summary: "Update content (merge: omitted fields keep their value)", Tags: []string{"content"},
			Scopes: []string{"content.edit"}, Params: []kernel.APIParam{id}, Request: PatchInput{}, Response: Item{}, Handler: a.patchContent},
		{Method: "DELETE", Path: "/v1/content/{id}", Summary: "Trash content (hard=1 deletes)", Tags: []string{"content"},
			Scopes: []string{"content.delete"}, Params: []kernel.APIParam{id, {Name: "hard", In: "query", Type: "boolean"}}, Handler: a.deleteContent},
		{Method: "POST", Path: "/v1/content/{id}/publish", Summary: "Publish content now", Tags: []string{"content"},
			Scopes: []string{"content.publish"}, Params: []kernel.APIParam{id}, Response: Item{}, Handler: a.publishContent},
		{Method: "POST", Path: "/v1/content/{id}/unpublish", Summary: "Return content to draft", Tags: []string{"content"},
			Scopes: []string{"content.publish"}, Params: []kernel.APIParam{id}, Response: Item{}, Handler: a.unpublishContent},
		{Method: "GET", Path: "/v1/content/{id}/revisions", Summary: "List revisions (newest first)", Tags: []string{"content"},
			Scopes: []string{"content.view"}, Params: []kernel.APIParam{id}, Response: []Revision{}, Handler: a.listRevisions},
		{Method: "POST", Path: "/v1/content/{id}/revisions/{revision}/restore", Summary: "Restore a revision", Tags: []string{"content"},
			Scopes: []string{"content.edit"}, Params: []kernel.APIParam{id, {Name: "revision", In: "path", Type: "string", Required: true}},
			Response: Item{}, Handler: a.restoreRevision},
		{Method: "GET", Path: "/v1/comments", Summary: "List comments (newest first)", Tags: []string{"comments"},
			Scopes: []string{PermModerateComments}, Params: []kernel.APIParam{
				{Name: "status", In: "query", Type: "string", Description: "pending, approved, spam or trash"},
				{Name: "content_id", In: "query", Type: "string"}, {Name: "page", In: "query", Type: "integer"},
				{Name: "per_page", In: "query", Type: "integer"}}, Response: []Comment{}, Handler: a.listComments},
		{Method: "PATCH", Path: "/v1/comments/{id}", Summary: "Moderate a comment (set its status)", Tags: []string{"comments"},
			Scopes: []string{PermModerateComments}, Params: []kernel.APIParam{id}, Request: commentStatusInput{}, Response: Comment{}, Handler: a.patchComment},
		{Method: "DELETE", Path: "/v1/comments/{id}", Summary: "Delete a comment permanently", Tags: []string{"comments"},
			Scopes: []string{PermModerateComments}, Params: []kernel.APIParam{id}, Handler: a.deleteComment},
		{Method: "GET", Path: "/v1/content/{id}/translations", Summary: "List the item and its translations (by locale)", Tags: []string{"content"},
			Scopes: []string{"content.view"}, Params: []kernel.APIParam{id}, Response: []Item{}, Handler: a.listTranslations},
		{Method: "POST", Path: "/v1/content/{id}/translations", Summary: "Copy the item into another locale as a linked draft", Tags: []string{"content"},
			Scopes: []string{"content.create"}, Params: []kernel.APIParam{id}, Request: translationInput{}, Response: Item{}, Handler: a.createTranslation},
		{Method: "POST", Path: "/v1/content/{id}/translations/link", Summary: "Link another item (and its translations) as translations", Tags: []string{"content"},
			Scopes: []string{"content.edit"}, Params: []kernel.APIParam{id}, Request: linkInput{}, Response: []Item{}, Handler: a.linkTranslation},
		{Method: "DELETE", Path: "/v1/content/{id}/translations", Summary: "Take the item out of its translations", Tags: []string{"content"},
			Scopes: []string{"content.edit"}, Params: []kernel.APIParam{id}, Response: []Item{}, Handler: a.unlinkTranslation},
	}
	for _, op := range ops {
		if op.Method == "POST" || op.Method == "PATCH" {
			op.Contract = &kernel.APIContract{}
			if op.Request != nil {
				op.Contract.MaxBodyBytes = maxAPIBody
			}
		}
		if op.Method == "POST" && (op.Path == "/v1/content" || op.Path == "/v1/types" || op.Path == "/v1/content/{id}/translations") {
			op.Contract.SuccessStatuses = []int{http.StatusCreated}
		}
		if op.Path == "/v1/types/{slug}" && op.Method == "DELETE" {
			op.Contract = &kernel.APIContract{Errors: []kernel.APIErrorResponse{{Status: 409, Code: "conflict", Description: "A system content type or a type in use cannot be deleted."}}}
		}
		if op.Path == "/v1/content/{id}/translations/link" && op.Method == "POST" {
			op.Contract.Errors = []kernel.APIErrorResponse{{Status: 409, Code: "conflict", Description: "The current translation membership differs from expected_ids."}}
		}
		d.API.Add(op)
	}
}

func listParams() []kernel.APIParam {
	q := func(name, typ, desc string) kernel.APIParam {
		return kernel.APIParam{Name: name, In: "query", Type: typ, Description: desc}
	}
	return []kernel.APIParam{
		q("type", "string", "content type slug"), q("status", "string", "status or comma-separated statuses"),
		q("search", "string", "search title, excerpt and body"), q("author_id", "string", ""), q("parent_id", "string", "0 for top level"),
		q("locale", "string", ""), q("page", "integer", ""), q("per_page", "integer", "max 100"),
		q("order", "string", "comma-separated by[:asc|desc], e.g. price:desc,title"),
		q("taxonomy.{tax}", "string", "comma-separated term slugs"), q("fields.{key}.{op}", "string", "indexed field filter; op eq ne gt lt gte lte in like"),
	}
}

// --- helpers ---

func (a *api) allow(w http.ResponseWriter, r *http.Request, perm string) bool {
	if auth.Can(r.Context(), perm) {
		return true
	}
	httpx.Error(w, http.StatusForbidden, "forbidden", "missing permission "+perm, nil)
	return false
}

func (a *api) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxAPIBody))
	if err := dec.Decode(dst); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body", nil)
		return false
	}
	return true
}

// fail maps service errors to envelopes.
func (a *api) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ve *ValidationError
	switch {
	case errors.As(err, &ve):
		httpx.Error(w, http.StatusUnprocessableEntity, "validation_failed", "validation failed", a.translate(r, ve.Fields))
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", "not found", nil)
	case errors.Is(err, ErrForbidden):
		httpx.Error(w, http.StatusForbidden, "forbidden", "not allowed", nil)
	case errors.Is(err, ErrConflict):
		httpx.Error(w, http.StatusConflict, "conflict", "content changed; reload before retry", nil)
	case errors.Is(err, ErrSystemType), errors.Is(err, ErrTypeInUse):
		httpx.Error(w, http.StatusConflict, "conflict", err.Error(), nil)
	default:
		a.d.Log.Error("content api", "err", err, "path", r.URL.Path)
		httpx.Error(w, http.StatusInternalServerError, "internal", "internal error", nil)
	}
}

func (a *api) translate(r *http.Request, fields FieldErrors) map[string]string {
	out := map[string]string{}
	loc := i18n.Locale(r.Context())
	for k, v := range fields {
		if a.i18n != nil {
			out[k] = a.i18n.T(loc, v)
		} else {
			out[k] = v
		}
	}
	return out
}

// --- types ---

func (a *api) listTypes(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "content.view") {
		return
	}
	types, err := a.svc.Types(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, types, nil)
}

func (a *api) getType(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "content.view") {
		return
	}
	t, err := a.svc.GetType(r.Context(), chi.URLParam(r, "slug"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, t, nil)
}

func (a *api) createType(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "types.manage") {
		return
	}
	var t Type
	if !a.decode(w, r, &t) {
		return
	}
	a.saveType(w, r, t, http.StatusCreated)
}

// patchType replaces the top-level properties present in the body (a
// "fields" list replaces the whole list) and keeps the others.
func (a *api) patchType(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "types.manage") {
		return
	}
	cur, err := a.svc.GetType(r.Context(), chi.URLParam(r, "slug"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var t Type
	if !a.overlay(w, r, cur, &t) {
		return
	}
	t.Slug = cur.Slug
	a.saveType(w, r, t, http.StatusOK)
}

// overlay decodes the JSON body as an object and writes into dst the JSON of
// cur with the body's top-level keys replacing cur's. Decoding into a fresh
// value matters: decoding over cur would reuse slice elements and leak old
// values into replaced list items.
func (a *api) overlay(w http.ResponseWriter, r *http.Request, cur, dst any) bool {
	var body map[string]json.RawMessage
	if !a.decode(w, r, &body) {
		return false
	}
	return overlayJSON(w, cur, body, dst)
}

func overlayJSON(w http.ResponseWriter, cur any, body map[string]json.RawMessage, dst any) bool {
	merged := map[string]json.RawMessage{}
	base, _ := json.Marshal(cur)
	if err := json.Unmarshal(base, &merged); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "internal error", nil)
		return false
	}
	maps.Copy(merged, body)
	raw, _ := json.Marshal(merged)
	if err := json.Unmarshal(raw, dst); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body", nil)
		return false
	}
	return true
}

func (a *api) saveType(w http.ResponseWriter, r *http.Request, t Type, status int) {
	if err := a.svc.SaveType(r.Context(), t); err != nil {
		a.fail(w, r, err)
		return
	}
	saved, err := a.svc.GetType(r.Context(), t.Slug)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, status, saved, nil)
}

func (a *api) deleteType(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "types.manage") {
		return
	}
	if err := a.svc.DeleteType(r.Context(), chi.URLParam(r, "slug")); err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"deleted": true}, nil)
}

// --- content ---

// QueryFromValues parses the list query string: type, status, search,
// author_id, parent_id, locale, page, per_page, order (by[:dir],...),
// taxonomy.<tax>=a,b and fields.<key>.<op>=value (in: comma-separated).
func QueryFromValues(v url.Values) Query {
	q := Query{Type: v.Get("type"), Status: v.Get("status"), Search: v.Get("search"), AuthorID: v.Get("author_id"),
		ParentID: v.Get("parent_id"), Locale: v.Get("locale"), IncludeTrash: v.Get("include_trash") == "1"}
	q.Page, _ = strconv.Atoi(v.Get("page"))
	q.PerPage, _ = strconv.Atoi(v.Get("per_page"))
	for _, part := range strings.Split(v.Get("order"), ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		by, dir, _ := strings.Cut(part, ":")
		q.Order = append(q.Order, Order{By: by, Dir: dir})
	}
	for key, vals := range v {
		switch {
		case strings.HasPrefix(key, "taxonomy."):
			if q.Taxonomy == nil {
				q.Taxonomy = map[string][]string{}
			}
			q.Taxonomy[strings.TrimPrefix(key, "taxonomy.")] = stringList(strings.Split(strings.Join(vals, ","), ","))
		case strings.HasPrefix(key, "fields."):
			field, op, ok := strings.Cut(strings.TrimPrefix(key, "fields."), ".")
			if !ok {
				field, op = strings.TrimPrefix(key, "fields."), "eq"
			}
			if q.Fields == nil {
				q.Fields = map[string]Filter{}
			}
			var val any = vals[0]
			if op == "in" {
				val = stringList(strings.Split(vals[0], ","))
			}
			q.Fields[field] = Filter{Op: op, Value: val}
		}
	}
	return q
}

func (a *api) listContent(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "content.view") {
		return
	}
	a.runQuery(w, r, QueryFromValues(r.URL.Query()))
}

func (a *api) queryContent(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "content.view") {
		return
	}
	var q Query
	if !a.decode(w, r, &q) {
		return
	}
	a.runQuery(w, r, q)
}

func (a *api) runQuery(w http.ResponseWriter, r *http.Request, q Query) {
	page, err := a.svc.Query(r.Context(), q)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, page.Items, httpx.Meta{Total: page.Total, Page: page.Page, PerPage: page.PerPage})
}

func (a *api) getContent(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "content.view") {
		return
	}
	it, err := a.svc.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, it, nil)
}

func (a *api) createContent(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "content.create") {
		return
	}
	var in Input
	if !a.decode(w, r, &in) {
		return
	}
	it, err := a.svc.Create(r.Context(), in)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, it, nil)
}

// patchContent decodes the body over the current Input: omitted properties
// keep their value and object properties (fields, seo, terms) merge by key.
func (a *api) patchContent(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "content.edit") {
		return
	}
	cur, err := a.svc.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	in := InputFromItem(cur)
	in.Fields, in.SEO, in.Terms = maps.Clone(cur.Fields), maps.Clone(cur.SEO), maps.Clone(cur.Terms)
	if !a.decode(w, r, &in) {
		return
	}
	it, err := a.svc.Update(r.Context(), cur.ID, in)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, it, nil)
}

func (a *api) deleteContent(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "content.delete") {
		return
	}
	hard := r.URL.Query().Get("hard")
	if err := a.svc.Delete(r.Context(), chi.URLParam(r, "id"), hard == "1" || hard == "true"); err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"deleted": true}, nil)
}

func (a *api) publishContent(w http.ResponseWriter, r *http.Request) {
	a.statusChange(w, r, a.svc.Publish)
}

func (a *api) unpublishContent(w http.ResponseWriter, r *http.Request) {
	a.statusChange(w, r, a.svc.Unpublish)
}

func (a *api) statusChange(w http.ResponseWriter, r *http.Request, fn func(ctx context.Context, id string) error) {
	if !a.allow(w, r, "content.publish") {
		return
	}
	id := chi.URLParam(r, "id")
	if err := fn(r.Context(), id); err != nil {
		a.fail(w, r, err)
		return
	}
	it, err := a.svc.Get(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, it, nil)
}

func (a *api) listRevisions(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "content.view") {
		return
	}
	revs, err := a.svc.Revisions(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, revs, httpx.Meta{Total: len(revs), Page: 1, PerPage: len(revs)})
}

func (a *api) restoreRevision(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "content.edit") {
		return
	}
	it, err := a.svc.Restore(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "revision"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, it, nil)
}

// translationInput is the body of POST /v1/content/{id}/translations.
type translationInput struct {
	Locale string `json:"locale"`
}

// linkInput is the body of POST /v1/content/{id}/translations/link.
type linkInput struct {
	ID             string   `json:"id"`
	ExpectedIDs    []string `json:"expected_ids,omitempty" doc:"Optional exact union of current translation member IDs; checked atomically before merging. Explicit null is rejected with 400. Empty or stale arrays return 409. Omit for legacy behavior."`
	hasExpectedIDs bool
}

func (a *api) listTranslations(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "content.view") {
		return
	}
	a.translations(w, r, chi.URLParam(r, "id"))
}

// translations answers the group of id.
func (a *api) translations(w http.ResponseWriter, r *http.Request, id string) {
	items, err := a.svc.Translations(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, items, httpx.Meta{Total: len(items), Page: 1, PerPage: len(items)})
}

func (a *api) createTranslation(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "content.create") {
		return
	}
	var in translationInput
	if !a.decode(w, r, &in) {
		return
	}
	it, err := a.svc.CreateTranslation(r.Context(), chi.URLParam(r, "id"), in.Locale)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, it, nil)
}

func (a *api) linkTranslation(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "content.edit") {
		return
	}
	var in linkInput
	if !a.decode(w, r, &in) {
		return
	}
	id := chi.URLParam(r, "id")
	var err error
	if in.hasExpectedIDs {
		err = a.svc.LinkTranslationGuarded(r.Context(), id, in.ID, in.ExpectedIDs)
	} else {
		err = a.svc.LinkTranslation(r.Context(), id, in.ID)
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.translations(w, r, id)
}

func (a *api) unlinkTranslation(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "content.edit") {
		return
	}
	id := chi.URLParam(r, "id")
	if err := a.svc.UnlinkTranslation(r.Context(), id); err != nil {
		a.fail(w, r, err)
		return
	}
	a.translations(w, r, id)
}

// commentStatusInput is the body of PATCH /v1/comments/{id}.
type commentStatusInput struct {
	Status string `json:"status"`
}

func (a *api) listComments(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, PermModerateComments) {
		return
	}
	v := r.URL.Query()
	q := CommentQuery{Status: v.Get("status"), ContentID: v.Get("content_id")}
	q.Page, _ = strconv.Atoi(v.Get("page"))
	q.PerPage, _ = strconv.Atoi(v.Get("per_page"))
	page, err := a.svc.Comments(r.Context(), q)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, page.Items, httpx.Meta{Total: page.Total, Page: page.Page, PerPage: page.PerPage})
}

func (a *api) patchComment(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, PermModerateComments) {
		return
	}
	var in commentStatusInput
	if !a.decode(w, r, &in) {
		return
	}
	id := chi.URLParam(r, "id")
	if err := a.svc.SetCommentStatus(r.Context(), id, in.Status); err != nil {
		a.fail(w, r, err)
		return
	}
	c, err := a.svc.GetComment(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, c, nil)
}

func (a *api) deleteComment(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, PermModerateComments) {
		return
	}
	id := chi.URLParam(r, "id")
	if err := a.svc.DeleteComment(r.Context(), id); err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true}, nil)
}
