package commerce

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/i18n"
)

var digitalKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,199}$`)

func validDigitalKey(key string) bool {
	return digitalKeyPattern.MatchString(key) && key != "." && key != ".."
}
func (s *service) digitalDir() string { return filepath.Join(s.d.Config.DataDir, "commerce-downloads") }
func (s *service) digitalFileExists(key string) bool {
	if !validDigitalKey(key) {
		return false
	}
	root, e := os.OpenRoot(s.digitalDir())
	if e != nil {
		return false
	}
	defer root.Close()
	f, e := root.Open(key)
	if e != nil {
		return false
	}
	defer f.Close()
	st, e := f.Stat()
	return e == nil && st.Mode().IsRegular() && st.Size() > 0
}

// uploadDigital stores private bytes outside the public media tree. Only commerce
// managers may upload; product activation requires an actual existing file.
func (h *adminHandlers) uploadDigital(w http.ResponseWriter, r *http.Request) {
	if id := chi.URLParam(r, "id"); id != "" {
		if _, err := h.svc.productByID(r.Context(), id); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	const maxBytes = 20 << 20
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+65536)
	if e := r.ParseMultipartForm(maxBytes); e != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(e, &tooLarge) {
			h.uploadResult(w, r, 413, "", 0, "commerce.error.upload_too_large")
		} else if errors.Is(e, http.ErrNotMultipart) {
			h.uploadResult(w, r, 422, "", 0, "commerce.error.upload_required")
		} else {
			h.uploadResult(w, r, 422, "", 0, "commerce.error.upload_failed")
		}
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, e := r.FormFile("file")
	if e != nil {
		h.uploadResult(w, r, 422, "", 0, "commerce.error.upload_required")
		return
	}
	defer file.Close()
	key := db.NewID() + filepath.Ext(filepath.Base(header.Filename))
	if !validDigitalKey(key) {
		key = db.NewID() + ".bin"
	}
	if e = os.MkdirAll(h.svc.digitalDir(), 0700); e != nil {
		h.fail(w, r, e)
		return
	}
	root, e := os.OpenRoot(h.svc.digitalDir())
	if e != nil {
		h.fail(w, r, e)
		return
	}
	defer root.Close()
	dst, e := root.OpenFile(key, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	n, e := io.Copy(dst, io.LimitReader(file, maxBytes+1))
	ce := dst.Close()
	if e != nil || ce != nil || n > maxBytes {
		_ = root.Remove(key)
		h.uploadResult(w, r, 413, "", 0, "commerce.error.upload_too_large")
		return
	}
	if n == 0 {
		_ = root.Remove(key)
		h.uploadResult(w, r, 422, "", 0, "commerce.error.upload_required")
		return
	}
	h.svc.audit.Record(r.Context(), "commerce.digital.upload", "file", key, map[string]any{"bytes": n})
	h.uploadResult(w, r, http.StatusOK, key, n, "")
}

// download requires authentication, order ownership, genuine payment and an
// unexpired frozen digital item. An order cookie alone never grants delivery.
func (h *publicHandlers) download(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		h.notFound(w, r)
		return
	}
	o, e := h.svc.Order(r.Context(), chi.URLParam(r, "order"))
	if e != nil || (o.Status != OrderPaid && o.Status != OrderActive) || o.PaidAt == "" {
		h.notFound(w, r)
		return
	}
	cid, e := h.svc.customerIDForUser(r.Context(), u.ID)
	if !auth.Can(r.Context(), PermManage) && (e != nil || cid != o.CustomerID) {
		h.notFound(w, r)
		return
	}
	var raw string
	e = h.svc.db.QueryRowContext(r.Context(), `SELECT snapshot_json FROM order_items WHERE id=? AND order_id=?`, chi.URLParam(r, "item"), o.ID).Scan(&raw)
	var snap snapshot
	if e != nil || json.Unmarshal([]byte(raw), &snap) != nil || snap.Kind != ProductDigital || !validDigitalKey(snap.DigitalFile) {
		h.notFound(w, r)
		return
	}
	paid, e := time.Parse(time.RFC3339, o.PaidAt)
	if e != nil || snap.DownloadDays < 1 || !h.svc.now().Before(paid.Add(time.Duration(snap.DownloadDays)*24*time.Hour)) {
		h.notFound(w, r)
		return
	}
	root, e := os.OpenRoot(h.svc.digitalDir())
	if e != nil {
		h.notFound(w, r)
		return
	}
	defer root.Close()
	f, e := root.Open(snap.DigitalFile)
	if e != nil {
		h.notFound(w, r)
		return
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		h.notFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="download`+filepath.Ext(snap.DigitalFile)+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	h.svc.audit.Record(r.Context(), "commerce.digital.download", "order", o.ID, map[string]any{"item": chi.URLParam(r, "item")})
	http.ServeContent(w, r, "download"+filepath.Ext(snap.DigitalFile), st.ModTime(), f)
}

type OrderItem struct {
	Intake       map[string]string `json:"intake,omitempty"`
	IntakeSchema []IntakeField     `json:"intake_schema,omitempty"`
	ID           string            `json:"id"`
	ProductID    string            `json:"product_id"`
	Quantity     int               `json:"quantity"`
	UnitMinor    int64             `json:"unit_minor"`
	ProductName  string            `json:"product_name"`
	Kind         string            `json:"kind"`
	DownloadURL  string            `json:"download_url,omitempty"`
}

func (s *service) orderItems(ctx context.Context, o *Order) ([]OrderItem, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT id,product_id,quantity,unit_minor,snapshot_json FROM order_items WHERE order_id=? ORDER BY id`, o.ID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []OrderItem{}
	for rows.Next() {
		var i OrderItem
		var raw string
		if e = rows.Scan(&i.ID, &i.ProductID, &i.Quantity, &i.UnitMinor, &raw); e != nil {
			return nil, e
		}
		var snap snapshot
		_ = json.Unmarshal([]byte(raw), &snap)
		i.ProductName = snap.Name
		i.Kind = snap.Kind
		i.Intake = snap.Intake
		i.IntakeSchema = snap.IntakeSchema
		if snap.Kind == ProductDigital && (o.Status == OrderPaid || o.Status == OrderActive) && o.PaidAt != "" {
			i.DownloadURL = "/account/download/" + o.ID + "/" + i.ID
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (h *adminHandlers) uploadResult(w http.ResponseWriter, r *http.Request, status int, key string, size int64, errorKey string) {
	if !strings.Contains(r.Header.Get("Accept"), "text/html") {
		if errorKey != "" {
			api.WriteError(w, status, "upload_failed", h.svc.d.I18n.T(i18n.Locale(r.Context()), errorKey), nil)
		} else {
			api.WriteItem(w, map[string]any{"digital_file": key, "bytes": size})
		}
		return
	}
	pg := productPage{Form: productForm{Kind: ProductDigital, Currency: h.svc.currency(r.Context()), AccessMonths: 1, DownloadDays: 7}, UploadError: errorKey}
	if id := chi.URLParam(r, "id"); id != "" {
		p, err := h.svc.productByID(r.Context(), id)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		pg.ID = p.ID
		pg.WPProductID = p.WPProductID
		pg.Form = formOf(p)
	}
	if key != "" {
		pg.Form.DigitalFile = key
		pg.UploadNotice = "commerce.upload.saved"
	}
	h.renderProduct(w, r, status, pg)
}
