package wpmigrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

// maxWXRUpload caps a WXR upload (spec: 200 MB).
const maxWXRUpload = 200 << 20

// wizardSteps are the seven wizard steps (spec §4.7).
var wizardSteps = []string{"connect", "scan", "map", "fields", "preview", "import", "validate"}

type wizard struct {
	s   *service
	tpl *httpx.Templates
	log *slog.Logger
}

func registerAdmin(d *kernel.Deps, s *service) error {
	tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	if !ok {
		return fmt.Errorf("wpmigrate: admin templates not registered (auth module must boot first)")
	}
	w := &wizard{s: s, tpl: tpl, log: d.Log}
	r := d.Router.Admin.With(auth.RequirePerm(Permission))
	r.Get("/migrations", w.list)
	r.Get("/migrations/new", w.connectPage)
	r.Post("/migrations/connect", w.connect)
	r.Post("/migrations/upload", w.upload)
	r.Get("/migrations/{id}/scan", w.scanPage)
	r.Post("/migrations/{id}/scan", w.scan)
	r.Get("/migrations/{id}/map", w.mapPage)
	r.Post("/migrations/{id}/map", w.saveMap)
	r.Get("/migrations/{id}/fields", w.fieldsPage)
	r.Post("/migrations/{id}/fields", w.saveFields)
	r.Get("/migrations/{id}/preview", w.previewPage)
	r.Post("/migrations/{id}/preview", w.preview)
	r.Get("/migrations/{id}/import", w.importPage)
	for _, action := range []string{"start", "pause", "resume", "retry"} {
		r.Post("/migrations/{id}/"+action, w.control(action))
	}
	r.Get("/x/migrations/{id}/progress", w.progress)
	r.Get("/migrations/{id}/validate", w.validatePage)
	r.Post("/migrations/{id}/validate", w.validate)
	r.Get("/migrations/{id}/report.md", w.reportMarkdown)
	d.Admin.AddNav(kernel.NavItem{ID: "migrations", LabelKey: "wpmigrate.nav", Icon: "import", Href: "/admin/migrations",
		Permission: Permission, Group: "system", Position: 940})
	return nil
}

// stepLink is one entry of the stepper.
type stepLink struct {
	N             int
	Key, Href     string
	Current, Done bool
}

// page is the data every wizard template receives.
type page struct {
	Steps      []stepLink
	M          *MigrationView
	Source     *SourceView
	Report     *Report
	Error      string            // i18n key
	Fields     map[string]string // field → i18n key
	Form       connectForm
	Policies   []string
	TypeRows   []typeRow
	TaxRows    []taxRow
	FieldRows  []fieldRow
	Targets    []string
	Preview    *PreviewResult
	Status     *Status
	Kinds      []kindBar
	Log        []LogEntry
	Validate   *ValidateReport
	Migrations []MigrationView
	Sources    []SourceView
	Polling    bool
	CanStart   bool
	CanPause   bool
	CanResume  bool
	CanRetry   bool
}

type connectForm struct {
	Name, URL, Username, SlugPolicy, Rate string
	KeepClasses                           bool
}

type typeRow struct {
	WPType, Name, Target string
	Count                int
	Options              []string
}

type taxRow struct {
	WPTax, Target string
	Count         int
}

type fieldRow struct {
	WPType, Key, Target, Type string
}

func (w *wizard) steps(id, current string, status string) []stepLink {
	reached := map[string]int{StatusDraft: 1, StatusScanned: 2, StatusMapped: 4, StatusPreviewed: 5, StatusRunning: 6,
		StatusPaused: 6, StatusFailed: 6, StatusCompleted: 6, StatusValidated: 7}[status]
	out := make([]stepLink, len(wizardSteps))
	for i, k := range wizardSteps {
		href := "/admin/migrations/new"
		if id != "" && k != "connect" {
			href = "/admin/migrations/" + id + "/" + k
		}
		out[i] = stepLink{N: i + 1, Key: k, Href: href, Current: k == current, Done: i < reached}
	}
	return out
}

func (w *wizard) load(rw http.ResponseWriter, r *http.Request) (*MigrationView, *migrationRow, bool) {
	id := chi.URLParam(r, "id")
	v, err := w.s.Migration(r.Context(), id)
	if err == nil {
		var m *migrationRow
		if m, err = w.s.getMigration(r.Context(), id); err == nil {
			return v, m, true
		}
	}
	if errors.Is(err, ErrNotFound) {
		w.tpl.RenderError(rw, r, http.StatusNotFound)
	} else {
		w.fail(rw, r, err)
	}
	return nil, nil, false
}

func (w *wizard) fail(rw http.ResponseWriter, r *http.Request, err error) {
	w.log.Error("wpmigrate: admin", "err", err, "path", httpx.RedactPath(r.URL.Path))
	w.tpl.RenderError(rw, r, http.StatusInternalServerError)
}

func (w *wizard) render(rw http.ResponseWriter, r *http.Request, status int, name string, p page) {
	w.tpl.RenderWith(rw, r, status, "admin", "pages/migrations/"+name+".html", p)
}

func (w *wizard) list(rw http.ResponseWriter, r *http.Request) {
	ms, err := w.s.Migrations(r.Context())
	if err != nil {
		w.fail(rw, r, err)
		return
	}
	srcs, err := w.s.Sources(r.Context())
	if err != nil {
		w.fail(rw, r, err)
		return
	}
	w.render(rw, r, http.StatusOK, "list", page{Migrations: ms, Sources: srcs})
}

func (w *wizard) connectPage(rw http.ResponseWriter, r *http.Request) {
	w.render(rw, r, http.StatusOK, "wizard_connect", page{Steps: w.steps("", "connect", ""), Policies: policies(),
		Form: connectForm{SlugPolicy: SlugSuffix}})
}

func policies() []string { return []string{SlugSuffix, SlugSkip, SlugOverwrite} }

func (w *wizard) connect(rw http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(rw, "bad form", http.StatusBadRequest)
		return
	}
	f := connectForm{Name: strings.TrimSpace(r.PostFormValue("name")), URL: strings.TrimSpace(r.PostFormValue("url")),
		Username: strings.TrimSpace(r.PostFormValue("username")), SlugPolicy: r.PostFormValue("slug_policy"),
		KeepClasses: r.PostFormValue("keep_classes") == "1", Rate: strings.TrimSpace(r.PostFormValue("rate_per_second"))}
	rate, ok := parseRate(f.Rate) // blank: the setting / default
	opts := Options{SlugPolicy: f.SlugPolicy, KeepClasses: f.KeepClasses, RatePerSecond: rate}.normalized()
	f.SlugPolicy = opts.SlugPolicy
	if !ok {
		w.render(rw, r, http.StatusUnprocessableEntity, "wizard_connect", page{Steps: w.steps("", "connect", ""),
			Policies: policies(), Form: f, Fields: map[string]string{"rate_per_second": "wpmigrate.error.rate"}})
		return
	}
	src, err := w.s.CreateSource(r.Context(), SourceInput{Name: f.Name, URL: f.URL, Username: f.Username,
		AppPassword: r.PostFormValue("app_password")})
	if errors.Is(err, ErrInvalidSource) {
		fields := map[string]string{"url": "wpmigrate.error.url"}
		if strings.Contains(err.Error(), "together") {
			fields = map[string]string{"app_password": "wpmigrate.error.credentials_pair"}
		}
		// The password is never echoed back.
		w.render(rw, r, http.StatusUnprocessableEntity, "wizard_connect", page{Steps: w.steps("", "connect", ""),
			Policies: policies(), Form: f, Fields: fields})
		return
	}
	if err != nil {
		w.fail(rw, r, err)
		return
	}
	m, err := w.s.CreateMigration(r.Context(), src.ID, opts, userID(r))
	if err != nil {
		w.fail(rw, r, err)
		return
	}
	http.Redirect(rw, r, "/admin/migrations/"+m.ID+"/scan", http.StatusSeeOther)
}

func userID(r *http.Request) string {
	if u := auth.UserFromContext(r.Context()); u != nil {
		return u.ID
	}
	return ""
}

// upload stores a WXR export under <DataDir>/imports/ (multipart via HTMX,
// which sends the CSRF header).
func (w *wizard) upload(rw http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(rw, r.Body, maxWXRUpload+1<<20)
	bad := func(key string) {
		w.render(rw, r, http.StatusUnprocessableEntity, "wizard_connect", page{Steps: w.steps("", "connect", ""),
			Policies: policies(), Form: connectForm{SlugPolicy: SlugSuffix}, Fields: map[string]string{"wxr": key}})
	}
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		bad("wpmigrate.error.upload_size")
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, hdr, err := r.FormFile("wxr")
	if err != nil {
		bad("wpmigrate.error.upload_missing")
		return
	}
	defer file.Close()
	dir := filepath.Join(w.s.cfg.DataDir, "imports")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		w.fail(rw, r, err)
		return
	}
	dst := filepath.Join(dir, strings.ToLower(db.NewID())+".xml")
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o640)
	if err != nil {
		w.fail(rw, r, err)
		return
	}
	n, err := io.Copy(out, io.LimitReader(file, maxWXRUpload+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil || n > maxWXRUpload {
		os.Remove(dst)
		if err != nil {
			w.fail(rw, r, err)
			return
		}
		bad("wpmigrate.error.upload_size")
		return
	}
	if _, err := openWXR(dst, ""); err != nil {
		os.Remove(dst)
		bad("wpmigrate.error.upload_invalid")
		return
	}
	src, err := w.s.createWXRSource(r.Context(), filepath.Base(hdr.Filename), dst)
	if err != nil {
		w.fail(rw, r, err)
		return
	}
	opts := Options{SlugPolicy: r.FormValue("slug_policy"), Currency: strings.ToUpper(strings.TrimSpace(r.FormValue("currency")))}
	m, err := w.s.CreateMigration(r.Context(), src.ID, opts, userID(r))
	if err != nil {
		w.fail(rw, r, err)
		return
	}
	to := "/admin/migrations/" + m.ID + "/scan"
	if r.Header.Get("HX-Request") == "true" {
		rw.Header().Set("HX-Redirect", to)
		rw.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(rw, r, to, http.StatusSeeOther)
}

func (w *wizard) scanPage(rw http.ResponseWriter, r *http.Request) {
	v, m, ok := w.load(rw, r)
	if !ok {
		return
	}
	p := page{Steps: w.steps(v.ID, "scan", v.Status), M: v, Report: m.Discovery}
	if src, err := w.s.getSource(r.Context(), v.SourceID); err == nil {
		p.Source = &src.SourceView
		if p.Report == nil && src.LastDiscoveryAt != "" {
			p.Report = w.lastReport(r, src.ID)
		}
	}
	w.render(rw, r, http.StatusOK, "wizard_scan", p)
}

func (w *wizard) lastReport(r *http.Request, sourceID string) *Report {
	var raw string
	if err := w.s.db.QueryRowContext(r.Context(), `SELECT COALESCE(last_discovery_json, '') FROM wp_sources WHERE id = ?`, sourceID).Scan(&raw); err != nil || raw == "" {
		return nil
	}
	var rep Report
	if json.Unmarshal([]byte(raw), &rep) != nil {
		return nil
	}
	return &rep
}

func (w *wizard) scan(rw http.ResponseWriter, r *http.Request) {
	v, _, ok := w.load(rw, r)
	if !ok {
		return
	}
	rep, err := w.s.Discover(r.Context(), v.SourceID)
	p := page{M: v, Report: rep}
	switch {
	case errors.Is(err, ErrAuthFailed):
		p.Error = "wpmigrate.scan.auth_failed"
	case errors.Is(err, ErrAuthUnavailable):
		p.Error = "wpmigrate.scan.auth_unavailable"
	case errors.Is(err, ErrUnreachable), errors.Is(err, ErrNotWordPress), errors.Is(err, ErrInvalidSource):
		p.Error, p.Report = "wpmigrate.scan.unreachable", nil
	case err != nil:
		w.fail(rw, r, err)
		return
	}
	v, _ = w.s.Migration(r.Context(), v.ID)
	p.M, p.Steps = v, w.steps(v.ID, "scan", v.Status)
	if src, err := w.s.Source(r.Context(), v.SourceID); err == nil {
		p.Source = src
	}
	status := http.StatusOK
	if p.Error != "" {
		status = http.StatusUnprocessableEntity
	}
	w.render(rw, r, status, "wizard_scan", p)
}

// mapping returns the stored mapping or the defaults from the report.
func (w *wizard) mapping(m *migrationRow) Mapping { return m.Mapping.normalized(m.Discovery) }

// typeTargets are the choices for a WordPress type.
func typeTargets(wpType string) []string {
	if wpType == "product" {
		return []string{TargetProduct, TargetSkip}
	}
	out := []string{"post", "page", TargetCreate, TargetSkip}
	return out
}

func (w *wizard) mapPage(rw http.ResponseWriter, r *http.Request) {
	v, m, ok := w.load(rw, r)
	if !ok {
		return
	}
	if m.Discovery == nil {
		http.Redirect(rw, r, "/admin/migrations/"+v.ID+"/scan", http.StatusSeeOther)
		return
	}
	mp := w.mapping(m)
	p := page{Steps: w.steps(v.ID, "map", v.Status), M: v, Report: m.Discovery}
	for _, typ := range sortedKeys(mp.Types) {
		p.TypeRows = append(p.TypeRows, typeRow{WPType: typ, Name: mp.Types[typ].Name, Target: mp.Types[typ].Target,
			Count: m.Discovery.Counts[typ], Options: typeTargets(typ)})
	}
	taxes := map[string]bool{}
	for k := range mp.Taxonomies {
		taxes[k] = true
	}
	for _, f := range m.Discovery.Available {
		if t, ok := strings.CutPrefix(f.Kind, "taxonomy:"); ok {
			taxes[t] = true
		}
	}
	for _, t := range sortedKeys(taxes) {
		p.TaxRows = append(p.TaxRows, taxRow{WPTax: t, Target: mp.Taxonomies[t], Count: m.Discovery.Counts[t]})
	}
	if names, err := w.s.tax.Taxonomies(r.Context()); err == nil {
		for _, t := range names {
			p.Targets = append(p.Targets, t.Slug)
		}
	}
	w.render(rw, r, http.StatusOK, "wizard_map_types", p)
}

func (w *wizard) saveMap(rw http.ResponseWriter, r *http.Request) {
	v, m, ok := w.load(rw, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(rw, "bad form", http.StatusBadRequest)
		return
	}
	mp := w.mapping(m)
	for typ, tm := range mp.Types {
		if val := r.PostFormValue("type." + typ); val != "" && slices.Contains(typeTargets(typ), val) {
			tm.Target = val
			mp.Types[typ] = tm
		}
	}
	for key, vals := range r.PostForm {
		if tax, ok := strings.CutPrefix(key, "tax."); ok && len(vals) > 0 {
			mp.Taxonomies[tax] = strings.TrimSpace(vals[0])
		}
	}
	if err := w.s.SaveMapping(r.Context(), v.ID, mp); err != nil {
		w.fail(rw, r, err)
		return
	}
	http.Redirect(rw, r, "/admin/migrations/"+v.ID+"/fields", http.StatusSeeOther)
}

var fieldTypes = []string{"text", "textarea", "richtext", "number", "date", "boolean", "json"}

func (w *wizard) fieldsPage(rw http.ResponseWriter, r *http.Request) {
	v, m, ok := w.load(rw, r)
	if !ok {
		return
	}
	mp := w.mapping(m)
	p := page{Steps: w.steps(v.ID, "fields", v.Status), M: v, Targets: fieldTypes}
	for _, typ := range sortedKeys(mp.Fields) {
		for _, key := range sortedKeys(mp.Fields[typ]) {
			fm := mp.Fields[typ][key]
			p.FieldRows = append(p.FieldRows, fieldRow{WPType: typ, Key: key, Target: fm.Target, Type: firstNonEmpty(fm.Type, "text")})
		}
	}
	w.render(rw, r, http.StatusOK, "wizard_map_fields", p)
}

func (w *wizard) saveFields(rw http.ResponseWriter, r *http.Request) {
	v, m, ok := w.load(rw, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(rw, "bad form", http.StatusBadRequest)
		return
	}
	mp := w.mapping(m)
	errs := map[string]string{}
	for typ, fields := range mp.Fields {
		for key, fm := range fields {
			name := "field." + typ + "." + key
			fm.Target = strings.TrimSpace(r.PostFormValue(name))
			if t := r.PostFormValue(name + ".type"); slices.Contains(fieldTypes, t) {
				fm.Type = t
			}
			if fm.Target != "" && !fieldKey.MatchString(fm.Target) {
				errs[name] = "wpmigrate.error.field_key"
			}
			fields[key] = fm
		}
	}
	if len(errs) > 0 {
		p := page{Steps: w.steps(v.ID, "fields", v.Status), M: v, Targets: fieldTypes, Fields: errs}
		for _, typ := range sortedKeys(mp.Fields) {
			for _, key := range sortedKeys(mp.Fields[typ]) {
				fm := mp.Fields[typ][key]
				p.FieldRows = append(p.FieldRows, fieldRow{WPType: typ, Key: key, Target: fm.Target, Type: firstNonEmpty(fm.Type, "text")})
			}
		}
		w.render(rw, r, http.StatusUnprocessableEntity, "wizard_map_fields", p)
		return
	}
	if err := w.s.SaveMapping(r.Context(), v.ID, mp); err != nil {
		w.fail(rw, r, err)
		return
	}
	http.Redirect(rw, r, "/admin/migrations/"+v.ID+"/preview", http.StatusSeeOther)
}

func (w *wizard) previewPage(rw http.ResponseWriter, r *http.Request) {
	v, _, ok := w.load(rw, r)
	if !ok {
		return
	}
	p := page{Steps: w.steps(v.ID, "preview", v.Status), M: v}
	if v.DryRun || v.Status == StatusPreviewed {
		p.Preview, _ = w.s.previewResult(r.Context(), v.ID)
	}
	w.render(rw, r, http.StatusOK, "wizard_preview", p)
}

func (w *wizard) preview(rw http.ResponseWriter, r *http.Request) {
	v, _, ok := w.load(rw, r)
	if !ok {
		return
	}
	res, err := w.s.Preview(r.Context(), v.ID)
	p := page{M: v, Preview: res}
	switch {
	case errors.Is(err, ErrBadState):
		p.Error = "wpmigrate.error.running"
	case errors.Is(err, ErrAuthFailed):
		p.Error = "wpmigrate.scan.auth_failed"
	case err != nil:
		w.fail(rw, r, err)
		return
	}
	v, _ = w.s.Migration(r.Context(), v.ID)
	p.M, p.Steps = v, w.steps(v.ID, "preview", v.Status)
	w.render(rw, r, http.StatusOK, "wizard_preview", p)
}
