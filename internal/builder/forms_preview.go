package builder

import (
	"bytes"
	"context"
	"html/template"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/theme"
)

// draftFormTemplate mirrors site's form markup (internal/site/forms_render.go)
// class for class, so themes style a draft preview exactly like the
// published form. It has no action, method or CSRF field, and every control
// and the button are disabled: a preview never posts. html/template escapes
// every author value.
var draftFormTemplate = template.Must(template.New("draft-form").Parse(
	`<form class="sb-form gp-form-fields">` +
		`{{range .Fields}}<div class="sf-field sf-{{.Type}}">` +
		`{{if eq .Type "checkbox"}}` +
		`<input id="{{.ID}}" name="{{.Name}}" type="checkbox" value="yes"{{if .Required}} required{{end}}{{with .HelpID}} aria-describedby="{{.}}"{{end}} disabled>` +
		`<label for="{{.ID}}">{{.Label}}</label>` +
		`{{else}}` +
		`<label for="{{.ID}}">{{.Label}}</label>` +
		`{{if eq .Type "textarea"}}<textarea id="{{.ID}}" name="{{.Name}}" rows="5" maxlength="4000"{{if .Required}} required{{end}}{{with .HelpID}} aria-describedby="{{.}}"{{end}} disabled></textarea>` +
		`{{else if eq .Type "choice"}}<select id="{{.ID}}" name="{{.Name}}"{{if .Required}} required{{end}}{{with .HelpID}} aria-describedby="{{.}}"{{end}} disabled><option value="">{{$.Choose}}</option>{{range .Choices}}<option value="{{.}}">{{.}}</option>{{end}}</select>` +
		`{{else if eq .Type "file"}}<input id="{{.ID}}" name="{{.Name}}" type="file"{{if .Required}} required{{end}}{{with .HelpID}} aria-describedby="{{.}}"{{end}} disabled>` +
		`{{else}}<input id="{{.ID}}" name="{{.Name}}" type="{{.Input}}"{{if .Text}} maxlength="4000"{{end}}{{if .LTR}} dir="ltr"{{end}}{{if eq .Type "number"}} step="any"{{end}}{{with .Auto}} autocomplete="{{.}}"{{end}}{{if .Required}} required{{end}}{{with .HelpID}} aria-describedby="{{.}}"{{end}} disabled>` +
		`{{end}}{{end}}` +
		`{{with .HelpID}}<p id="{{.}}" class="sf-help">{{$.HelpOf .}}</p>{{end}}` +
		`</div>{{end}}` +
		`<button type="submit" disabled>{{.Submit}}</button></form>`))

type draftFormView struct {
	Choose, Submit string
	Fields         []draftFieldView
	help           map[string]string
}

// HelpOf returns the help text of the paragraph id.
func (v draftFormView) HelpOf(id string) string { return v.help[id] }

type draftFieldView struct {
	ID, Name, Label, Type, Input, Auto, HelpID string
	Required, Text, LTR                        bool
	Choices                                    []string
}

// draftFormMarkup renders each form section of a draft, keyed by section
// ID, for the builder preview: site only knows published forms, so a form
// that exists only in the draft would otherwise preview as a bare heading.
// A section without fields gets no entry. t looks up builder catalog keys
// for the default button and choice prompt.
func draftFormMarkup(doc Document, t func(key string) string) map[string]string {
	out := map[string]string{}
	slugs := map[string]bool{}
	for _, s := range doc.Sections {
		if s.Kind != "form" || s.Form == nil || len(s.Form.Fields) == 0 {
			continue
		}
		f := s.Form
		// Element ids follow site's sf-{slug}-{name}; a missing or repeated
		// slug, or a missing name, falls back to the section ID or the index.
		prefix := "sf-" + s.ID + "-"
		if formSlugPattern.MatchString(f.Slug) && !slugs[f.Slug] {
			prefix = "sf-" + f.Slug + "-"
			slugs[f.Slug] = true
		}
		v := draftFormView{Choose: t("form_preview_choose"), Submit: strings.TrimSpace(f.SubmitLabel), help: map[string]string{}}
		if v.Submit == "" {
			v.Submit = t("form_preview_submit")
		}
		names := map[string]bool{}
		for i, fl := range f.Fields {
			name := fl.Name
			if !formFieldPattern.MatchString(name) || names[name] {
				name = "f" + strconv.Itoa(i)
			}
			names[name] = true
			fv := draftFieldView{ID: prefix + name, Name: fl.Name, Label: fl.Label, Type: fl.Type, Required: fl.Required}
			for _, c := range fl.Choices {
				if strings.TrimSpace(c) != "" {
					fv.Choices = append(fv.Choices, c)
				}
			}
			switch fl.Type {
			case "textarea", "choice", "checkbox", "file":
			case "email":
				fv.Input, fv.Text, fv.LTR, fv.Auto = "email", true, true, "email"
			case "number":
				fv.Input, fv.LTR = "number", true
			case "date":
				fv.Input = "date"
			default:
				fv.Type, fv.Input, fv.Text = "text", "text", true
			}
			if help := strings.TrimSpace(fl.Help); help != "" {
				fv.HelpID = fv.ID + "-help"
				v.help[fv.HelpID] = help
			}
			v.Fields = append(v.Fields, fv)
		}
		var b bytes.Buffer
		if err := draftFormTemplate.Execute(&b, v); err != nil {
			continue
		}
		out[s.ID] = b.String()
	}
	return out
}

// previewContext carries doc's draft forms, labelled in the page locale,
// into the theme's preview render.
func (h *adminHandlers) previewContext(r *http.Request, doc Document, locale string) context.Context {
	return theme.WithDraftForms(r.Context(), draftFormMarkup(doc, func(key string) string { return h.d.I18n.T(locale, "builder."+key) }))
}

// previewLocale is the page's locale, else the site's, else the default.
func previewLocale(d *kernel.Deps, ctx context.Context, item *content.Item) string {
	locale := item.Locale
	if locale == "" {
		locale = d.Settings.GetString(ctx, "site.locale", i18n.DefaultLocale)
	}
	if !slices.Contains(i18n.ContentLocales(ctx, d.Settings), locale) {
		locale = i18n.ContentLocale(ctx, d.Settings)
	}
	return locale
}
