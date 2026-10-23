package media

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

// UploadRequest documents POST /v1/media (multipart/form-data).
type UploadRequest struct {
	File    string `json:"file" format:"binary" doc:"the file part (multipart/form-data)"`
	Alt     string `json:"alt,omitempty"`
	Caption string `json:"caption,omitempty"`
	Title   string `json:"title,omitempty"`
}

// DeleteResponse is returned by DELETE /v1/media/{id}.
type DeleteResponse struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

var idParam = kernel.APIParam{Name: "id", In: "path", Type: "string", Required: true, Description: "media ID"}

var listParams = []kernel.APIParam{
	{Name: "search", In: "query", Type: "string", Description: "matches filename, title and alt"},
	{Name: "page", In: "query", Type: "integer", Description: "page number (default 1)"},
	{Name: "per_page", In: "query", Type: "integer", Description: "items per page (default 20, max 100)"},
	{Name: "sort", In: "query", Type: "string", Description: "created_at | filename | size | title"},
	{Name: "order", In: "query", Type: "string", Description: "asc | desc (default desc)"},
}

func registerAPI(d *kernel.Deps, svc *service) {
	a := &apiHandlers{svc: svc}
	tags := []string{"media"}
	d.API.Add(kernel.APIOperation{Method: http.MethodPost, Path: "/v1/media", Summary: "Upload a media file",
		Tags: tags, Scopes: []string{"media.upload"}, Request: UploadRequest{}, Response: Media{}, Handler: a.upload,
		Contract: &kernel.APIContract{SuccessStatuses: []int{201}, RequestContentType: "multipart/form-data", Limits: map[string]any{"media_upload": map[string]any{"default_file_bytes": defaultMaxUploadMB << 20, "setting": SettingMaxUploadMB, "multipart_overhead_bytes": 1 << 20, "effective_limit_source": "site setting; query settings with settings.manage"}}, Errors: []kernel.APIErrorResponse{
			{Status: 413, Code: "too_large", Description: "File exceeds the configured media.max_upload_mb limit."},
			{Status: 415, Code: "unsupported_media_type", Description: "Detected file type is outside the configured media allowlist."},
		}}})
	d.API.Add(kernel.APIOperation{Method: http.MethodGet, Path: "/v1/media", Summary: "List media",
		Tags: tags, Scopes: []string{"media.upload"}, Response: []Media{}, Params: listParams, Handler: a.list})
	d.API.Add(kernel.APIOperation{Method: http.MethodGet, Path: "/v1/media/{id}", Summary: "Get a media item",
		Tags: tags, Scopes: []string{"media.upload"}, Response: Media{}, Params: []kernel.APIParam{idParam}, Handler: a.get})
	d.API.Add(kernel.APIOperation{Method: http.MethodPatch, Path: "/v1/media/{id}", Summary: "Update alt, caption, title or focal point",
		Tags: tags, Scopes: []string{"media.upload"}, Request: Patch{}, Response: Media{}, Params: []kernel.APIParam{idParam}, Handler: a.patch})
	d.API.Add(kernel.APIOperation{Method: http.MethodDelete, Path: "/v1/media/{id}", Summary: "Delete a media item",
		Tags: tags, Scopes: []string{"media.delete"}, Response: DeleteResponse{}, Params: []kernel.APIParam{idParam}, Handler: a.remove})
}

type apiHandlers struct{ svc *service }

func allowed(w http.ResponseWriter, r *http.Request, perm string) bool {
	if !auth.Can(r.Context(), perm) {
		httpx.Error(w, http.StatusForbidden, "forbidden", "missing permission "+perm, nil)
		return false
	}
	return true
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", "media not found", nil)
	case errors.Is(err, ErrTooLarge):
		httpx.Error(w, http.StatusRequestEntityTooLarge, "too_large", err.Error(), nil)
	case errors.Is(err, ErrNotAllowed):
		httpx.Error(w, http.StatusUnsupportedMediaType, "unsupported_media_type", err.Error(), nil)
	case errors.Is(err, ErrFocal):
		httpx.Error(w, http.StatusUnprocessableEntity, "validation_failed", err.Error(), map[string]string{"focal": "x and y between 0 and 1"})
	case errors.Is(err, ErrEmpty):
		httpx.Error(w, http.StatusUnprocessableEntity, "validation_failed", "empty file", map[string]string{"file": "required"})
	default:
		httpx.Error(w, http.StatusInternalServerError, "internal", "internal error", nil)
	}
}

func (a *apiHandlers) upload(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r, "media.upload") {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, a.svc.maxUpload(r.Context())+1<<20)
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeErr(w, ErrTooLarge)
			return
		}
		httpx.Error(w, http.StatusBadRequest, "bad_request", "expected multipart/form-data with a file part", nil)
		return
	}
	defer r.MultipartForm.RemoveAll()
	f, fh, err := r.FormFile("file")
	if err != nil {
		httpx.Error(w, http.StatusUnprocessableEntity, "validation_failed", "file is required", map[string]string{"file": "required"})
		return
	}
	defer f.Close()
	m, err := a.svc.Upload(r.Context(), fh.Filename, f, UploadMeta{Alt: r.FormValue("alt"),
		Caption: r.FormValue("caption"), Title: r.FormValue("title")})
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, m, nil)
}

func (a *apiHandlers) list(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r, "media.upload") {
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	perPage, _ := strconv.Atoi(q.Get("per_page"))
	page, perPage = normalizePage(page, perPage)
	items, total, err := a.svc.List(r.Context(), ListQuery{Search: q.Get("search"), Page: page, PerPage: perPage,
		Sort: q.Get("sort"), Order: q.Get("order")})
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, items, httpx.Meta{Total: total, Page: page, PerPage: perPage})
}

func (a *apiHandlers) get(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r, "media.upload") {
		return
	}
	m, err := a.svc.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, m, nil)
}

func (a *apiHandlers) patch(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r, "media.upload") {
		return
	}
	var p Patch
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&p); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body", nil)
		return
	}
	m, err := a.svc.Update(r.Context(), chi.URLParam(r, "id"), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, m, nil)
}

func (a *apiHandlers) remove(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r, "media.delete") {
		return
	}
	id := chi.URLParam(r, "id")
	if err := a.svc.Delete(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, DeleteResponse{ID: id, Deleted: true}, nil)
}
