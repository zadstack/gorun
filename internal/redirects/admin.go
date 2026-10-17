package redirects

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/httpx"
)

const (
	adminPerPage  = 50
	maxImportSize = 5 << 20
)

type adminHandlers struct {
	svc *service
	tpl *httpx.Templates
	log *slog.Logger
}

type addForm struct {
	FromPath string `form:"from_path"`
	ToURL    string `form:"to_url"`
	Status   int    `form:"status"`
}

type listData struct {
	Items    []Redirect
	Q        string
	Page     httpx.Pagination
	Form     addForm
	Fields   map[string]string
	Import   *ImportResult
	Statuses []int
}

func (h *adminHandlers) render(w http.ResponseWriter, r *http.Request, status int, d listData) {
	q := r.URL.Query()
	if d.Q == "" {
		d.Q = strings.TrimSpace(q.Get("q"))
	}
	page, _ := strconv.Atoi(q.Get("page"))
	items, total, err := h.svc.List(r.Context(), ListQuery{Search: d.Q, Page: max(page, 1), PerPage: adminPerPage})
	if err != nil {
		h.log.Error("redirects: admin list", "err", err)
		h.tpl.RenderError(w, r, http.StatusInternalServerError)
		return
	}
	d.Items, d.Page, d.Statuses = items, httpx.NewPagination(max(page, 1), adminPerPage, total), []int{301, 302, 307, 308}
	if d.Form.Status == 0 {
		d.Form.Status = 301
	}
	h.tpl.RenderWith(w, r, status, "admin", "pages/redirects/list.html", d)
}

func (h *adminHandlers) list(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusOK, listData{})
}

func (h *adminHandlers) create(w http.ResponseWriter, r *http.Request) {
	var f addForm
	if err := httpx.Bind(r, &f); err != nil {
		h.render(w, r, http.StatusUnprocessableEntity, listData{Form: f, Fields: map[string]string{"status": "redirects.error.status_invalid"}})
		return
	}
	_, err := h.svc.Save(r.Context(), Redirect{FromPath: f.FromPath, ToURL: f.ToURL, Status: f.Status, Enabled: true})
	var ve *ValidationError
	if errors.As(err, &ve) {
		h.render(w, r, http.StatusUnprocessableEntity, listData{Form: f, Fields: ve.Fields})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "redirects.flash.created")
	http.Redirect(w, r, "/admin/redirects", http.StatusSeeOther)
}

// importCSV takes a multipart "file" (sent by HTMX so the CSRF header is
// present) and re-renders the page content with the import summary.
func (h *adminHandlers) importCSV(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImportSize)
	res := &ImportResult{}
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		res.Errors = append(res.Errors, RowError{Key: "redirects.import.error_file"})
		h.render(w, r, http.StatusOK, listData{Import: res})
		return
	}
	defer r.MultipartForm.RemoveAll()
	f, _, err := r.FormFile("file")
	if err != nil {
		res.Errors = append(res.Errors, RowError{Key: "redirects.import.error_file"})
		h.render(w, r, http.StatusOK, listData{Import: res})
		return
	}
	defer f.Close()
	out, err := h.svc.ImportCSV(r.Context(), f)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, http.StatusOK, listData{Import: &out})
}

func (h *adminHandlers) toggle(w http.ResponseWriter, r *http.Request) {
	rd, err := h.svc.get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.notFoundOr(w, r, err)
		return
	}
	rd.Enabled = !rd.Enabled
	if _, err := h.svc.Save(r.Context(), *rd); err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "redirects.flash.saved")
	http.Redirect(w, r, "/admin/redirects", http.StatusSeeOther)
}

func (h *adminHandlers) remove(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		h.notFoundOr(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "redirects.flash.deleted")
	http.Redirect(w, r, "/admin/redirects", http.StatusSeeOther)
}

func (h *adminHandlers) notFoundOr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrNotFound) {
		h.tpl.RenderError(w, r, http.StatusNotFound)
		return
	}
	h.fail(w, r, err)
}

func (h *adminHandlers) fail(w http.ResponseWriter, r *http.Request, err error) {
	h.log.Error("redirects: admin", "err", err, "path", httpx.RedactPath(r.URL.Path))
	h.tpl.RenderError(w, r, http.StatusInternalServerError)
}
