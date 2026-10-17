package site

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

const (
	// HookFormSubmitted is emitted (async) for every accepted form
	// submission; the site module itself subscribes (deliverForm) and mails
	// it to contact.email through the mailer service.
	HookFormSubmitted = "site.form.submitted"

	contactRate   = 5
	contactWindow = time.Minute
	honeypotField = "website"
	maxFormBody   = 64 << 10
	maxNameLen    = 200
	maxEmailLen   = 254
	maxMessageLen = 5000
	csrfHeader    = "X-CSRF-Token" // what auth.CSRFMiddleware reads for multipart bodies

	multipartOverhead = 64 << 10 // boundaries and part headers on top of the file
	multipartMemory   = 1 << 20  // larger parts spill to temp files (removed after the request)
)

// FormSubmission is the HookFormSubmitted payload. It carries attachment
// metadata only; delivery reads the file from <data>/form-uploads/<id>.
type FormSubmission struct {
	ID             string `json:"id"` // form_submissions row
	Form           string `json:"form"`
	Name           string `json:"name"`
	Email          string `json:"email"`
	Message        string `json:"message"`
	Page           string `json:"page"`
	IP             string `json:"ip"`
	SubmittedAt    string `json:"submitted_at"`
	AttachmentName string `json:"attachment_name,omitempty"`
	AttachmentType string `json:"attachment_type,omitempty"`
	AttachmentSize int64  `json:"attachment_size,omitempty"`
}

// FormState is passed to form-result.html as .Extra.form.
type FormState struct {
	OK     bool
	Errors map[string]string // field → i18n key
	Values map[string]string
	Back   string
}

type formConfigKey struct{}

// contactGuard runs on POST /forms/contact before the CSRF middleware: it
// applies the per-IP rate limit (5/min) before reading anything, caps the
// body (64 KiB, plus the attachment limit when attachments are on) and, for
// a multipart body, parses it once and passes its _csrf field on as the
// X-CSRF-Token header, which is how auth.CSRFMiddleware accepts multipart
// requests. The form configuration it read rides on to contact.
func (h *handlers) contactGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.limiter.allow(audit.IPFromContext(r.Context())) {
			h.formResult(w, r, http.StatusTooManyRequests, FormState{Errors: map[string]string{"form": "site.form.rate_limited"}})
			return
		}
		cfg := h.formConfig(r.Context())
		limit := int64(maxFormBody)
		if cfg.Attachments {
			limit += cfg.maxBytes() + multipartOverhead
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct == "multipart/form-data" {
			if err := r.ParseMultipartForm(multipartMemory); err != nil {
				if _, tooBig := errors.AsType[*http.MaxBytesError](err); tooBig {
					// A form-level error: the body was not read, so there is no form to show again.
					h.formResult(w, r, http.StatusRequestEntityTooLarge, FormState{Errors: map[string]string{"form": errFileTooLarge.Error()},
						Back: safeBack("", r.Referer())})
					return
				}
				http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
				return
			}
			defer r.MultipartForm.RemoveAll()
			if tok := r.PostFormValue("_csrf"); tok != "" && r.Header.Get(csrfHeader) == "" {
				r.Header.Set(csrfHeader, tok)
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), formConfigKey{}, cfg)))
	})
}

// contact handles POST /forms/contact (after contactGuard and the CSRF
// check): honeypot, validation, the optional attachment, then the row, the
// site.form.submitted event and an audit entry.
func (h *handlers) contact(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	vals := map[string]string{}
	for _, k := range []string{"name", "email", "message"} {
		vals[k] = strings.TrimSpace(r.PostFormValue(k))
	}
	back := safeBack(r.PostFormValue("_back"), r.Referer())
	if r.PostFormValue(honeypotField) != "" {
		h.formResult(w, r, http.StatusOK, FormState{OK: true, Back: back})
		return
	}
	if errs := validateContact(vals); len(errs) > 0 {
		h.formResult(w, r, http.StatusUnprocessableEntity, FormState{Errors: errs, Values: vals, Back: back})
		return
	}
	sub := FormSubmission{ID: db.NewID(), Form: "contact", Name: vals["name"], Email: vals["email"],
		Message: vals["message"], Page: formSourcePath(back), IP: audit.IPFromContext(ctx), SubmittedAt: db.Now()}
	cfg, ok := ctx.Value(formConfigKey{}).(formConfig)
	if !ok {
		cfg = h.formConfig(ctx)
	}
	fieldErr, err := h.attach(r, cfg, &sub)
	if fieldErr != nil {
		h.formResult(w, r, http.StatusUnprocessableEntity, FormState{Errors: map[string]string{attachmentField: fieldErr.Error()}, Values: vals, Back: back})
		return
	}
	// A replay with the same CSRF session and payload must not create another request.
	// Include attachment bytes so different attachments remain distinct requests.
	var attachmentHash string
	if sub.AttachmentSize > 0 {
		data, readErr := os.ReadFile(h.uploadPath(sub.ID))
		if readErr != nil {
			err = readErr
		} else {
			sum := sha256.Sum256(data)
			attachmentHash = hex.EncodeToString(sum[:])
		}
	}
	token := r.PostFormValue("_csrf")
	if token == "" {
		token = r.Header.Get(csrfHeader)
	}
	encoded, _ := json.Marshal([]string{token, sub.Name, sub.Email, sub.Message, sub.Page, attachmentHash})
	sum := sha256.Sum256(encoded)
	requestKey := hex.EncodeToString(sum[:])
	duplicate := false
	// Stored first, in every env: the message survives a missing or failing mailer.
	if err == nil {
		duplicate, err = h.storeContactRequest(ctx, sub, requestKey)
		if (err != nil || duplicate) && sub.AttachmentSize > 0 {
			_ = h.removeUpload(sub.ID)
		}
	}
	if err != nil {
		h.d.Log.Error("site: store form submission", "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	if duplicate {
		h.formResult(w, r, http.StatusOK, FormState{OK: true, Back: back})
		return
	}
	h.d.Hooks.Emit(ctx, HookFormSubmitted, sub)
	h.d.Audit.Record(ctx, "site.form.submit", "form", "contact", map[string]any{"page": back})
	h.formResult(w, r, http.StatusOK, FormState{OK: true, Back: back})
}

// attach validates and stores the request's file (when attachments are on)
// and records it on sub. fieldErr is a visitor-facing rejection
// (errFileTooLarge, errFileType); err is an internal failure.
func (h *handlers) attach(r *http.Request, cfg formConfig, sub *FormSubmission) (fieldErr, err error) {
	if !cfg.Attachments {
		return nil, nil
	}
	up, err := readUpload(r.MultipartForm, cfg.maxBytes())
	if errors.Is(err, errFileTooLarge) || errors.Is(err, errFileType) {
		return err, nil
	}
	if err != nil || up == nil {
		return nil, err
	}
	if err := h.saveUpload(sub.ID, up); err != nil {
		return nil, err
	}
	sub.AttachmentName, sub.AttachmentType, sub.AttachmentSize = up.Name, up.Type, up.Size
	return nil, nil
}

// validEmail reports a plain address (no display name) of at most 254 bytes.
func validEmail(v string) bool {
	a, err := mail.ParseAddress(v)
	return err == nil && a.Address == v && len(v) <= maxEmailLen
}

func validateContact(v map[string]string) map[string]string {
	errs := map[string]string{}
	if v["name"] == "" {
		errs["name"] = "core.validation.required"
	} else if utf8.RuneCountInString(v["name"]) > maxNameLen {
		errs["name"] = "site.form.too_long"
	}
	if v["email"] == "" {
		errs["email"] = "core.validation.required"
	} else if !validEmail(v["email"]) {
		errs["email"] = "core.validation.email"
	}
	if v["message"] == "" {
		errs["message"] = "core.validation.required"
	} else if utf8.RuneCountInString(v["message"]) > maxMessageLen {
		errs["message"] = "site.form.too_long"
	}
	return errs
}

// safeBack returns a same-origin path to return to: the _back field, else
// the Referer's path, else "/". Control characters and backslashes are
// refused outright (browsers strip tabs/newlines, turning "/\t/x" into
// "//x"), and the parsed value must have no scheme or host.
func safeBack(field, referer string) string {
	if localPath(field) {
		return field
	}
	if u, err := url.Parse(referer); err == nil && u.Path != "" {
		p := u.EscapedPath()
		if u.RawQuery != "" {
			p += "?" + u.RawQuery
		}
		if localPath(p) {
			return p
		}
	}
	return "/"
}

// localPath reports a path-absolute, same-origin URL reference.
func localPath(p string) bool {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return false
	}
	for i := 0; i < len(p); i++ {
		if c := p[i]; c < 0x20 || c == 0x7f || c == '\\' {
			return false
		}
	}
	u, err := url.Parse(p)
	return err == nil && u.Scheme == "" && u.Host == "" && u.User == nil
}

// formResult renders the theme's form-result.html, or — when the theme has
// none — redirects back with a one-shot gp_form flash cookie.
func (h *handlers) formResult(w http.ResponseWriter, r *http.Request, status int, st FormState) {
	if st.Back == "" {
		st.Back = "/"
	}
	if svc, ok := h.theme(); ok {
		if name, err := svc.Resolve(kernel.KindRequest{Kind: KindFormResult}); err == nil && name == "form-result.html" {
			tc := h.newContext(w, r)
			tc.Status = status
			tc.Extra["form"] = st
			h.render(w, r, kernel.KindRequest{Kind: KindFormResult}, tc)
			return
		}
	}
	flash := "ok"
	if !st.OK {
		flash = "error"
	}
	if status == http.StatusTooManyRequests {
		http.Error(w, http.StatusText(status), status)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: formFlashCookie, Value: flash, Path: "/", MaxAge: 120, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, st.Back, http.StatusSeeOther)
}

// limiter is a fixed-window counter per key (client IP).
type limiter struct {
	mu     sync.Mutex
	rate   int
	window time.Duration
	hits   map[string]*window
	now    func() time.Time
}

type window struct {
	start time.Time
	count int
}

func newLimiter(rate int, per time.Duration) *limiter {
	return &limiter{rate: rate, window: per, hits: map[string]*window{}, now: time.Now}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.hits) > 10000 {
		for k, w := range l.hits {
			if now.Sub(w.start) >= l.window {
				delete(l.hits, k)
			}
		}
	}
	w, ok := l.hits[key]
	if !ok || now.Sub(w.start) >= l.window {
		l.hits[key] = &window{start: now, count: 1}
		return true
	}
	if w.count >= l.rate {
		return false
	}
	w.count++
	return true
}
