package taxonomy

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

const maxAPIBody = 1 << 20

type api struct {
	svc *service
	d   *kernel.Deps
}

// registerAPI adds the /v1/taxonomies operations (routed by the api module).
func registerAPI(d *kernel.Deps, svc *service) {
	a := &api{svc: svc, d: d}
	tax := kernel.APIParam{Name: "tax", In: "path", Type: "string", Required: true, Description: "taxonomy slug"}
	id := kernel.APIParam{Name: "id", In: "path", Type: "string", Required: true, Description: "term id"}
	q := func(name, typ, desc string) kernel.APIParam {
		return kernel.APIParam{Name: name, In: "query", Type: typ, Description: desc}
	}
	for _, op := range []kernel.APIOperation{
		{Method: "GET", Path: "/v1/taxonomies", Summary: "List taxonomies", Tags: []string{"taxonomies"}, Scopes: []string{"content.view"},
			Response: []Taxonomy{}, Handler: a.list},
		{Method: "POST", Path: "/v1/taxonomies", Summary: "Create or replace a taxonomy", Tags: []string{"taxonomies"},
			Scopes: []string{"taxonomy.manage"}, Request: Taxonomy{}, Response: Taxonomy{}, Handler: a.create},
		{Method: "GET", Path: "/v1/taxonomies/{tax}", Summary: "Get a taxonomy", Tags: []string{"taxonomies"}, Scopes: []string{"content.view"},
			Params: []kernel.APIParam{tax}, Response: Taxonomy{}, Handler: a.get},
		{Method: "PATCH", Path: "/v1/taxonomies/{tax}", Summary: "Update a taxonomy", Tags: []string{"taxonomies"},
			Scopes: []string{"taxonomy.manage"}, Params: []kernel.APIParam{tax}, Request: Taxonomy{}, Response: Taxonomy{}, Handler: a.patch},
		{Method: "DELETE", Path: "/v1/taxonomies/{tax}", Summary: "Delete a taxonomy and its terms", Tags: []string{"taxonomies"},
			Scopes: []string{"taxonomy.manage"}, Params: []kernel.APIParam{tax}, Handler: a.delete},
		{Method: "GET", Path: "/v1/taxonomies/{tax}/terms", Summary: "List terms", Tags: []string{"taxonomies"}, Scopes: []string{"content.view"},
			Params: []kernel.APIParam{tax, q("search", "string", ""), q("parent_id", "string", "0 for top level"), q("page", "integer", ""),
				q("per_page", "integer", "max 500")}, Response: []Term{}, Handler: a.listTerms},
		{Method: "POST", Path: "/v1/taxonomies/{tax}/terms", Summary: "Create a term", Tags: []string{"taxonomies"},
			Scopes: []string{"taxonomy.manage"}, Params: []kernel.APIParam{tax}, Request: Term{}, Response: Term{}, Handler: a.createTerm},
		{Method: "GET", Path: "/v1/taxonomies/{tax}/terms/{id}", Summary: "Get a term", Tags: []string{"taxonomies"},
			Scopes: []string{"content.view"}, Params: []kernel.APIParam{tax, id}, Response: Term{}, Handler: a.getTerm},
		{Method: "PATCH", Path: "/v1/taxonomies/{tax}/terms/{id}", Summary: "Update a term", Tags: []string{"taxonomies"},
			Scopes: []string{"taxonomy.manage"}, Params: []kernel.APIParam{tax, id}, Request: Term{}, Response: Term{}, Handler: a.patchTerm},
		{Method: "DELETE", Path: "/v1/taxonomies/{tax}/terms/{id}", Summary: "Delete a term (children move up)", Tags: []string{"taxonomies"},
			Scopes: []string{"taxonomy.manage"}, Params: []kernel.APIParam{tax, id}, Handler: a.deleteTerm},
	} {
		d.API.Add(op)
	}
}

func (a *api) allow(w http.ResponseWriter, r *http.Request, perm string) bool {
	if auth.Can(r.Context(), perm) {
		return true
	}
	httpx.Error(w, http.StatusForbidden, "forbidden", "missing permission "+perm, nil)
	return false
}

func (a *api) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, maxAPIBody)).Decode(dst); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body", nil)
		return false
	}
	return true
}

func (a *api) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ve *content.ValidationError
	switch {
	case errors.As(err, &ve):
		out := map[string]string{}
		for k, v := range ve.Fields {
			out[k] = v
			if a.d.I18n != nil {
				out[k] = a.d.I18n.T(i18n.Locale(r.Context()), v)
			}
		}
		httpx.Error(w, http.StatusUnprocessableEntity, "validation_failed", "validation failed", out)
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", "not found", nil)
	case errors.Is(err, ErrSystem):
		httpx.Error(w, http.StatusConflict, "conflict", err.Error(), nil)
	default:
		a.d.Log.Error("taxonomy api", "err", err, "path", r.URL.Path)
		httpx.Error(w, http.StatusInternalServerError, "internal", "internal error", nil)
	}
}

func (a *api) list(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "content.view") {
		return
	}
	list, err := a.svc.Taxonomies(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, list, nil)
}

func (a *api) get(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "content.view") {
		return
	}
	t, err := a.svc.Get(r.Context(), chi.URLParam(r, "tax"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, t, nil)
}

func (a *api) create(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "taxonomy.manage") {
		return
	}
	var t Taxonomy
	if !a.decode(w, r, &t) {
		return
	}
	a.save(w, r, t, http.StatusCreated)
}

// patch replaces the top-level properties present in the body.
func (a *api) patch(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "taxonomy.manage") {
		return
	}
	cur, err := a.svc.Get(r.Context(), chi.URLParam(r, "tax"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var t Taxonomy
	if !a.overlay(w, r, cur, &t) {
		return
	}
	t.Slug = cur.Slug
	a.save(w, r, t, http.StatusOK)
}

// overlay writes into dst the JSON of cur with the body's top-level keys
// replacing cur's (decoding into a fresh value, so maps and lists are
// replaced rather than merged).
func (a *api) overlay(w http.ResponseWriter, r *http.Request, cur, dst any) bool {
	var body map[string]json.RawMessage
	if !a.decode(w, r, &body) {
		return false
	}
	merged := map[string]json.RawMessage{}
	base, _ := json.Marshal(cur)
	_ = json.Unmarshal(base, &merged)
	for k, v := range body {
		merged[k] = v
	}
	raw, _ := json.Marshal(merged)
	if err := json.Unmarshal(raw, dst); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body", nil)
		return false
	}
	return true
}

func (a *api) save(w http.ResponseWriter, r *http.Request, t Taxonomy, status int) {
	if err := a.svc.Save(r.Context(), t); err != nil {
		a.fail(w, r, err)
		return
	}
	saved, err := a.svc.Get(r.Context(), t.Slug)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, status, saved, nil)
}

func (a *api) delete(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "taxonomy.manage") {
		return
	}
	if err := a.svc.Delete(r.Context(), chi.URLParam(r, "tax")); err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"deleted": true}, nil)
}

func (a *api) listTerms(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "content.view") {
		return
	}
	qs := r.URL.Query()
	q := TermQuery{Search: qs.Get("search"), ParentID: qs.Get("parent_id")}
	q.Page, _ = strconv.Atoi(qs.Get("page"))
	q.PerPage, _ = strconv.Atoi(qs.Get("per_page"))
	terms, total, err := a.svc.Terms(r.Context(), chi.URLParam(r, "tax"), q)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	page, per := max(q.Page, 1), q.PerPage
	if per <= 0 {
		per = defaultPerPage
	}
	httpx.JSON(w, http.StatusOK, terms, httpx.Meta{Total: total, Page: page, PerPage: min(per, maxPerPage)})
}

// termIn loads the term {id} and checks it belongs to {tax}.
func (a *api) termIn(w http.ResponseWriter, r *http.Request) (*Term, bool) {
	t, err := a.svc.GetTerm(r.Context(), chi.URLParam(r, "id"))
	if err == nil && t.Taxonomy != chi.URLParam(r, "tax") {
		err = ErrNotFound
	}
	if err != nil {
		a.fail(w, r, err)
		return nil, false
	}
	return t, true
}

func (a *api) getTerm(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "content.view") {
		return
	}
	if t, ok := a.termIn(w, r); ok {
		httpx.JSON(w, http.StatusOK, t, nil)
	}
}

func (a *api) createTerm(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "taxonomy.manage") {
		return
	}
	var t Term
	if !a.decode(w, r, &t) {
		return
	}
	t.ID, t.Taxonomy = "", chi.URLParam(r, "tax")
	saved, err := a.svc.SaveTerm(r.Context(), t)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, saved, nil)
}

func (a *api) patchTerm(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "taxonomy.manage") {
		return
	}
	t, ok := a.termIn(w, r)
	if !ok {
		return
	}
	cur := *t
	var nt Term
	if !a.overlay(w, r, &cur, &nt) {
		return
	}
	nt.ID, nt.Taxonomy = cur.ID, cur.Taxonomy
	t = &nt
	saved, err := a.svc.SaveTerm(r.Context(), *t)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, saved, nil)
}

func (a *api) deleteTerm(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, "taxonomy.manage") {
		return
	}
	t, ok := a.termIn(w, r)
	if !ok {
		return
	}
	if err := a.svc.DeleteTerm(r.Context(), t.ID); err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"deleted": true}, nil)
}
