package site

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"slices"
	"strings"

	"github.com/zadstack/gorun/internal/i18n"
)

// formTemplate is the markup of every builder form, on a builder page or
// through {{form}} in a theme. html/template escapes every value: labels,
// help, choices and the button text come from admins and are never markup.
// Native required/type attributes let the browser block and focus the first
// invalid field; server errors render through form-result (ruling R4).
var formTemplate = template.Must(template.New("form").Parse(
	`<form class="sb-form gp-form-fields" method="post" action="/forms/{{.Slug}}"{{if .Multipart}} enctype="multipart/form-data"{{end}}>` +
		`<input type="hidden" name="_csrf" value="{{.CSRF}}"><input type="hidden" name="_back" value="{{.Back}}">` +
		`<div class="sf-hp" hidden aria-hidden="true"><label for="{{.Prefix}}website">{{.Honeypot}}</label>` +
		`<input id="{{.Prefix}}website" name="website" type="text" tabindex="-1" autocomplete="off"></div>` +
		`{{range .Fields}}<div class="sf-field sf-{{.Type}}">` +
		`{{if eq .Type "checkbox"}}` +
		`<input id="{{.ID}}" name="{{.Name}}" type="checkbox" value="yes"{{if .Required}} required{{end}}{{with .HelpID}} aria-describedby="{{.}}"{{end}}>` +
		`<label for="{{.ID}}">{{.Label}}</label>` +
		`{{else}}` +
		`<label for="{{.ID}}">{{.Label}}</label>` +
		`{{if eq .Type "textarea"}}<textarea id="{{.ID}}" name="{{.Name}}" rows="5" maxlength="4000"{{if .Required}} required{{end}}{{with .HelpID}} aria-describedby="{{.}}"{{end}}></textarea>` +
		`{{else if eq .Type "choice"}}<select id="{{.ID}}" name="{{.Name}}"{{if .Required}} required{{end}}{{with .HelpID}} aria-describedby="{{.}}"{{end}}><option value="">{{$.Choose}}</option>{{range .Choices}}<option value="{{.}}">{{.}}</option>{{end}}</select>` +
		`{{else if eq .Type "file"}}<input id="{{.ID}}" name="{{.Name}}" type="file" accept="{{$.Accept}}"{{if .Required}} required{{end}}{{with .HelpID}} aria-describedby="{{.}}"{{end}}>` +
		`{{else}}<input id="{{.ID}}" name="{{.Name}}" type="{{.Input}}"{{if .Text}} maxlength="4000"{{end}}{{if .LTR}} dir="ltr"{{end}}{{if eq .Type "number"}} step="any"{{end}}{{with .Auto}} autocomplete="{{.}}"{{end}}{{if .Required}} required{{end}}{{with .HelpID}} aria-describedby="{{.}}"{{end}}>` +
		`{{end}}{{end}}` +
		`{{with .HelpID}}<p id="{{.}}" class="sf-help">{{$.HelpOf .}}</p>{{end}}` +
		`</div>{{end}}` +
		`<button type="submit">{{.Submit}}</button></form>`))

type formView struct {
	Slug, CSRF, Back, Prefix string
	Honeypot, Choose, Submit string
	Accept                   string
	Multipart                bool
	Fields                   []formFieldView
	help                     map[string]string
}

// HelpOf returns the help text of the paragraph id.
func (v formView) HelpOf(id string) string { return v.help[id] }

type formFieldView struct {
	ID, Name, Label, Type, Input, Auto, HelpID string
	Required, Text, LTR                        bool
	Choices                                    []string
}

// renderForm is the "form" theme func (ruling R3): the open builder form
// slug as markup posting to /forms/{slug}, with csrf and back in hidden
// fields. locale (the render's; the site locale when absent) picks the
// default button label and hints. An unknown, closed or captured form, and
// any error, renders nothing. The file field shows only while attachments
// are on.
func (h *handlers) renderForm(slug, csrf, back string, locale ...string) template.HTML {
	if !formSlugRe.MatchString(slug) || slices.Contains(reservedFormSlugs, slug) {
		return ""
	}
	ctx := context.Background()
	def, err := h.formBySlug(ctx, slug)
	if err != nil {
		if !errors.Is(err, errNoForm) {
			h.d.Log.Error("site: render form", "form", slug, "err", err)
		}
		return ""
	}
	if def.Source != formBuilder || !def.Open {
		return ""
	}
	loc := ""
	if len(locale) > 0 && slices.Contains(i18n.ContentLocales(ctx, h.d.Settings), locale[0]) {
		loc = locale[0]
	} else {
		loc = h.siteLocaleOf(ctx)
	}
	t := func(key string, args ...any) string { return h.d.I18n.T(loc, key, args...) }
	if !localPath(back) {
		back = "/"
	}
	cfg := h.formConfig(ctx)
	prefix := "sf-" + slug + "-"
	v := formView{Slug: slug, CSRF: csrf, Back: back, Prefix: prefix, Honeypot: t("site.forms.honeypot"), Choose: t("site.forms.choose"),
		Submit: strings.TrimSpace(def.SubmitLabel), Accept: uploadAccept, help: map[string]string{}}
	if v.Submit == "" {
		v.Submit = t("site.forms.submit")
	}
	for _, f := range def.Fields {
		if f.State != statePinned || (f.Type == fieldFile && !cfg.Attachments) {
			continue
		}
		fv := formFieldView{ID: prefix + f.Name, Name: f.Name, Label: f.Label, Type: f.Type, Required: f.Required, Choices: f.Choices}
		switch f.Type {
		case fieldEmail:
			fv.Input, fv.Text, fv.LTR, fv.Auto = "email", true, true, "email"
		case fieldNumber:
			fv.Input, fv.LTR = "number", true
		case fieldDate:
			fv.Input = "date"
		case fieldFile:
			v.Multipart = true
		default:
			fv.Input, fv.Text = "text", true
		}
		help := strings.TrimSpace(f.Help)
		if f.Type == fieldFile {
			help = strings.TrimSpace(help + " " + t("site.forms.file_help", cfg.MaxMB))
		}
		if help != "" {
			fv.HelpID = fv.ID + "-help"
			v.help[fv.HelpID] = help
		}
		v.Fields = append(v.Fields, fv)
	}
	if len(v.Fields) == 0 {
		return ""
	}
	var out bytes.Buffer
	if err := formTemplate.Execute(&out, v); err != nil {
		h.d.Log.Error("site: render form", "form", slug, "err", err)
		return ""
	}
	return template.HTML(out.String())
}
