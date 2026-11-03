package site

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

const (
	formsPath          = "/forms"
	formEntriesPerPage = 50
	maxEntryColumns    = 6 // visible field columns in the table
	maxLabelLen        = 120
	maxChoices         = 20
)

var entryStatuses = []string{statusNew, statusRead, statusSpam}

type formsAdmin struct {
	h   *handlers
	tpl *httpx.Templates
}

// formRow is one line of the forms list.
type formRow struct {
	Slug, Title, Source string
	Open                bool
	Entries, New        int
	LastEntry           string // created_at of the newest entry, "" when none
	Href                string
}

type formsListPage struct {
	Rows     []formRow
	NewTotal int // new entries across every form, contact included
}

type entryRow struct {
	ID, Status, CreatedAt string
	Cells                 []string
}

type entryFilter struct {
	Status, From, To, Q string
}

type formEntriesPage struct {
	Form FormDefinition
	// Fields is Form.Fields in admin order (the fields panel).
	Fields  []FormField
	Columns []FormField
	Rows    []entryRow
	Filter  entryFilter
	Total   int
	Page    int
	PerPage int
	Types   []string
	// Error is an i18n key shown above the fields panel ("" for none).
	Error string
}

// Link returns the table's URL for page n with the current filters.
func (p formEntriesPage) Link(n int) string {
	v := p.query()
	v.Set("page", strconv.Itoa(n))
	return "?" + v.Encode()
}

// query is the current filters as URL values (no page).
func (p formEntriesPage) query() url.Values {
	v := url.Values{}
	if p.Filter.Status != "" {
		v.Set("status", p.Filter.Status)
	}
	if p.Filter.From != "" {
		v.Set("from", p.Filter.From)
	}
	if p.Filter.To != "" {
		v.Set("to", p.Filter.To)
	}
	if p.Filter.Q != "" {
		v.Set("q", p.Filter.Q)
	}
	return v
}

// CSVLink is the export URL with the current filters.
func (p formEntriesPage) CSVLink() string {
	base := "/admin" + formsPath + "/" + p.Form.Slug + "/export.csv"
	if q := p.query().Encode(); q != "" {
		return base + "?" + q
	}
	return base
}

type entryField struct {
	Name, Label, Value string
	Known              bool
}

type formEntryPage struct {
	Form      FormDefinition
	ID        string
	Status    string
	CreatedAt string
	Page      string
	IP        string
	Fields    []entryField
	// The uploaded file of a builder form's file field, if any.
	AttachmentName string
	AttachmentSize int64
}

// SizeText is the attachment size for display.
func (p formEntryPage) SizeText() string { return humanSize(p.AttachmentSize) }

type formDeletePage struct {
	Form    FormDefinition
	Entries int
	Blocked bool
}

// registerFormsAdmin adds Inbox → Forms: one table per form (fields are the
// columns, entries the rows), the entry page that marks it read, status and
// delete, field presentation (hide, rename, reorder, pin), CSV export and
// deleting a form. kernel.NavItem has no badge, so the total of new entries
// is shown on the list page. Without the admin renderer it does nothing.
func registerFormsAdmin(d *kernel.Deps, h *handlers) {
	tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	if !ok {
		return
	}
	a := &formsAdmin{h: h, tpl: tpl}
	d.Admin.AddNav(kernel.NavItem{ID: "site.forms", LabelKey: "site.forms.nav", Icon: "inbox",
		Href: "/admin" + formsPath, Permission: PermSubmissions, Group: "inbox", Position: 190})
	r := d.Router.Admin.With(auth.RequirePerm(PermSubmissions))
	r.Get(formsPath, a.list)
	r.Get(formsPath+"/{slug}", a.entries)
	r.Get(formsPath+"/{slug}/export.csv", a.export)
	r.Get(formsPath+"/{slug}/delete", a.confirmDelete)
	r.Post(formsPath+"/{slug}/delete", a.deleteForm)
	r.Post(formsPath+"/{slug}/fields/{name}/{op}", a.field)
	r.Get(formsPath+"/{slug}/entries/{id}", a.entry)
	r.Post(formsPath+"/{slug}/entries/{id}/status", a.entryStatus)
	r.Post(formsPath+"/{slug}/entries/{id}/delete", a.entryDelete)
	r.Get(formsPath+"/{slug}/entries/{id}/attachment", a.entryAttachment)
}

func (a *formsAdmin) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errNoForm) || errors.Is(err, sql.ErrNoRows) {
		a.tpl.RenderError(w, r, http.StatusNotFound)
		return
	}
	a.h.d.Log.Error("site: forms admin", "err", err)
	a.tpl.RenderError(w, r, http.StatusInternalServerError)
}

func (a *formsAdmin) back(w http.ResponseWriter, r *http.Request, to, kind, msg string) {
	httpx.SetFlash(w, kind, msg)
	http.Redirect(w, r, "/admin"+formsPath+to, http.StatusSeeOther)
}

// form loads the form named by the URL; the contact form lives elsewhere.
func (a *formsAdmin) form(r *http.Request) (FormDefinition, error) {
	slug := chi.URLParam(r, "slug")
	if !formSlugRe.MatchString(slug) {
		return FormDefinition{}, errNoForm
	}
	def, err := a.h.formBySlug(r.Context(), slug)
	if err == nil && def.Source == formContact {
		return def, errNoForm
	}
	return def, err
}

func (a *formsAdmin) list(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := a.h.d.DB.QueryContext(ctx, `SELECT f.slug, f.title, f.source, f.open, count(e.id), coalesce(sum(e.status = 'new'), 0), coalesce(max(e.created_at), '')
		FROM forms f LEFT JOIN form_entries e ON e.form_id = f.id
		WHERE f.source <> 'contact' GROUP BY f.id ORDER BY f.created_at, f.slug`)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	defer rows.Close()
	data := formsListPage{Rows: []formRow{}}
	for rows.Next() {
		var f formRow
		if err := rows.Scan(&f.Slug, &f.Title, &f.Source, &f.Open, &f.Entries, &f.New, &f.LastEntry); err != nil {
			a.fail(w, r, err)
			return
		}
		f.Href = "/admin" + formsPath + "/" + f.Slug
		data.NewTotal += f.New
		data.Rows = append(data.Rows, f)
	}
	if err := rows.Err(); err != nil {
		a.fail(w, r, err)
		return
	}
	// The contact form keeps form_submissions (ruling R1): a row that links
	// to its own inbox.
	counts, err := a.h.countSubmissions(ctx)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var last string
	if err := a.h.d.DB.QueryRowContext(ctx, `SELECT coalesce(max(created_at), '') FROM form_submissions WHERE status <> 'spam'`).Scan(&last); err != nil {
		a.fail(w, r, err)
		return
	}
	data.Rows = append(data.Rows, formRow{Slug: formContact, Title: formContact, Source: formContact, Open: true,
		Entries: counts["inbox"], New: counts["new"], LastEntry: last, Href: "/admin" + submissionsPath})
	data.NewTotal += counts["new"]
	a.tpl.Render(w, r, "pages/site/forms.html", data)
}

func filterFrom(r *http.Request) entryFilter {
	q := r.URL.Query()
	f := entryFilter{Status: q.Get("status"), From: q.Get("from"), To: q.Get("to"), Q: strings.TrimSpace(q.Get("q"))}
	if !slices.Contains(entryStatuses, f.Status) {
		f.Status = ""
	}
	if _, err := time.Parse(time.DateOnly, f.From); err != nil {
		f.From = ""
	}
	if _, err := time.Parse(time.DateOnly, f.To); err != nil {
		f.To = ""
	}
	if len(f.Q) > 200 {
		f.Q = f.Q[:200]
	}
	return f
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// where builds the entries query condition; every value is a bound argument.
func (f entryFilter) where(formID string) (string, []any) {
	cond, args := []string{"form_id = ?"}, []any{formID}
	if f.Status != "" {
		cond, args = append(cond, "status = ?"), append(args, f.Status)
	}
	if t, err := time.Parse(time.DateOnly, f.From); err == nil {
		cond, args = append(cond, "created_at >= ?"), append(args, db.FormatTime(t))
	}
	if t, err := time.Parse(time.DateOnly, f.To); err == nil {
		cond, args = append(cond, "created_at < ?"), append(args, db.FormatTime(t.AddDate(0, 0, 1)))
	}
	if f.Q != "" {
		cond, args = append(cond, `data_json LIKE ? ESCAPE '\'`), append(args, "%"+likeEscaper.Replace(f.Q)+"%")
	}
	return strings.Join(cond, " AND "), args
}

// visibleColumns returns the first maxEntryColumns fields that are not
// hidden, in admin order.
func visibleColumns(fields []FormField) []FormField {
	var out []FormField
	for _, f := range adminOrder(fields) {
		if !f.Hidden && len(out) < maxEntryColumns {
			out = append(out, f)
		}
	}
	return out
}

func dataMap(raw string) map[string]string {
	var m map[string]any
	_ = json.Unmarshal([]byte(raw), &m)
	out := make(map[string]string, len(m))
	for k, v := range m {
		switch x := v.(type) {
		case string:
			out[k] = x
		case nil:
		default:
			b, _ := json.Marshal(x)
			out[k] = string(b)
		}
	}
	return out
}

func (a *formsAdmin) entries(w http.ResponseWriter, r *http.Request) {
	def, err := a.form(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.renderEntries(w, r, http.StatusOK, def, "")
}

// renderEntries renders def's table with status and an optional error key.
func (a *formsAdmin) renderEntries(w http.ResponseWriter, r *http.Request, status int, def FormDefinition, errKey string) {
	ctx := r.Context()
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	data := formEntriesPage{Form: def, Fields: adminOrder(def.Fields), Columns: visibleColumns(def.Fields), Filter: filterFrom(r), Page: max(page, 1),
		PerPage: formEntriesPerPage, Rows: []entryRow{}, Types: fieldTypes, Error: errKey}
	cond, args := data.Filter.where(def.ID)
	if err := a.h.d.DB.QueryRowContext(ctx, `SELECT count(*) FROM form_entries WHERE `+cond, args...).Scan(&data.Total); err != nil {
		a.fail(w, r, err)
		return
	}
	rows, err := a.h.d.DB.QueryContext(ctx, `SELECT id, status, created_at, data_json FROM form_entries WHERE `+cond+
		` ORDER BY created_at DESC, rowid DESC LIMIT ? OFFSET ?`, append(args, formEntriesPerPage, (data.Page-1)*formEntriesPerPage)...)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var e entryRow
		var raw string
		if err := rows.Scan(&e.ID, &e.Status, &e.CreatedAt, &raw); err != nil {
			a.fail(w, r, err)
			return
		}
		m := dataMap(raw)
		for _, c := range data.Columns {
			e.Cells = append(e.Cells, m[c.Name])
		}
		data.Rows = append(data.Rows, e)
	}
	if err := rows.Err(); err != nil {
		a.fail(w, r, err)
		return
	}
	a.tpl.RenderWith(w, r, status, "admin", "pages/site/form_entries.html", data)
}

// entry renders one entry and marks it read when it was new.
func (a *formsAdmin) entry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	def, err := a.form(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	p := formEntryPage{Form: def, ID: chi.URLParam(r, "id")}
	var raw string
	err = a.h.d.DB.QueryRowContext(ctx, `SELECT status, created_at, page, ip, data_json, attachment_name, attachment_size FROM form_entries WHERE id = ? AND form_id = ?`,
		p.ID, def.ID).Scan(&p.Status, &p.CreatedAt, &p.Page, &p.IP, &raw, &p.AttachmentName, &p.AttachmentSize)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if p.Status == statusNew {
		if _, err := a.h.d.DB.ExecContext(ctx, `UPDATE form_entries SET status = ? WHERE id = ?`, statusRead, p.ID); err != nil {
			a.fail(w, r, err)
			return
		}
		p.Status = statusRead
	}
	m := dataMap(raw)
	for _, f := range adminOrder(def.Fields) {
		if v, ok := m[f.Name]; ok {
			p.Fields = append(p.Fields, entryField{Name: f.Name, Label: f.DisplayLabel(), Value: v, Known: true})
			delete(m, f.Name)
		}
	}
	for _, k := range sortedKeys(m) { // keys no longer in the schema
		p.Fields = append(p.Fields, entryField{Name: k, Label: k, Value: m[k]})
	}
	a.tpl.Render(w, r, "pages/site/form_entry.html", p)
}

// entryAttachment sends an entry's stored file as a download (never
// inline), like the contact inbox's attachment handler.
func (a *formsAdmin) entryAttachment(w http.ResponseWriter, r *http.Request) {
	def, err := a.form(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	id := chi.URLParam(r, "id")
	var name, ctype string
	var size int64
	err = a.h.d.DB.QueryRowContext(r.Context(), `SELECT attachment_name, attachment_type, attachment_size FROM form_entries WHERE id = ? AND form_id = ?`,
		id, def.ID).Scan(&name, &ctype, &size)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	f, err := os.Open(a.h.uploadPath(id))
	if size == 0 || errors.Is(err, os.ErrNotExist) {
		if err == nil {
			f.Close()
		}
		a.tpl.RenderError(w, r, http.StatusNotFound)
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	defer f.Close()
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, no-store")
	h.Set("Content-Security-Policy", "sandbox")
	http.ServeContent(w, r, "", time.Time{}, f)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func (a *formsAdmin) entryStatus(w http.ResponseWriter, r *http.Request) {
	def, err := a.form(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	id, status := chi.URLParam(r, "id"), r.PostFormValue("status")
	if !slices.Contains(entryStatuses, status) {
		a.tpl.RenderError(w, r, http.StatusBadRequest)
		return
	}
	res, err := a.h.d.DB.ExecContext(r.Context(), `UPDATE form_entries SET status = ? WHERE id = ? AND form_id = ?`, status, id, def.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		a.tpl.RenderError(w, r, http.StatusNotFound)
		return
	}
	a.h.d.Audit.Record(r.Context(), "site.form.entry.status", "form_entry", id, map[string]any{"form": def.Slug, "status": status})
	to := "/entries/" + id
	if status == statusSpam {
		to = ""
	}
	a.back(w, r, "/"+def.Slug+to, "success", "site.forms.flash.status")
}

func (a *formsAdmin) entryDelete(w http.ResponseWriter, r *http.Request) {
	def, err := a.form(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	id := chi.URLParam(r, "id")
	res, err := a.h.d.DB.ExecContext(r.Context(), `DELETE FROM form_entries WHERE id = ? AND form_id = ?`, id, def.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		a.tpl.RenderError(w, r, http.StatusNotFound)
		return
	}
	if err := a.h.removeUpload(id); err != nil {
		a.h.d.Log.Error("site: forms admin", "err", err)
	}
	a.h.d.Audit.Record(r.Context(), "site.form.entry.delete", "form_entry", id, map[string]any{"form": def.Slug})
	a.back(w, r, "/"+def.Slug, "success", "site.forms.flash.deleted")
}

// saveFields rewrites a form's field list inside one transaction: change
// receives the current fields and returns the new ones (or an error that
// maps to a 400). Entries are never touched.
func (h *handlers) saveFields(ctx context.Context, slug string, change func([]FormField) ([]FormField, error)) error {
	return db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		def, err := formBySlugQ(ctx, tx, slug)
		if err != nil {
			return err
		}
		fields, err := change(def.Fields)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(fields)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE forms SET fields_json = ?, updated_at = ? WHERE id = ?`, string(raw), db.Now(), def.ID)
		return err
	})
}

var (
	errBadField = errors.New("site: invalid field change")
	// errBuilderPin: a builder form's fields come from its page (the builder
	// owns that schema), so none can be pinned in the admin.
	errBuilderPin = errors.New("site: builder form fields cannot be pinned")
)

// field changes how one field is presented in the admin: hide, show, rename
// (admin_label), up and down (admin_pos); or pins a discovered field of a
// captured form with a type, label and the required flag. Only pin changes
// the schema visitors are checked against, and a builder form refuses it.
func (a *formsAdmin) field(w http.ResponseWriter, r *http.Request) {
	def, err := a.form(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	name, op := chi.URLParam(r, "name"), chi.URLParam(r, "op")
	err = a.h.saveFields(r.Context(), def.Slug, func(fields []FormField) ([]FormField, error) {
		i := slices.IndexFunc(fields, func(f FormField) bool { return f.Name == name })
		if i < 0 {
			return nil, errNoForm
		}
		f := &fields[i]
		switch op {
		case "hide", "show":
			f.Hidden = op == "hide"
		case "rename":
			label := strings.TrimSpace(r.PostFormValue("label"))
			if label == "" || len([]rune(label)) > maxLabelLen {
				return nil, errBadField
			}
			f.AdminLabel = label
			if label == f.Label {
				f.AdminLabel = ""
			}
		case "up", "down":
			moveField(fields, name, op == "up")
		case "pin":
			if def.Source == formBuilder {
				return nil, errBuilderPin
			}
			typ := r.PostFormValue("type")
			if !slices.Contains(fieldTypes, typ) || typ == fieldFile {
				return nil, errBadField
			}
			if label := strings.TrimSpace(r.PostFormValue("label")); label != "" {
				if len([]rune(label)) > maxLabelLen {
					return nil, errBadField
				}
				f.Label = label
			}
			f.Type, f.State, f.Required = typ, statePinned, r.PostFormValue("required") == "1"
			f.Choices = nil
			if typ == fieldChoice {
				for _, c := range strings.FieldsFunc(r.PostFormValue("choices"), func(r rune) bool { return r == '\n' }) {
					if c = strings.TrimSpace(c); c != "" && !slices.Contains(f.Choices, c) {
						f.Choices = append(f.Choices, c)
					}
				}
				if n := len(f.Choices); n < 1 || n > maxChoices {
					return nil, errBadField
				}
			}
		default:
			return nil, errBadField
		}
		return fields, nil
	})
	switch {
	case errors.Is(err, errBuilderPin):
		a.renderEntries(w, r, http.StatusBadRequest, def, "site.forms.fields.pin_builder")
		return
	case errors.Is(err, errBadField):
		a.tpl.RenderError(w, r, http.StatusBadRequest)
		return
	case err != nil:
		a.fail(w, r, err)
		return
	}
	a.h.d.Audit.Record(r.Context(), "site.form.field", "form", def.Slug, map[string]any{"field": name, "op": op})
	a.back(w, r, "/"+def.Slug, "success", "site.forms.flash.field")
}

// moveField moves field name one place up or down in admin order and
// renumbers every field's AdminPos from 1; the schema order is untouched.
func moveField(fields []FormField, name string, up bool) {
	order := adminOrder(fields)
	i := slices.IndexFunc(order, func(f FormField) bool { return f.Name == name })
	j := i + 1
	if up {
		j = i - 1
	}
	if i < 0 || j < 0 || j >= len(order) {
		return
	}
	order[i], order[j] = order[j], order[i]
	for pos, o := range order {
		k := slices.IndexFunc(fields, func(f FormField) bool { return f.Name == o.Name })
		fields[k].AdminPos = pos + 1
	}
}

// pageExists reports whether content item id still exists (trashed counts:
// only a hard delete removes it). Without the content service, or on any
// error other than not found, the page is taken to exist.
func (h *handlers) pageExists(ctx context.Context, id string) bool {
	svc, ok := h.content()
	if !ok || id == "" {
		return ok
	}
	_, err := svc.Get(ctx, id)
	return !errors.Is(err, content.ErrNotFound)
}

// deleteBlocked reports a builder form that is open while its page exists:
// the page still declares it. A form whose page was deleted can go.
func (a *formsAdmin) deleteBlocked(ctx context.Context, def FormDefinition) bool {
	return def.Source == formBuilder && def.Open && a.h.pageExists(ctx, def.PageID)
}

func (a *formsAdmin) deletePage(r *http.Request, def FormDefinition) (formDeletePage, error) {
	p := formDeletePage{Form: def, Blocked: a.deleteBlocked(r.Context(), def)}
	err := a.h.d.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM form_entries WHERE form_id = ?`, def.ID).Scan(&p.Entries)
	return p, err
}

// errFormOpen: the guarded DELETE found the form open on a live page.
var errFormOpen = errors.New("site: form is open on a page")

func (a *formsAdmin) confirmDelete(w http.ResponseWriter, r *http.Request) {
	def, err := a.form(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	p, err := a.deletePage(r, def)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.tpl.Render(w, r, "pages/site/form_delete.html", p)
}

// deleteForm removes a form and its entries. A builder form that is open
// while its page exists is refused with 409; the DELETE itself carries that
// condition, so a republish between the check and the commit is refused too.
func (a *formsAdmin) deleteForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	def, err := a.form(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	p, err := a.deletePage(r, def)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if p.Blocked {
		a.tpl.RenderWith(w, r, http.StatusConflict, "admin", "pages/site/form_delete.html", p)
		return
	}
	// Read before the write transaction; the DELETE re-checks open itself.
	live := def.Source == formBuilder && a.h.pageExists(ctx, def.PageID)
	var files []string
	err = db.InTx(ctx, a.h.d.DB, func(tx *sql.Tx) error {
		files = files[:0]
		rows, err := tx.QueryContext(ctx, `SELECT id FROM form_entries WHERE form_id = ? AND attachment_size > 0`, def.ID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			files = append(files, id)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		q := `DELETE FROM forms WHERE id = ?`
		if live {
			q += ` AND NOT (source = 'builder' AND open = 1)`
		}
		res, err := tx.ExecContext(ctx, q, def.ID)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return errors.Join(err, errFormOpen)
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM form_entries WHERE form_id = ?`, def.ID) // no-op when the FK cascade ran
		return err
	})
	if errors.Is(err, errFormOpen) {
		if cur, cerr := a.h.formBySlug(ctx, def.Slug); cerr == nil && cur.ID == def.ID {
			p.Form, p.Blocked = cur, true
			a.tpl.RenderWith(w, r, http.StatusConflict, "admin", "pages/site/form_delete.html", p)
			return
		}
		a.tpl.RenderError(w, r, http.StatusNotFound) // deleted meanwhile
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	for _, id := range files { // after the commit: a rollback keeps every file
		if err := a.h.removeUpload(id); err != nil {
			a.h.d.Log.Error("site: forms admin", "err", err)
		}
	}
	a.h.d.Audit.Record(ctx, "site.form.delete_form", "form", def.Slug, map[string]any{"entries": p.Entries})
	a.back(w, r, "", "success", "site.forms.flash.form_deleted")
}

// export writes the filtered entries as CSV (UTF-8 with BOM): id,
// created_at, status, page, every schema field (hidden and retired ones
// too), then keys found only in entries in first-seen (oldest first) order.
// Cells are passed through csvSafe.
func (a *formsAdmin) export(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	def, err := a.form(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	cond, args := filterFrom(r).where(def.ID)
	columns, labels := make([]string, 0, len(def.Fields)), make([]string, 0, len(def.Fields))
	for _, f := range adminOrder(def.Fields) {
		columns, labels = append(columns, f.Name), append(labels, f.DisplayLabel())
	}
	if err := a.extraKeys(ctx, cond, args, &columns); err != nil {
		a.fail(w, r, err)
		return
	}
	rows, err := a.h.d.DB.QueryContext(ctx, `SELECT id, created_at, status, page, data_json FROM form_entries WHERE `+cond+
		` ORDER BY created_at DESC, rowid DESC`, args...)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	defer rows.Close()
	name := "form-" + def.Slug + "-" + time.Now().UTC().Format("20060102") + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write([]byte("\xef\xbb\xbf"))
	cw := csv.NewWriter(w)
	// Schema fields are headed by their admin label; keys found only in
	// entries by their name.
	head := append(append([]string{"id", "created_at", "status", "page"}, labels...), columns[len(labels):]...)
	for i := range head {
		head[i] = csvSafe(head[i])
	}
	_ = cw.Write(head)
	n := 0
	for rows.Next() {
		var id, at, status, page, raw string
		if err = rows.Scan(&id, &at, &status, &page, &raw); err != nil {
			break
		}
		m := dataMap(raw)
		line := []string{id, at, status, page}
		for _, c := range columns {
			line = append(line, m[c])
		}
		for i := range line {
			line[i] = csvSafe(line[i])
		}
		n++
		if err = cw.Write(line); err != nil {
			break
		}
	}
	cw.Flush()
	if err = errors.Join(err, rows.Err(), cw.Error()); err != nil {
		a.h.d.Log.Error("site: export form entries", "form", def.Slug, "err", err) // headers are sent: the file ends early
	}
	a.h.d.Audit.Record(ctx, "site.form.entries.export", "form", def.Slug, map[string]any{"rows": n})
}

// extraKeys appends to columns the entry keys the schema does not list, in
// first-seen order (entries oldest first, keys sorted within one entry).
func (a *formsAdmin) extraKeys(ctx context.Context, cond string, args []any, columns *[]string) error {
	seen := make(map[string]bool, len(*columns))
	for _, c := range *columns {
		seen[c] = true
	}
	rows, err := a.h.d.DB.QueryContext(ctx, `SELECT data_json FROM form_entries WHERE `+cond+` ORDER BY created_at, rowid`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		m := dataMap(raw)
		for _, k := range sortedKeys(m) {
			if !seen[k] {
				seen[k] = true
				*columns = append(*columns, k)
			}
		}
	}
	return rows.Err()
}
