package media

import (
	"errors"
	"fmt"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
)

const (
	libraryPerPage    = 30
	pickerPerPage     = 24
	maxFilesPerUpload = 10
	multipartMemory   = 8 << 20
)

type adminHandlers struct {
	svc *service
	tpl *httpx.Templates
	log *slog.Logger
}

// gridItem is the view model of one library tile.
type gridItem struct {
	ID, Filename, Title, Alt, URL, Thumb, Mime, Icon, Size, Dims string
	IsImage                                                      bool
}

func toGridItem(s *service, m Media) gridItem {
	it := gridItem{ID: m.ID, Filename: m.Filename, Title: m.Title, Alt: m.Alt, URL: m.URL, Mime: m.Mime,
		Size: humanSize(m.Size), IsImage: strings.HasPrefix(m.Mime, "image/")}
	if m.Width > 0 {
		it.Dims = fmt.Sprintf("%d×%d", m.Width, m.Height)
	}
	if it.IsImage {
		it.Thumb = s.URL(&m, "thumbnail")
	}
	switch {
	case strings.HasPrefix(m.Mime, "video/"):
		it.Icon = "film"
	case strings.HasPrefix(m.Mime, "audio/"):
		it.Icon = "music"
	case m.Mime == "application/zip":
		it.Icon = "file-archive"
	default:
		it.Icon = "file-text"
	}
	return it
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// uploadError is one rejected file; Key is an i18n key.
type uploadError struct{ Filename, Key string }

func errorKey(err error) string {
	switch {
	case errors.Is(err, ErrTooLarge):
		return "media.error.too_large"
	case errors.Is(err, ErrNotAllowed):
		return "media.error.not_allowed"
	case errors.Is(err, ErrEmpty):
		return "media.error.empty"
	}
	return "media.error.upload_failed"
}

// listData is shared by the library page and the picker.
type listData struct {
	Items                         []gridItem
	Q, Target                     string
	Kind, SizeFilter, FilterQuery string
	PrevURL, NextURL              string
	Page, Pages                   int
	Total                         int
	HasPrev                       bool
	HasNext                       bool
	Prev, Next                    int
	Errors                        []uploadError
	Uploaded                      int
	CanDelete                     bool
	MaxUploadMB                   int64
	AllowedMimes                  string
}

func (h *adminHandlers) list(r *http.Request, q string, page, perPage int) (listData, error) {
	query := ListQuery{Search: q, Page: page, PerPage: perPage}
	kind := r.URL.Query().Get("kind")
	prefix := map[string]string{"image": "image/", "video": "video/", "audio": "audio/", "document": "application/"}[kind]
	if prefix == "" {
		kind = ""
	}
	query.MimePrefix = prefix
	size := r.URL.Query().Get("size")
	switch size {
	case "small":
		query.MaxSize = (1 << 20) - 1
	case "large":
		query.MinSize = 1 << 20
	case "huge":
		query.MinSize = 10 << 20
	default:
		size = ""
	}
	items, total, err := h.svc.List(r.Context(), query)
	if err != nil {
		return listData{}, err
	}
	p := httpx.NewPagination(page, perPage, total)
	filters := url.Values{}
	for key, value := range map[string]string{"q": q, "kind": kind, "size": size} {
		if value != "" {
			filters.Set(key, value)
		}
	}
	d := listData{Kind: kind, SizeFilter: size, FilterQuery: filters.Encode(), Q: q, Page: p.Page, Pages: p.Pages, Total: total, HasPrev: p.HasPrev, HasNext: p.HasNext,
		Prev: p.Prev, Next: p.Next, CanDelete: auth.Can(r.Context(), "media.delete"),
		MaxUploadMB: h.svc.maxUpload(r.Context()) >> 20, AllowedMimes: strings.Join(h.svc.allowedMimes(r.Context()), ",")}
	filters.Set("page", strconv.Itoa(p.Prev))
	d.PrevURL = "/admin/media?" + filters.Encode()
	filters.Set("page", strconv.Itoa(p.Next))
	d.NextURL = "/admin/media?" + filters.Encode()
	for _, m := range items {
		d.Items = append(d.Items, toGridItem(h.svc, m))
	}
	return d, nil
}

func pageParam(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("page"))
	return max(n, 1)
}

func (h *adminHandlers) library(w http.ResponseWriter, r *http.Request) {
	d, err := h.list(r, strings.TrimSpace(r.URL.Query().Get("q")), pageParam(r), libraryPerPage)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.tpl.Render(w, r, "pages/media/library.html", d)
}

func (h *adminHandlers) picker(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d, err := h.list(r, strings.TrimSpace(q.Get("q")), pageParam(r), pickerPerPage)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	d.Target = q.Get("target")
	h.tpl.RenderWith(w, r, http.StatusOK, "", "pages/media/picker.html", d)
}

// upload accepts one or more "file" parts. HTMX callers get the library
// (or, with view=picker, the picker) re-rendered with per-file errors;
// plain form posts are redirected back with a flash.
func (h *adminHandlers) upload(w http.ResponseWriter, r *http.Request) {
	limit := h.svc.maxUpload(r.Context())*maxFilesPerUpload + 1<<20
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	var errs []uploadError
	uploaded := 0
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		errs = append(errs, uploadError{Key: "media.error.too_large"})
	} else {
		defer r.MultipartForm.RemoveAll()
		files := r.MultipartForm.File["file"]
		if len(files) == 0 {
			errs = append(errs, uploadError{Key: "media.error.no_file"})
		}
		if len(files) > maxFilesPerUpload {
			files = files[:maxFilesPerUpload]
			errs = append(errs, uploadError{Key: "media.error.too_many"})
		}
		for _, fh := range files {
			if err := h.saveOne(r, fh); err != nil {
				errs = append(errs, uploadError{Filename: fh.Filename, Key: errorKey(err)})
				if errorKey(err) == "media.error.upload_failed" {
					h.log.Error("media: upload", "filename", fh.Filename, "err", err)
				}
				continue
			}
			uploaded++
		}
	}
	if r.Header.Get("HX-Request") != "true" {
		if len(errs) > 0 {
			httpx.SetFlash(w, "error", errs[0].Key)
		} else {
			httpx.SetFlash(w, "success", "media.flash.uploaded")
		}
		http.Redirect(w, r, "/admin/media", http.StatusSeeOther)
		return
	}
	view := r.FormValue("view")
	perPage, page := libraryPerPage, "pages/media/library.html"
	if view == "picker" {
		perPage, page = pickerPerPage, "pages/media/picker.html"
	}
	d, err := h.list(r, "", 1, perPage)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	d.Errors, d.Uploaded, d.Target = errs, uploaded, r.FormValue("target")
	h.tpl.RenderWith(w, r, http.StatusOK, "", page, d)
}

func (h *adminHandlers) saveOne(r *http.Request, fh *multipart.FileHeader) error {
	f, err := fh.Open()
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = h.svc.Upload(r.Context(), fh.Filename, f, UploadMeta{})
	return err
}

type editData struct {
	Media     *Media
	Item      gridItem
	CanDelete bool
	Variants  []namedVariant
}

type namedVariant struct {
	Name string
	Variant
}

func (h *adminHandlers) edit(w http.ResponseWriter, r *http.Request) {
	m, err := h.svc.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.notFoundOr(w, r, err)
		return
	}
	d := editData{Media: m, Item: toGridItem(h.svc, *m), CanDelete: auth.Can(r.Context(), "media.delete")}
	for _, spec := range variantSpecs {
		if v, ok := m.Variants[spec.Name]; ok {
			d.Variants = append(d.Variants, namedVariant{Name: spec.Name, Variant: v})
		}
	}
	h.tpl.Render(w, r, "pages/media/edit.html", d)
}

// thumbData is the JSON of GET /admin/x/media/{id}/thumb.
type thumbData struct {
	ID       string `json:"id"`
	URL      string `json:"url"`   // the original file
	Thumb    string `json:"thumb"` // thumbnail variant; "" for non-images
	Alt      string `json:"alt"`
	Filename string `json:"filename"`
	Mime     string `json:"mime"`
}

// thumb answers {id,url,thumb,alt,filename,mime} for one media item, so
// editors can preview saved media values (content's media slot fetches it
// on load). Open to anyone who can view content (the editor needs no
// more) or browse media.
func (h *adminHandlers) thumb(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !auth.Can(ctx, "media.upload") && !auth.Can(ctx, "content.edit") && !auth.Can(ctx, "content.view") {
		httpx.Error(w, http.StatusForbidden, "forbidden", "missing permission", nil)
		return
	}
	m, err := h.svc.Get(ctx, chi.URLParam(r, "id"))
	if errors.Is(err, ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "media not found", nil)
		return
	}
	if err != nil {
		h.log.Error("media: thumb", "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "internal error", nil)
		return
	}
	it := toGridItem(h.svc, *m)
	w.Header().Set("Cache-Control", "private, max-age=60")
	httpx.JSON(w, http.StatusOK, thumbData{ID: m.ID, URL: m.URL, Thumb: it.Thumb, Alt: m.Alt, Filename: m.Filename, Mime: m.Mime}, nil)
}

type metaForm struct {
	Alt     string `form:"alt"`
	Caption string `form:"caption"`
	Title   string `form:"title"`
}

func (h *adminHandlers) update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var f metaForm
	if err := httpx.Bind(r, &f); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	if _, err := h.svc.Update(r.Context(), id, Patch{Alt: &f.Alt, Caption: &f.Caption, Title: &f.Title}); err != nil {
		h.notFoundOr(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "media.flash.saved")
	redirect(w, r, "/admin/media/"+id)
}

func (h *adminHandlers) remove(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		h.notFoundOr(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "media.flash.deleted")
	redirect(w, r, "/admin/media")
}

// redirect answers HTMX with HX-Redirect and browsers with 303.
func redirect(w http.ResponseWriter, r *http.Request, to string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", to)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func (h *adminHandlers) notFoundOr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrNotFound) {
		h.tpl.RenderError(w, r, http.StatusNotFound)
		return
	}
	h.fail(w, r, err)
}

func (h *adminHandlers) fail(w http.ResponseWriter, r *http.Request, err error) {
	h.log.Error("media: admin", "err", err, "path", httpx.RedactPath(r.URL.Path))
	h.tpl.RenderError(w, r, http.StatusInternalServerError)
}
