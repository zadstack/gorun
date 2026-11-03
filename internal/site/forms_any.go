package site

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/db"
)

// Form sources (forms.source) and field states (FormField.State).
const (
	formBuilder  = "builder"
	formCaptured = "captured"
	formContact  = "contact"

	statePinned     = "pinned"     // declared by the builder: validated
	stateDiscovered = "discovered" // seen in a captured submission, or retired from the builder

	fieldText     = "text"
	fieldTextarea = "textarea"
	fieldEmail    = "email"
	fieldNumber   = "number"
	fieldDate     = "date"
	fieldChoice   = "choice"
	fieldCheckbox = "checkbox"
	fieldFile     = "file" // builder only

	maxFormFields     = 30   // per form, captured or builder
	maxFieldValue     = 4000 // characters per value
	maxCapturedForms  = 50   // per site
	entryReplayWindow = 10 * time.Minute
)

var (
	formSlugRe  = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
	fieldNameRe = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)

	fieldTypes = []string{fieldText, fieldTextarea, fieldEmail, fieldNumber, fieldDate, fieldChoice, fieldCheckbox, fieldFile}
	// Slugs served by their own /forms/* routes.
	reservedFormSlugs = []string{"contact", "comment"}

	errNoForm      = errors.New("site: form not found")
	errFormCap     = errors.New("site: captured form limit reached")
	errFormClosed  = errors.New("site: form is closed")
	errFormChanged = errors.New("site: form changed during submission")
)

// FormField is one column of a form's table, in display order.
type FormField struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Help     string   `json:"help,omitempty"`
	Choices  []string `json:"choices,omitempty"`
	State    string   `json:"state"` // pinned | discovered
	Hidden   bool     `json:"hidden,omitempty"`
}

// FormDefinition is a forms row. PageID is the builder page that declares
// it ("" for captured forms).
type FormDefinition struct {
	ID        string      `json:"id"`
	Slug      string      `json:"slug"`
	Title     string      `json:"title"`
	Source    string      `json:"source"`
	PageID    string      `json:"page_id"`
	Fields    []FormField `json:"fields"`
	Open      bool        `json:"open"`
	CreatedAt string      `json:"created_at"`
	UpdatedAt string      `json:"updated_at"`
}

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const formCols = `id, slug, title, source, page_id, fields_json, open, created_at, updated_at`

func scanForm(row *sql.Row) (FormDefinition, error) {
	var f FormDefinition
	var raw string
	err := row.Scan(&f.ID, &f.Slug, &f.Title, &f.Source, &f.PageID, &raw, &f.Open, &f.CreatedAt, &f.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return f, errNoForm
	}
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal([]byte(raw), &f.Fields); err != nil {
		return f, fmt.Errorf("site: form %s fields: %w", f.Slug, err)
	}
	return f, nil
}

func formBySlugQ(ctx context.Context, q queryRower, slug string) (FormDefinition, error) {
	return scanForm(q.QueryRowContext(ctx, `SELECT `+formCols+` FROM forms WHERE slug = ?`, slug))
}

// formBySlug returns the forms row for slug, or errNoForm.
func (h *handlers) formBySlug(ctx context.Context, slug string) (FormDefinition, error) {
	return formBySlugQ(ctx, h.d.DB, slug)
}

type formOrderKey struct{}

// formGuard runs on POST /forms/{slug} before the CSRF middleware, like
// contactGuard: the shared per-IP limiter first, then a 64 KiB body cap.
// The body is read once to record the order its fields arrive in (the
// column order of a captured form), then replayed for form parsing; a
// multipart body is parsed here and its _csrf passed on as X-CSRF-Token.
func (h *handlers) formGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.limiter.allow(audit.IPFromContext(r.Context())) {
			h.formResult(w, r, http.StatusTooManyRequests, FormState{Errors: map[string]string{"form": "site.form.rate_limited"}})
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxFormBody))
		if err != nil {
			if _, tooBig := errors.AsType[*http.MaxBytesError](err); tooBig {
				h.formResult(w, r, http.StatusRequestEntityTooLarge, FormState{Errors: map[string]string{"form": "site.form.too_long"},
					Back: safeBack("", r.Referer())})
				return
			}
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		ct, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		order := fieldOrder(ct, params, raw)
		if ct == "multipart/form-data" {
			if err := r.ParseMultipartForm(multipartMemory); err != nil {
				http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
				return
			}
			defer r.MultipartForm.RemoveAll()
			if tok := r.PostFormValue("_csrf"); tok != "" && r.Header.Get(csrfHeader) == "" {
				r.Header.Set(csrfHeader, tok)
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), formOrderKey{}, order)))
	})
}

// fieldOrder lists the distinct field names of a form body in the order
// they first appear (urlencoded or multipart); nil for anything else.
func fieldOrder(ct string, params map[string]string, raw []byte) []string {
	var names []string
	add := func(n string) {
		if n != "" && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	switch ct {
	case "application/x-www-form-urlencoded":
		for pair := range strings.SplitSeq(string(raw), "&") {
			k, _, _ := strings.Cut(pair, "=")
			if name, err := url.QueryUnescape(k); err == nil {
				add(name)
			}
		}
	case "multipart/form-data":
		mr := multipart.NewReader(bytes.NewReader(raw), params["boundary"])
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			add(p.FormName())
			_ = p.Close()
		}
	}
	return names
}

// formSubmit handles POST /forms/{slug} (after formGuard and the CSRF
// check): honeypot, then builder validation for a declared form or the
// capture rules otherwise, the entry (replay-safe), the site.form.submitted
// event and an audit entry without the values.
func (h *handlers) formSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	back := safeBack(r.PostFormValue("_back"), r.Referer())
	if r.PostFormValue(honeypotField) != "" {
		h.formResult(w, r, http.StatusOK, FormState{OK: true, Back: back})
		return
	}
	slug := chi.URLParam(r, "slug")
	if !formSlugRe.MatchString(slug) || slices.Contains(reservedFormSlugs, slug) {
		http.NotFound(w, r)
		return
	}
	def, err := h.formBySlug(ctx, slug)
	known := err == nil
	if err != nil && !errors.Is(err, errNoForm) {
		h.d.Log.Error("site: load form", "form", slug, "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	if known && def.Source == formContact {
		http.NotFound(w, r)
		return
	}
	closed := func() {
		h.formResult(w, r, http.StatusGone, FormState{Errors: map[string]string{"form": "site.form.closed"}, Back: back})
	}
	if known && !def.Open {
		closed()
		return
	}
	order, _ := ctx.Value(formOrderKey{}).([]string)
	order = completeOrder(order, r.PostForm)
	values := postedValues(r.PostForm, order)

	builder := known && def.Source == formBuilder
	var data map[string]string
	var names []string
	if builder {
		var errs map[string]string
		if data, errs = validateBuilder(def, values); len(errs) > 0 {
			// "form" also tells form-result.html not to re-render the contact form.
			errs["form"] = "site.form.check_fields"
			h.formResult(w, r, http.StatusUnprocessableEntity, FormState{Errors: errs, Values: data, Back: back})
			return
		}
	} else {
		hasFiles := r.MultipartForm != nil && len(r.MultipartForm.File) > 0
		var ok bool
		if names, data, ok = captureValues(order, values, hasFiles); !ok {
			h.formResult(w, r, http.StatusUnprocessableEntity, FormState{Errors: map[string]string{"form": "site.form.invalid"}, Back: back})
			return
		}
		if known {
			// Fields an admin pinned on a captured form are validated; the
			// entry still keeps every posted field.
			if _, errs := validateBuilder(def, values); len(errs) > 0 {
				errs["form"] = "site.form.check_fields"
				h.formResult(w, r, http.StatusUnprocessableEntity, FormState{Errors: errs, Values: data, Back: back})
				return
			}
		}
	}

	page := formSourcePath(back)
	token := r.PostFormValue("_csrf")
	if token == "" {
		token = r.Header.Get(csrfHeader)
	}
	encoded, _ := json.Marshal([]any{token, slug, data, page}) // map keys marshal sorted
	sum := sha256.Sum256(encoded)
	sub := FormSubmission{ID: db.NewID(), Form: slug, Fields: data, Page: page, IP: audit.IPFromContext(ctx), SubmittedAt: db.Now()}

	duplicate, err := h.storeFormEntry(ctx, slug, builder, names, sub, hex.EncodeToString(sum[:]))
	switch {
	case errors.Is(err, errFormCap), errors.Is(err, errFormChanged):
		h.formResult(w, r, http.StatusUnprocessableEntity, FormState{Errors: map[string]string{"form": "site.form.invalid"}, Back: back})
		return
	case errors.Is(err, errFormClosed):
		closed()
		return
	case err != nil:
		h.d.Log.Error("site: store form entry", "form", slug, "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	if !duplicate {
		// After the commit: Emit may enqueue through the pool.
		h.d.Hooks.Emit(ctx, HookFormSubmitted, sub)
		h.d.Audit.Record(ctx, "site.form.submit", "form", slug, map[string]any{"page": page})
	}
	h.formResult(w, r, http.StatusOK, FormState{OK: true, Back: back})
}

// completeOrder appends any posted name the body scan missed, sorted.
func completeOrder(order []string, posted url.Values) []string {
	var rest []string
	for k := range posted {
		if !slices.Contains(order, k) {
			rest = append(rest, k)
		}
	}
	slices.Sort(rest)
	return append(order, rest...)
}

// postedValues maps each posted name to its trimmed value; repeated names
// (several checked boxes) are joined with ", ".
func postedValues(posted url.Values, order []string) map[string]string {
	out := make(map[string]string, len(order))
	for _, k := range order {
		vs, ok := posted[k]
		if !ok {
			continue
		}
		parts := make([]string, 0, len(vs))
		for _, v := range vs {
			if v = strings.TrimSpace(v); v != "" {
				parts = append(parts, v)
			}
		}
		out[k] = strings.Join(parts, ", ")
	}
	return out
}

// ignoredField reports names never stored: _-prefixed (_csrf, _back, ...)
// and the honeypot.
func ignoredField(name string) bool { return strings.HasPrefix(name, "_") || name == honeypotField }

// captureValues applies the capture rules; any breach rejects the whole
// submission (ok false): a file, a bad name, more than 30 fields, a value
// over 4,000 characters, or nothing to store.
func captureValues(order []string, values map[string]string, hasFiles bool) (names []string, data map[string]string, ok bool) {
	if hasFiles {
		return nil, nil, false
	}
	data = map[string]string{}
	for _, name := range order {
		v, posted := values[name]
		if !posted || ignoredField(name) {
			continue
		}
		if !fieldNameRe.MatchString(name) || utf8.RuneCountInString(v) > maxFieldValue {
			return nil, nil, false
		}
		names = append(names, name)
		data[name] = v
	}
	if len(names) == 0 || len(names) > maxFormFields {
		return nil, nil, false
	}
	return names, data, true
}

// validateBuilder checks the posted values against def's pinned fields and
// returns only those fields' non-empty values; unknown and discovered
// fields are dropped. errs maps a field name to an i18n key. File fields
// are not handled yet (no value is stored for them).
func validateBuilder(def FormDefinition, values map[string]string) (data, errs map[string]string) {
	data, errs = map[string]string{}, map[string]string{}
	for _, f := range def.Fields {
		if f.State != statePinned || f.Type == fieldFile {
			continue
		}
		v := values[f.Name]
		if v == "" {
			if f.Required {
				errs[f.Name] = "core.validation.required"
			}
			continue
		}
		if key := checkFieldValue(f, v); key != "" {
			errs[f.Name] = key
			continue
		}
		data[f.Name] = v
	}
	return data, errs
}

// checkFieldValue returns the i18n key of what is wrong with a non-empty v,
// or "".
func checkFieldValue(f FormField, v string) string {
	if utf8.RuneCountInString(v) > maxFieldValue {
		return "site.form.too_long"
	}
	switch f.Type {
	case fieldEmail:
		if !validEmail(v) {
			return "core.validation.email"
		}
	case fieldNumber:
		if n, err := strconv.ParseFloat(v, 64); err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
			return "site.form.invalid_number"
		}
	case fieldDate:
		if _, err := time.Parse(time.DateOnly, v); err != nil {
			return "site.form.invalid_date"
		}
	case fieldChoice:
		if len(f.Choices) > 0 && !slices.Contains(f.Choices, v) {
			return "site.form.invalid_choice"
		}
	}
	return ""
}

// storeFormEntry stores sub as an entry of form slug with its replay key,
// in one transaction. In capture mode (builder false) it creates the form
// on its first submission (within the 50-form cap) and appends new names
// as discovered fields. duplicate reports a replay inside the window.
func (h *handlers) storeFormEntry(ctx context.Context, slug string, builder bool, names []string, sub FormSubmission, key string) (duplicate bool, err error) {
	data, err := json.Marshal(sub.Fields)
	if err != nil {
		return false, err
	}
	err = db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		cutoff := db.FormatTime(time.Now().Add(-entryReplayWindow))
		if _, err := tx.ExecContext(ctx, `DELETE FROM form_entry_keys WHERE created_at < ?`, cutoff); err != nil {
			return err
		}
		var existing string
		switch err := tx.QueryRowContext(ctx, `SELECT entry_id FROM form_entry_keys WHERE request_key = ?`, key).Scan(&existing); {
		case err == nil:
			duplicate = true
			return nil
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		def, err := formBySlugQ(ctx, tx, slug)
		switch {
		case errors.Is(err, errNoForm) && !builder:
			if def, err = createCapturedForm(ctx, tx, slug, names, sub.SubmittedAt); err != nil {
				return err
			}
		case err != nil:
			return err
		case !def.Open:
			return errFormClosed
		case (def.Source == formBuilder) != builder || def.Source == formContact:
			return errFormChanged
		case !builder:
			if err := appendDiscovered(ctx, tx, def, names, sub.SubmittedAt); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO form_entries (id, form_id, data_json, page, ip, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			sub.ID, def.ID, string(data), sub.Page, sub.IP, sub.SubmittedAt); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO form_entry_keys (request_key, entry_id, created_at) VALUES (?, ?, ?)`, key, sub.ID, sub.SubmittedAt)
		return err
	})
	return duplicate, err
}

func discoveredField(name string) FormField {
	return FormField{Name: name, Label: name, Type: fieldText, State: stateDiscovered}
}

func createCapturedForm(ctx context.Context, tx *sql.Tx, slug string, names []string, now string) (FormDefinition, error) {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM forms WHERE source = 'captured'`).Scan(&n); err != nil {
		return FormDefinition{}, err
	}
	if n >= maxCapturedForms {
		return FormDefinition{}, errFormCap
	}
	def := FormDefinition{ID: db.NewID(), Slug: slug, Title: slug, Source: formCaptured, Open: true, CreatedAt: now, UpdatedAt: now}
	for _, name := range names {
		def.Fields = append(def.Fields, discoveredField(name))
	}
	raw, err := json.Marshal(def.Fields)
	if err != nil {
		return def, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO forms (id, slug, title, source, fields_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		def.ID, def.Slug, def.Title, def.Source, string(raw), now, now)
	return def, err
}

// appendDiscovered adds the names def does not know yet, in order. A
// captured form keeps at most 30 columns; beyond that new names are still
// stored in the entry but not added as columns.
func appendDiscovered(ctx context.Context, tx *sql.Tx, def FormDefinition, names []string, now string) error {
	fields := def.Fields
	for _, name := range names {
		if len(fields) >= maxFormFields {
			break
		}
		if !slices.ContainsFunc(fields, func(f FormField) bool { return f.Name == name }) {
			fields = append(fields, discoveredField(name))
		}
	}
	if len(fields) == len(def.Fields) {
		return nil
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE forms SET fields_json = ?, updated_at = ? WHERE id = ?`, string(raw), now, def.ID)
	return err
}

// validateDefinition checks a builder form before it is stored.
func validateDefinition(d FormDefinition) error {
	if !formSlugRe.MatchString(d.Slug) || slices.Contains(reservedFormSlugs, d.Slug) {
		return fmt.Errorf("site: invalid form slug %q", d.Slug)
	}
	if len(d.Fields) > maxFormFields {
		return fmt.Errorf("site: form %s has more than %d fields", d.Slug, maxFormFields)
	}
	seen := map[string]bool{}
	for _, f := range d.Fields {
		if !fieldNameRe.MatchString(f.Name) || ignoredField(f.Name) {
			return fmt.Errorf("site: form %s: invalid field name %q", d.Slug, f.Name)
		}
		if seen[f.Name] {
			return fmt.Errorf("site: form %s: duplicate field %q", d.Slug, f.Name)
		}
		seen[f.Name] = true
		if !slices.Contains(fieldTypes, f.Type) {
			return fmt.Errorf("site: form %s: field %s has unknown type %q", d.Slug, f.Name, f.Type)
		}
	}
	return nil
}

// upsertBuilderForms records the forms page pageID declares on publish:
// each def becomes (or stays) an open builder form with its fields pinned,
// adopting a captured form of the same slug with its entries. Fields the
// stored form knew that defs no longer declare are kept as discovered, so
// old entries keep their column. Builder forms of pageID missing from defs
// are closed (open = 0); their entries stay.
func (h *handlers) upsertBuilderForms(ctx context.Context, pageID string, defs []FormDefinition) error {
	if pageID == "" {
		return errors.New("site: upsert builder forms: empty page id")
	}
	slugs := make([]any, 0, len(defs))
	for _, d := range defs {
		if err := validateDefinition(d); err != nil {
			return err
		}
		if slices.Contains(slugs, any(d.Slug)) {
			return fmt.Errorf("site: form slug %q declared twice", d.Slug)
		}
		slugs = append(slugs, d.Slug)
	}
	now := db.Now()
	return db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		for _, d := range defs {
			if err := upsertBuilderForm(ctx, tx, pageID, d, now); err != nil {
				return err
			}
		}
		q := `UPDATE forms SET open = 0, updated_at = ? WHERE source = 'builder' AND page_id = ? AND open = 1`
		args := []any{now, pageID}
		if len(slugs) > 0 {
			q += ` AND slug NOT IN (` + strings.TrimSuffix(strings.Repeat("?, ", len(slugs)), ", ") + `)`
			args = append(args, slugs...)
		}
		_, err := tx.ExecContext(ctx, q, args...)
		return err
	})
}

func upsertBuilderForm(ctx context.Context, tx *sql.Tx, pageID string, d FormDefinition, now string) error {
	fields := make([]FormField, 0, len(d.Fields))
	for _, f := range d.Fields {
		f.State = statePinned
		if f.Label == "" {
			f.Label = f.Name
		}
		fields = append(fields, f)
	}
	title := strings.TrimSpace(d.Title)
	if title == "" {
		title = d.Slug
	}
	cur, err := formBySlugQ(ctx, tx, d.Slug)
	if err != nil && !errors.Is(err, errNoForm) {
		return err
	}
	exists := err == nil
	if exists {
		if cur.Source == formContact {
			return fmt.Errorf("site: form slug %q is reserved", d.Slug)
		}
		// The builder checks slug uniqueness across its published pages
		// (ruling R5); this refusal is the defense in depth behind it.
		if cur.Source == formBuilder && cur.PageID != "" && cur.PageID != pageID {
			return fmt.Errorf("site: form slug %q belongs to another page", d.Slug)
		}
		var extras []FormField
		for _, old := range cur.Fields {
			if !slices.ContainsFunc(fields, func(f FormField) bool { return f.Name == old.Name }) {
				old.State = stateDiscovered
				extras = append(extras, old)
			}
		}
		// Keep at most maxFormFields columns: the oldest discovered extras go first.
		if room := maxFormFields - len(fields); len(extras) > room {
			extras = extras[len(extras)-room:]
		}
		fields = append(fields, extras...)
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	if exists {
		_, err = tx.ExecContext(ctx, `UPDATE forms SET title = ?, source = 'builder', page_id = ?, fields_json = ?, open = 1, updated_at = ? WHERE id = ?`,
			title, pageID, string(raw), now, cur.ID)
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO forms (id, slug, title, source, page_id, fields_json, open, created_at, updated_at)
		VALUES (?, ?, ?, 'builder', ?, ?, 1, ?, ?)`, db.NewID(), d.Slug, title, pageID, string(raw), now, now)
	return err
}

// builderFormsPublished is the builder's HookFormsPublished. Site does not
// import the builder (docs/BOUNDARIES.md), so the name and payload shape are
// mirrored here.
const builderFormsPublished = "builder.forms.published"

// builderFormsPayload mirrors the builder's FormsPublished JSON.
type builderFormsPayload struct {
	PageID string `json:"page_id"`
	Forms  []struct {
		Slug        string `json:"slug"`
		Title       string `json:"title"`
		SubmitLabel string `json:"submit_label"`
		Success     string `json:"success"`
		Fields      []struct {
			Name     string   `json:"name"`
			Label    string   `json:"label"`
			Type     string   `json:"type"`
			Required bool     `json:"required"`
			Help     string   `json:"help"`
			Choices  []string `json:"choices"`
		} `json:"fields"`
	} `json:"forms"`
}

// decodeBuilderForms reads a builder.forms.published payload: JSON when
// delivered through the queue (Emit), or any JSON-encodable value when a
// caller passes it to Do directly.
func decodeBuilderForms(payload any) (string, []FormDefinition, error) {
	var raw []byte
	switch v := payload.(type) {
	case json.RawMessage:
		raw = v
	case []byte:
		raw = v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return "", nil, fmt.Errorf("site: builder forms payload %T: %w", payload, err)
		}
		raw = b
	}
	var p builderFormsPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", nil, fmt.Errorf("site: builder forms payload: %w", err)
	}
	defs := make([]FormDefinition, 0, len(p.Forms))
	for _, f := range p.Forms {
		def := FormDefinition{Slug: f.Slug, Title: f.Title, Source: formBuilder, PageID: p.PageID, Fields: make([]FormField, 0, len(f.Fields))}
		for _, fl := range f.Fields {
			def.Fields = append(def.Fields, FormField{Name: fl.Name, Label: fl.Label, Type: fl.Type, Required: fl.Required, Help: fl.Help, Choices: fl.Choices})
		}
		defs = append(defs, def)
	}
	return p.PageID, defs, nil
}

// onBuilderForms stores the forms of a page the builder just published. A
// malformed payload is logged and dropped; a store error is returned so the
// queued event is retried (the upsert is idempotent).
func (h *handlers) onBuilderForms(ctx context.Context, payload any) error {
	pageID, defs, err := decodeBuilderForms(payload)
	if err != nil {
		h.d.Log.Error("site: builder forms: bad payload", "err", err)
		return nil
	}
	if err := h.upsertBuilderForms(ctx, pageID, defs); err != nil {
		h.d.Log.Error("site: builder forms: upsert refused", "page", pageID, "err", err)
		return err
	}
	return nil
}
