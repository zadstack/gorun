package mailer

import (
	"bytes"
	"embed"
	"fmt"
	htmltemplate "html/template"
	"io/fs"
	"strings"
	texttemplate "text/template"
)

//go:embed templates/*
var templatesFS embed.FS

const fallbackLocale = "en"

// Render executes templates/<name>.<locale>.html (html/template) and
// templates/<name>.<locale>.txt (text/template; it must define "subject"),
// falling back to the English templates when the locale has none.
func (m *Mailer) Render(locale, name string, data any) (subject, html, text string, err error) {
	loc := locale
	if _, statErr := fs.Stat(templatesFS, "templates/"+name+"."+loc+".txt"); statErr != nil {
		loc = fallbackLocale
	}
	base := "templates/" + name + "." + loc
	txt, err := texttemplate.ParseFS(templatesFS, base+".txt")
	if err != nil {
		return "", "", "", fmt.Errorf("mailer: template %s: %w", name, err)
	}
	htm, err := htmltemplate.ParseFS(templatesFS, base+".html")
	if err != nil {
		return "", "", "", fmt.Errorf("mailer: template %s: %w", name, err)
	}
	var sb, tb, hb bytes.Buffer
	if err := txt.ExecuteTemplate(&sb, "subject", data); err != nil {
		return "", "", "", fmt.Errorf("mailer: %s subject: %w", name, err)
	}
	if err := txt.Execute(&tb, data); err != nil {
		return "", "", "", fmt.Errorf("mailer: %s text: %w", name, err)
	}
	if err := htm.Execute(&hb, data); err != nil {
		return "", "", "", fmt.Errorf("mailer: %s html: %w", name, err)
	}
	return strings.TrimSpace(sb.String()), hb.String(), strings.TrimSpace(tb.String()) + "\n", nil
}
