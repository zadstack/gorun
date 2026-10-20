package ext

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

const deliveriesShown = 50

type adminHandlers struct {
	s   *service
	tpl *httpx.Templates
	log *slog.Logger
}

func registerAdmin(d *kernel.Deps, s *service) error {
	tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	if !ok {
		return errors.New("ext: admin templates not registered (auth module must boot first)")
	}
	h := &adminHandlers{s: s, tpl: tpl, log: d.Log}
	r := d.Router.Admin.With(auth.RequirePerm(Perm))
	r.Get("/extensions", h.list)
	r.Post("/extensions/install", h.installZip)
	r.Post("/extensions/install-dir", h.installDir)
	r.Get("/extensions/{slug}", h.detail)
	r.Post("/extensions/{slug}/activate", h.activate)
	r.Post("/extensions/{slug}/deactivate", h.deactivate)
	r.Post("/extensions/{slug}/uninstall", h.uninstall)
	r.Post("/extensions/{slug}/settings", h.saveSettings)
	d.Admin.AddNav(kernel.NavItem{ID: "extensions", LabelKey: "ext.nav", Icon: "puzzle", Href: "/admin/extensions",
		Permission: Perm, Group: "integrations", Position: 830})
	return nil
}

type installationGroup struct {
	Source, Title, Help string
	Items               []Extension
}

func installationGroups(items []Extension) []installationGroup {
	groups := []installationGroup{
		{Source: InstallSourceDirectory, Title: "ext.group.directory", Help: "ext.group.directory_help"},
		{Source: InstallSourceUpload, Title: "ext.group.upload", Help: "ext.group.upload_help"},
		{Source: InstallSourceLegacyUnknown, Title: "ext.group.legacy_unknown", Help: "ext.group.legacy_unknown_help"},
	}
	for _, x := range items {
		index := 2
		if x.InstallSource == InstallSourceDirectory {
			index = 0
		} else if x.InstallSource == InstallSourceUpload {
			index = 1
		}
		groups[index].Items = append(groups[index].Items, x)
	}
	return groups
}

type listData struct {
	Groups    []installationGroup
	Available []Manifest
	Fields    map[string]string // install errors (field path → i18n key)
	Detail    string            // untranslated technical detail, shown escaped
	MaxMB     int
}

func (h *adminHandlers) renderList(w http.ResponseWriter, r *http.Request, status int, fields map[string]string) {
	h.renderListDetail(w, r, status, fields, "")
}

func (h *adminHandlers) renderListDetail(w http.ResponseWriter, r *http.Request, status int, fields map[string]string, detail string) {
	items, err := h.s.List(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	avail, err := h.s.Available(r.Context())
	if err != nil {
		h.log.Warn("ext: scan available packages", "err", err)
	}
	h.tpl.RenderWith(w, r, status, "admin", "pages/extensions/list.html",
		listData{Groups: installationGroups(items), Available: avail, Fields: fields, Detail: detail, MaxMB: MaxZipSize >> 20})
}

func (h *adminHandlers) list(w http.ResponseWriter, r *http.Request) {
	h.renderList(w, r, http.StatusOK, nil)
}

// installZip takes a multipart "package" (sent with hx-post so the CSRF
// header is present) and answers with the detail page revealing the key
// and secret once.
func (h *adminHandlers) installZip(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxZipSize+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		h.renderList(w, r, http.StatusUnprocessableEntity, map[string]string{"package": "ext.package.too_large"})
		return
	}
	defer r.MultipartForm.RemoveAll()
	f, hdr, err := r.FormFile("package")
	if err != nil {
		h.renderList(w, r, http.StatusUnprocessableEntity, map[string]string{"package": "ext.package.required"})
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxZipSize+1))
	if err != nil || hdr.Size > MaxZipSize || len(data) > MaxZipSize {
		h.renderList(w, r, http.StatusUnprocessableEntity, map[string]string{"package": "ext.package.too_large"})
		return
	}
	res, err := h.s.InstallZip(r.Context(), bytes.NewReader(data), int64(len(data)))
	h.installed(w, r, res, err)
}

func (h *adminHandlers) installDir(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(r.PostFormValue("slug"))
	if !slugRe.MatchString(slug) {
		h.renderList(w, r, http.StatusUnprocessableEntity, map[string]string{"slug": "ext.validation.slug"})
		return
	}
	res, err := h.s.InstallDir(r.Context(), filepath.Join(h.s.cfg.DataDir, "extensions", slug))
	h.installed(w, r, res, err)
}

func (h *adminHandlers) installed(w http.ResponseWriter, r *http.Request, res *InstallResult, err error) {
	var ve *ValidationError
	switch {
	case errors.As(err, &ve):
		if ve.Detail != "" {
			h.log.Info("ext: install rejected", "fields", ve.Fields, "detail", ve.Detail)
		}
		h.renderListDetail(w, r, http.StatusUnprocessableEntity, ve.Fields, ve.Detail)
		return
	case errors.Is(err, ErrNoOwner):
		h.renderList(w, r, http.StatusUnprocessableEntity, map[string]string{"package": "ext.error.no_owner"})
		return
	case err != nil:
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store") // the response reveals the key and secret
	w.Header().Set("HX-Push-Url", "/admin/extensions/"+res.Extension.Slug)
	h.renderDetail(w, r, http.StatusCreated, res.Extension, res, nil)
}

type settingRow struct {
	Field   SettingField
	Value   string
	Checked bool
}

type detailData struct {
	X            *Extension
	Reveal       *InstallResult
	Deliveries   []Delivery
	Settings     []settingRow
	ManifestJSON string
	Fields       map[string]string
}

func (h *adminHandlers) renderDetail(w http.ResponseWriter, r *http.Request, status int, x *Extension, reveal *InstallResult, fields map[string]string) {
	ctx := r.Context()
	ds, err := h.s.Deliveries(ctx, x.Slug, deliveriesShown)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	pretty, _ := json.MarshalIndent(x.Manifest, "", "  ")
	h.tpl.RenderWith(w, r, status, "admin", "pages/extensions/detail.html", detailData{
		X: x, Reveal: reveal, Deliveries: ds, Settings: h.settingRows(ctx, x), ManifestJSON: string(pretty), Fields: fields,
	})
}

func (h *adminHandlers) settingRows(ctx context.Context, x *Extension) []settingRow {
	rows := make([]settingRow, 0, len(x.Manifest.Settings))
	for _, f := range x.Manifest.Settings {
		var v any
		_ = h.s.settings.Get(ctx, settingKey(x.Slug, f.Key), &v)
		row := settingRow{Field: f}
		switch val := v.(type) {
		case nil:
		case string:
			row.Value = val
		case bool:
			row.Checked = val
		default:
			row.Value = fmt.Sprint(val)
		}
		rows = append(rows, row)
	}
	return rows
}

func (h *adminHandlers) load(w http.ResponseWriter, r *http.Request) (*Extension, bool) {
	x, err := h.s.Status(r.Context(), chi.URLParam(r, "slug"))
	if errors.Is(err, ErrNotFound) {
		h.tpl.RenderError(w, r, http.StatusNotFound)
		return nil, false
	}
	if err != nil {
		h.fail(w, r, err)
		return nil, false
	}
	return x, true
}

func (h *adminHandlers) detail(w http.ResponseWriter, r *http.Request) {
	if x, ok := h.load(w, r); ok {
		h.renderDetail(w, r, http.StatusOK, x, nil, nil)
	}
}

func (h *adminHandlers) lifecycle(w http.ResponseWriter, r *http.Request, fn func(ctx context.Context, slug string) error, flash string) {
	x, ok := h.load(w, r)
	if !ok {
		return
	}
	if err := fn(r.Context(), x.Slug); err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", flash)
	http.Redirect(w, r, "/admin/extensions/"+x.Slug, http.StatusSeeOther)
}

func (h *adminHandlers) activate(w http.ResponseWriter, r *http.Request) {
	h.lifecycle(w, r, h.s.Activate, "ext.flash.activated")
}

func (h *adminHandlers) deactivate(w http.ResponseWriter, r *http.Request) {
	h.lifecycle(w, r, h.s.Deactivate, "ext.flash.deactivated")
}

func (h *adminHandlers) uninstall(w http.ResponseWriter, r *http.Request) {
	x, ok := h.load(w, r)
	if !ok {
		return
	}
	purge := r.PostFormValue("purge") != ""
	if err := h.s.Uninstall(r.Context(), x.Slug, purge); err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "ext.flash.uninstalled")
	http.Redirect(w, r, "/admin/extensions", http.StatusSeeOther)
}

// saveSettings stores the declared settings from the form.
func (h *adminHandlers) saveSettings(w http.ResponseWriter, r *http.Request) {
	x, ok := h.load(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderDetail(w, r, http.StatusBadRequest, x, nil, nil)
		return
	}
	values, fields := parseSettings(x.Manifest.Settings, r.PostForm)
	if len(fields) > 0 {
		h.renderDetail(w, r, http.StatusUnprocessableEntity, x, nil, fields)
		return
	}
	for _, f := range x.Manifest.Settings {
		if err := h.s.settings.Set(r.Context(), settingKey(x.Slug, f.Key), values[f.Key]); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	h.s.audit.Record(r.Context(), "ext.settings", "extension", x.Slug, map[string]any{"keys": len(values)})
	httpx.SetFlash(w, "success", "ext.flash.settings_saved")
	http.Redirect(w, r, "/admin/extensions/"+x.Slug, http.StatusSeeOther)
}

func parseSettings(schema []SettingField, form map[string][]string) (map[string]any, map[string]string) {
	values, fields := map[string]any{}, map[string]string{}
	get := func(k string) string {
		if v := form[k]; len(v) > 0 {
			return strings.TrimSpace(v[0])
		}
		return ""
	}
	for _, f := range schema {
		raw := get(f.Key)
		switch f.Type {
		case content.FieldBoolean:
			values[f.Key] = raw != ""
			continue
		case content.FieldNumber:
			if raw == "" {
				values[f.Key] = nil
				break
			}
			n, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				fields[f.Key] = "ext.validation.number"
				continue
			}
			values[f.Key] = n
		case content.FieldSelect:
			if raw != "" && !slices.ContainsFunc(f.Options, func(o content.Option) bool { return o.Value == raw }) {
				fields[f.Key] = "ext.validation.option"
				continue
			}
			values[f.Key] = raw
		default:
			values[f.Key] = raw
		}
		if f.Required && raw == "" {
			fields[f.Key] = "ext.validation.required"
		}
	}
	return values, fields
}

func (h *adminHandlers) fail(w http.ResponseWriter, r *http.Request, err error) {
	h.log.Error("ext: admin", "err", err, "path", httpx.RedactPath(r.URL.Path))
	h.tpl.RenderError(w, r, http.StatusInternalServerError)
}
