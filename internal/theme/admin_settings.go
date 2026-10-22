package theme

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
)

const adminSettingsPath = "/appearance/theme-settings"

// settingTypes are the theme.json setting types rendered with their own
// field partial; any other type is edited (and stored) as text.
var settingTypes = []content.FieldType{"text", "textarea", "number", "boolean", "select", "date", "image"}

// SettingKey is the settings key of theme setting key of theme slug.
func SettingKey(slug, key string) string { return "theme." + slug + "." + key }

// settingView is what the admin field partials (fields/<type>) render; it
// carries every field those partials read.
type settingView struct {
	Key, Name, ID, Type, Label, Help string
	Required, Multiple, Checked      bool
	CanResetIdentity, HasOverride    bool
	Value                            string
	Values                           []string
	Options                          []settingChoice
	Min, Max, Step, Mime             string
	Error                            string
	CanPick                          bool
}

type settingChoice struct {
	Value, Label string
	Selected     bool
}

type settingsPage struct {
	Theme        Info
	Fields       []settingView
	CanPickMedia bool
	Groups       []themeSettingsGroup
}

// schema is the active theme's settings schema with unsupported types
// mapped to text.
func settingsSchema(info Info) []content.Field {
	out := make([]content.Field, 0, len(info.Settings))
	for _, f := range info.Settings {
		if f.Key == "" {
			continue
		}
		if !slices.Contains(settingTypes, f.Type) {
			f.Type = "text"
		}
		f.Multiple = false
		out = append(out, f)
	}
	if !supportsIdentity(info) {
		return out
	}
	return withIdentity(out)
}

func pickLabel(m map[string]string, locale, def string) string {
	for _, k := range []string{locale, "en", "ar"} {
		if v := m[k]; v != "" {
			return v
		}
	}
	return def
}

func (a *admin) canPickMedia(ctx context.Context) bool {
	_, ok := kernel.Get[media.Service](a.s.d, kernel.ServiceMedia)
	return ok && auth.Can(ctx, "media.upload")
}

// settingViews builds the form from the schema and values (stored values,
// or the submitted ones after a failed save).
func (a *admin) settingViews(ctx context.Context, schema []content.Field, values map[string]any, errs map[string]string) []settingView {
	locale := i18n.Locale(ctx)
	canPick := a.canPickMedia(ctx)
	var active *Info // the active theme, read once when a variant select needs it
	out := make([]settingView, 0, len(schema))
	for _, f := range schema {
		name := "settings[" + f.Key + "]"
		v := settingView{Key: f.Key, Name: name, ID: "settings-" + f.Key, Type: string(f.Type), Label: pickLabel(f.Label, locale, f.Key),
			Help: pickLabel(f.Help, locale, ""), Required: f.Required, Value: displayValue(values[f.Key]), Error: errs[f.Key], CanPick: canPick}
		if f.Min != nil {
			v.Min = strconv.FormatFloat(*f.Min, 'f', -1, 64)
		}
		if f.Max != nil {
			v.Max = strconv.FormatFloat(*f.Max, 'f', -1, 64)
		}
		if f.Step != nil {
			v.Step = strconv.FormatFloat(*f.Step, 'f', -1, 64)
		}
		switch f.Type {
		case "boolean":
			b, _ := values[f.Key].(bool)
			v.Checked = b || v.Value == "true" || v.Value == "1"
		case "select":
			declared, isVariant := []string(nil), false
			if slot, ok := variantSlotOf(f.Key); ok {
				if active == nil {
					info := a.s.Active(ctx)
					active = &info
				}
				declared, isVariant = DeclaredVariants(*active, slot), true
			}
			var missing []string
			for _, o := range f.Options {
				if isVariant && !slices.Contains(declared, o.Value) {
					label := pickLabel(o.Label, locale, o.Value)
					missing = append(missing, label)
					if o.Value == v.Value && v.Value != "" {
						// Keep the stored value selected (it renders as native) so a
						// save does not silently replace it.
						note := "not offered"
						if a.s.d.I18n != nil {
							note = a.s.d.I18n.T(locale, "theme.settings.variant_not_offered")
						}
						v.Options = append(v.Options, settingChoice{Value: o.Value, Label: label + " — " + note, Selected: true})
					}
					continue
				}
				v.Options = append(v.Options, settingChoice{Value: o.Value, Label: pickLabel(o.Label, locale, o.Value), Selected: o.Value == v.Value})
			}
			if len(missing) > 0 && a.s.d.I18n != nil {
				sep := ", "
				if locale == "ar" {
					sep = "، "
				}
				v.Help = strings.TrimSpace(v.Help + " " + a.s.d.I18n.T(locale, "theme.settings.variant_unavailable", strings.Join(missing, sep)))
			}
		}
		v.CanResetIdentity = isIdentityResetKey(f.Key)
		v.HasOverride = v.Value != ""
		out = append(out, v)
	}
	return out
}

func displayValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// storedValues reads theme.<slug>.<key> for every schema key.
func (a *admin) storedValues(ctx context.Context, slug string, schema []content.Field) map[string]any {
	out := map[string]any{}
	for _, f := range schema {
		var v any
		if err := a.s.d.Settings.Get(ctx, SettingKey(slug, f.Key), &v); err == nil {
			out[f.Key] = v
		}
	}
	return out
}

func (a *admin) renderSettings(w http.ResponseWriter, r *http.Request, status int, info Info, values map[string]any, errs map[string]string) {
	ctx := r.Context()
	page := settingsPage{Theme: info, Fields: a.settingViews(ctx, settingsSchema(info), values, errs), CanPickMedia: a.canPickMedia(ctx)}
	if !supportsIdentity(info) {
		for i := range page.Fields {
			page.Fields[i].CanResetIdentity = false
		}
	}
	page.Groups = themeSettingsGroups(page.Fields)
	a.tpl.RenderWith(w, r, status, "admin", "pages/appearance/theme_settings.html", page)
}

// themeSettings is GET Appearance → Theme settings.
func (a *admin) themeSettings(w http.ResponseWriter, r *http.Request) {
	info := a.s.Active(r.Context())
	a.renderSettings(w, r, http.StatusOK, info, a.storedValues(r.Context(), info.Slug, settingsSchema(info)), nil)
}

// themeSettingsSave validates the form against the theme.json schema
// (content field rules; images must be existing media) and stores every
// key as theme.<slug>.<key> in one transaction, audited.
func (a *admin) themeSettingsSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	info := a.s.Active(ctx)
	schema := settingsSchema(info)
	raw := map[string]any{}
	for _, f := range schema {
		vs := r.PostForm["settings["+f.Key+"]"]
		if len(vs) > 0 {
			raw[f.Key] = strings.TrimSpace(vs[len(vs)-1]) // boolean: hidden "0" then the checkbox
		}
	}
	vals, errs := a.validateSettings(ctx, schema, raw)
	if len(errs) > 0 {
		a.renderSettings(w, r, http.StatusUnprocessableEntity, info, raw, errs)
		return
	}
	keys := make([]string, 0, len(schema))
	err := db.InTx(ctx, a.s.d.DB, func(tx *sql.Tx) error {
		for _, f := range schema {
			k := SettingKey(info.Slug, f.Key)
			if err := a.s.d.Settings.SetTx(ctx, tx, k, vals[f.Key]); err != nil {
				return err
			}
			keys = append(keys, k)
		}
		return nil
	})
	if err != nil {
		a.s.log.Error("theme: save settings", "err", err)
		a.tpl.RenderError(w, r, http.StatusInternalServerError)
		return
	}
	a.s.d.Audit.Record(ctx, "theme.settings.update", "theme", info.Slug, map[string][]string{"keys": keys})
	httpx.SetFlash(w, "success", "theme.settings.saved")
	http.Redirect(w, r, "/admin"+adminSettingsPath, http.StatusSeeOther)
}

// validateSettings coerces raw form values with the content field rules
// and checks image values against the media service. Missing values store
// as "" (text) / false (boolean) / nil.
func (a *admin) validateSettings(ctx context.Context, schema []content.Field, raw map[string]any) (map[string]any, map[string]string) {
	errs := map[string]string{}
	vals := map[string]any{}
	if cs, ok := kernel.Get[content.Service](a.s.d, kernel.ServiceContent); ok {
		v, fe := cs.Validate(&content.Type{Slug: "theme-settings", Fields: schema}, raw)
		for k, e := range fe {
			errs[k] = e
		}
		vals = v
	} else {
		for k, v := range raw {
			vals[k] = v
		}
	}
	for k, e := range validateIdentity(vals) {
		errs[k] = e
	}
	for _, f := range schema {
		if f.Type != "image" {
			continue
		}
		id := displayValue(vals[f.Key])
		if id == "" {
			continue
		}
		ms, ok := kernel.Get[media.Service](a.s.d, kernel.ServiceMedia)
		if !ok {
			errs[f.Key] = "theme.settings.error_image"
			continue
		}
		if _, err := ms.Get(ctx, id); err != nil {
			errs[f.Key] = "theme.settings.error_image"
		}
	}
	for _, f := range schema {
		if _, ok := vals[f.Key]; !ok && f.Type == "text" {
			vals[f.Key] = ""
		}
	}
	return vals, errs
}
