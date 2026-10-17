package site

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

type availabilityAdmin struct {
	h   *handlers
	tpl *httpx.Templates
}
type availabilityForm struct {
	AR, PasswordSet, PlatformRestricted bool
	Mode, Message, PlatformMode, Error  string
	Revision                            int64
}

func registerAvailabilityAdmin(d *kernel.Deps, h *handlers) {
	tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	if !ok {
		return
	}
	a := &availabilityAdmin{h, tpl}
	d.Admin.AddNav(kernel.NavItem{ID: "site.availability", LabelKey: "site.availability.nav", Href: "/admin/settings/availability", Icon: "shield", Permission: "settings.manage", Group: "settings", Position: 730})
	r := d.Router.Admin.With(auth.RequirePerm("settings.manage"))
	r.Get("/settings/availability", a.show)
	r.Post("/settings/availability", a.save)
}
func (a *availabilityAdmin) render(w http.ResponseWriter, r *http.Request, status int, problem string) {
	p, e := a.h.localAvailability(r.Context())
	if e != nil {
		http.Error(w, "Unable to read availability", 500)
		return
	}
	platform, pe := platformPolicy()
	mode := platform.Mode
	if pe != nil {
		mode = "blocked"
	}
	f := availabilityForm{AR: i18n.Locale(r.Context()) == "ar", Mode: p.Mode, Message: p.Message, Revision: p.Revision, PasswordSet: p.PasswordHash != "", PlatformMode: mode, PlatformRestricted: mode != "public", Error: problem}
	if p.Mode == "public" && a.h.boolSetting(r.Context(), SettingMaintenance, false) {
		f.Mode = "construction"
	}
	w.Header().Set("Cache-Control", "private, no-store")
	a.tpl.RenderWith(w, r, status, "admin", "pages/settings/availability.html", f)
}
func (a *availabilityAdmin) show(w http.ResponseWriter, r *http.Request) { a.render(w, r, 200, "") }
func (a *availabilityAdmin) save(w http.ResponseWriter, r *http.Request) {
	if e := r.ParseForm(); e != nil {
		a.render(w, r, 400, "Invalid form / بيانات غير صالحة")
		return
	}
	old, e := a.h.localAvailability(r.Context())
	if e != nil {
		http.Error(w, "Unable to read availability", 500)
		return
	}
	revision, e := strconv.ParseInt(r.FormValue("revision"), 10, 64)
	if e != nil || revision != old.Revision {
		a.render(w, r, 409, "Settings changed. Reload and try again. / تغيرت الإعدادات، حدّث الصفحة.")
		return
	}
	p := Availability{Mode: r.FormValue("mode"), Message: strings.TrimSpace(r.FormValue("message")), PasswordHash: old.PasswordHash, Revision: old.Revision + 1}
	if r.FormValue("clear_password") == "1" {
		p.PasswordHash = ""
	}
	if password := r.FormValue("password"); password != "" {
		if len(password) < 12 || len(password) > 256 {
			a.render(w, r, 422, "Use a password of 12–256 characters. / استخدم كلمة مرور من 12 إلى 256 حرفًا.")
			return
		}
		p.PasswordHash, e = auth.HashPassword(password)
		if e != nil {
			http.Error(w, "Unable to secure password", 500)
			return
		}
	}
	if !validAvailability(p, false) {
		a.render(w, r, 422, "Choose a valid mode, message and password. / راجع وضع الموقع والرسالة وكلمة المرور.")
		return
	}
	e = db.InTx(r.Context(), a.h.d.DB, func(tx *sql.Tx) error {
		// The write lock and revision check prevent two open forms from silently replacing each other.
		if _, e := tx.ExecContext(r.Context(), "UPDATE settings SET updated_at=updated_at WHERE key=?", SettingAvailability); e != nil {
			return e
		}
		var raw string
		var current Availability
		e := tx.QueryRowContext(r.Context(), "SELECT value_json FROM settings WHERE key=?", SettingAvailability).Scan(&raw)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if raw != "" {
			if e := json.Unmarshal([]byte(raw), &current); e != nil {
				return e
			}
		}
		if current.Revision != revision {
			return errors.New("conflict")
		}
		if e := a.h.d.Settings.SetTx(r.Context(), tx, SettingAvailability, p); e != nil {
			return e
		}
		if e := a.h.d.Settings.SetTx(r.Context(), tx, SettingMaintenance, false); e != nil {
			return e
		}
		a.h.d.Audit.RecordTx(r.Context(), tx, "site.availability.update", "settings", SettingAvailability, map[string]any{"mode": p.Mode, "revision": p.Revision})
		return nil
	})
	if e != nil {
		a.render(w, r, 409, "Unable to save. Reload and retry. / تعذّر الحفظ، حدّث الصفحة.")
		return
	}
	http.Redirect(w, r, "/admin/settings/availability", 303)
}
