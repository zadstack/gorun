package builder

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/brand"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
)

type identityForm struct {
	Title, Tagline, Logo string
	Identity             brand.Identity
}
type identityMedia struct {
	Name, ID, Value, Label string
	Multiple, CanPick      bool
}
type identityHandler struct {
	d   *kernel.Deps
	tpl *httpx.Templates
}

func registerIdentity(d *kernel.Deps) error {
	tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	if !ok {
		return errors.New("builder: admin templates unavailable")
	}
	h := &identityHandler{d: d, tpl: tpl}
	d.Admin.AddNav(kernel.NavItem{ID: "builder.identity", LabelKey: "identity.title", Icon: "palette", Href: "/admin/appearance/identity", Permission: "settings.manage", Group: "appearance", Position: 15})
	r := d.Router.Admin.With(auth.RequirePerm("settings.manage"))
	r.Get("/appearance/identity", h.view)
	r.Post("/appearance/identity", h.save)
	return nil
}
func (h *identityHandler) values(ctx context.Context) identityForm {
	form := identityForm{Title: h.d.Settings.GetString(ctx, "site.title", "GoRun"), Tagline: h.d.Settings.GetString(ctx, "site.tagline", ""), Logo: h.d.Settings.GetString(ctx, "site.logo", ""), Identity: brand.Default()}
	_ = h.d.Settings.Get(ctx, "builder.identity", &form.Identity)
	return form
}
func (h *identityHandler) render(w http.ResponseWriter, r *http.Request, status int, form identityForm, message string) {
	label := "Logo"
	if i18n.Locale(r.Context()) == "ar" {
		label = "الشعار"
	}
	h.tpl.RenderWith(w, r, status, "admin", "pages/builder/identity.html", struct {
		Form  identityForm
		Error string
		Saved bool
		Logo  identityMedia
	}{form, message, r.URL.Query().Get("saved") == "1", identityMedia{Name: "logo", ID: "identity-logo", Value: form.Logo, Label: label, CanPick: auth.Can(r.Context(), "media.upload")}})
}
func (h *identityHandler) view(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, 200, h.values(r.Context()), "")
}
func (h *identityHandler) store(ctx context.Context, form identityForm) error {
	if !auth.Can(ctx, "settings.manage") {
		return content.ErrForbidden
	}
	if strings.TrimSpace(form.Title) == "" || len(form.Title) > 200 || len(form.Tagline) > 500 {
		return errors.New("identity: site name must be 1-200 characters")
	}
	if err := brand.Validate(form.Identity); err != nil {
		return err
	}
	if form.Logo != "" {
		ms, ok := kernel.Get[media.Service](h.d, kernel.ServiceMedia)
		if !ok {
			return errors.New("identity: media library unavailable")
		}
		if _, err := ms.Get(ctx, form.Logo); err != nil {
			return errors.New("identity: not an existing logo in the media library")
		}
	}
	return db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		for key, value := range map[string]any{"site.title": strings.TrimSpace(form.Title), "site.tagline": strings.TrimSpace(form.Tagline), "site.logo": form.Logo, "builder.identity": form.Identity} {
			if err := h.d.Settings.SetTx(ctx, tx, key, value); err != nil {
				return err
			}
		}
		h.d.Audit.RecordTx(ctx, tx, "builder.identity", "settings", "", map[string]any{"keys": []string{"site.title", "site.tagline", "site.logo", "builder.identity"}})
		return nil
	})
}
func (h *identityHandler) save(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", 400)
		return
	}
	form := identityForm{Title: r.PostForm.Get("title"), Tagline: r.PostForm.Get("tagline"), Logo: r.PostForm.Get("logo"), Identity: brand.Identity{Accent: r.PostForm.Get("accent"), Background: r.PostForm.Get("background"), Text: r.PostForm.Get("text"), Font: r.PostForm.Get("font"), Width: r.PostForm.Get("width")}}
	if err := h.store(r.Context(), form); err != nil {
		h.render(w, r, 422, form, identityError(h.d, r.Context(), err))
		return
	}
	http.Redirect(w, r, "/admin/appearance/identity?saved=1", http.StatusSeeOther)
}
func previewBrand(d *kernel.Deps, ctx context.Context, id string) string {
	identity := brand.Default()
	var raw string
	// Pending starters preview their proposed identity without changing the live site.
	err := d.DB.QueryRowContext(ctx, `SELECT json_extract(s.value_json,'$.identity') FROM settings s,json_each(s.value_json,'$.pages') p WHERE s.key LIKE 'builder.starter.%' AND COALESCE(json_extract(s.value_json,'$.active'),0)=0 AND p.value=? ORDER BY s.updated_at DESC LIMIT 1`, id).Scan(&raw)
	if err == nil && json.Unmarshal([]byte(raw), &identity) == nil && brand.Validate(identity) == nil {
		return brand.CSS(identity)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		d.Log.Warn("builder: preview identity lookup failed", "err", err)
	}
	if err := d.Settings.Get(ctx, "builder.identity", &identity); err != nil {
		identity = brand.Default()
	}
	return brand.CSS(identity)
}

func identityError(d *kernel.Deps, ctx context.Context, err error) string {
	for _, entry := range []struct {
		err error
		key string
	}{{brand.ErrColors, "identity.colors_invalid"}, {brand.ErrFont, "identity.font_invalid"}, {brand.ErrWidth, "identity.width_invalid"}, {brand.ErrTextContrast, "identity.text_contrast"}, {brand.ErrButtonContrast, "identity.button_contrast"}} {
		if errors.Is(err, entry.err) {
			return d.I18n.T(i18n.Locale(ctx), entry.key)
		}
	}
	if i18n.Locale(ctx) == "ar" {
		return "تحقق من اسم الموقع والشعار والإعدادات وحاول الحفظ مرة أخرى."
	}
	return "Check the site name, logo and settings, then try saving again."
}
