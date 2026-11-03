package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// HookFormsPublished is emitted (hooks.Emit, after the publish commits) on
// every publish of a builder page with a FormsPublished payload. The site
// module subscribes by this name and stores the page's forms; Forms is empty
// when the page has none, which closes the forms it had before.
const HookFormsPublished = "builder.forms.published"

const (
	maxFormSections   = 10 // per page
	maxFormFields     = 30 // per form
	maxFormChoices    = 20 // per choice field
	maxFormTitleRunes = 200
	maxFormLabelRunes = 200 // field labels, choices and the submit label
	maxFormHelpRunes  = 500
	maxFormSuccess    = 1000
	maxFormSlugBytes  = 40
)

var (
	formSlugPattern  = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
	formFieldPattern = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)
	formFieldTypes   = []string{"text", "textarea", "email", "number", "date", "choice", "checkbox", "file"}
	// Slugs served by the site's own /forms/* routes.
	reservedFormSlugs = []string{"contact", "comment"}
)

// FormSpec is a form section's settings. The section's Title and Text are
// the form's heading and intro.
type FormSpec struct {
	Slug        string          `json:"slug"`
	SubmitLabel string          `json:"submit_label"`
	Success     string          `json:"success"`
	Fields      []FormFieldSpec `json:"fields"`
}

// FormFieldSpec is one field of a form section.
type FormFieldSpec struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Help     string   `json:"help"`
	Choices  []string `json:"choices"`
}

// PublishedForm is one visible form section as announced to the site.
type PublishedForm struct {
	Slug        string          `json:"slug"`
	Title       string          `json:"title"`
	SubmitLabel string          `json:"submit_label"`
	Success     string          `json:"success"`
	Fields      []FormFieldSpec `json:"fields"`
}

// FormsPublished is the HookFormsPublished payload. PublishedAt
// (RFC3339Nano, set when the event is emitted after the commit) lets the
// subscriber ignore an older publish delivered out of order.
type FormsPublished struct {
	PageID      string          `json:"page_id"`
	PublishedAt string          `json:"published_at"`
	Forms       []PublishedForm `json:"forms"`
}

// emitFormsPublished announces ev; call it only after the publish commits.
func (s *service) emitFormsPublished(ctx context.Context, ev FormsPublished) {
	if s.d.Hooks == nil {
		return
	}
	ev.PublishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.d.Hooks.Emit(ctx, HookFormsPublished, ev)
}

// FormSlugTakenError rejects a publish whose form slug another published
// builder page already uses.
type FormSlugTakenError struct{ Slug string }

func (e *FormSlugTakenError) Error() string {
	return fmt.Sprintf("builder: form slug %q is used by another published page", e.Slug)
}

// SlugFromTitle derives a form slug from a title: lowercase ASCII letters
// and digits, other runs joined by "-", at most 40 bytes. A title with no
// ASCII letter or digit (an Arabic title, say) gives "", and the editor
// asks for a slug.
func SlugFromTitle(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
			continue
		}
		dash = true
	}
	slug := b.String()
	if len(slug) > maxFormSlugBytes {
		slug = strings.TrimRight(slug[:maxFormSlugBytes], "-")
	}
	return slug
}

func defaultForm(s *Section, locale string) {
	if locale == "ar" {
		s.Text = "املأ النموذج وسنتواصل معك."
		s.Form = &FormSpec{Slug: SlugFromTitle(s.Title), SubmitLabel: "إرسال", Success: "شكرًا، وصلتنا رسالتك.", Fields: []FormFieldSpec{
			{Name: "name", Label: "الاسم", Type: "text", Required: true},
			{Name: "email", Label: "البريد الإلكتروني", Type: "email", Required: true},
			{Name: "message", Label: "الرسالة", Type: "textarea"},
		}}
		return
	}
	s.Text = "Fill in the form and we will get back to you."
	s.Form = &FormSpec{Slug: SlugFromTitle(s.Title), SubmitLabel: "Send", Success: "Thanks, we received your message.", Fields: []FormFieldSpec{
		{Name: "name", Label: "Name", Type: "text", Required: true},
		{Name: "email", Label: "Email", Type: "email", Required: true},
		{Name: "message", Label: "Message", Type: "textarea"},
	}}
}

func tooLong(v string, runes int) bool { return utf8.RuneCountInString(v) > runes }

// validateFormBounds holds for drafts too: sizes only, so an unfinished
// form (no slug or fields yet) can be saved.
func validateFormBounds(doc Document) error {
	forms := 0
	for _, s := range doc.Sections {
		if s.Kind != "form" {
			if s.Form != nil {
				return errors.New("builder: form settings on a non-form section")
			}
			continue
		}
		if forms++; forms > maxFormSections {
			return fmt.Errorf("builder: at most %d form sections per page", maxFormSections)
		}
		if tooLong(s.Title, maxFormTitleRunes) {
			return fmt.Errorf("builder: form title longer than %d characters", maxFormTitleRunes)
		}
		f := s.Form
		if f == nil {
			continue
		}
		if len(f.Slug) > maxFormSlugBytes || tooLong(f.SubmitLabel, maxFormLabelRunes) || tooLong(f.Success, maxFormSuccess) {
			return errors.New("builder: form setting too long")
		}
		if len(f.Fields) > maxFormFields {
			return fmt.Errorf("builder: at most %d form fields", maxFormFields)
		}
		for _, fl := range f.Fields {
			if len(fl.Name) > 40 || len(fl.Type) > 20 || tooLong(fl.Label, maxFormLabelRunes) || tooLong(fl.Help, maxFormHelpRunes) {
				return errors.New("builder: form field setting too long")
			}
			if len(fl.Choices) > maxFormChoices {
				return fmt.Errorf("builder: at most %d choices per field", maxFormChoices)
			}
			for _, c := range fl.Choices {
				if tooLong(c, maxFormLabelRunes) {
					return fmt.Errorf("builder: choice longer than %d characters", maxFormLabelRunes)
				}
			}
		}
	}
	return nil
}

// validateFormsForPublish checks every visible form section is complete.
func validateFormsForPublish(doc Document) error {
	slugs := map[string]bool{}
	for _, s := range doc.Sections {
		if s.Kind != "form" || s.Hidden {
			continue
		}
		f := s.Form
		if f == nil || len(f.Fields) == 0 {
			return errors.New("builder: a form needs at least one field")
		}
		if !formSlugPattern.MatchString(f.Slug) || slices.Contains(reservedFormSlugs, f.Slug) {
			return fmt.Errorf("builder: invalid form slug %q", f.Slug)
		}
		if slugs[f.Slug] {
			return fmt.Errorf("builder: form slug %q used twice on the page", f.Slug)
		}
		slugs[f.Slug] = true
		names := map[string]bool{}
		for _, fl := range f.Fields {
			if !formFieldPattern.MatchString(fl.Name) || strings.HasPrefix(fl.Name, "_") || fl.Name == "website" {
				return fmt.Errorf("builder: invalid form field name %q", fl.Name)
			}
			if names[fl.Name] {
				return fmt.Errorf("builder: form field %q used twice", fl.Name)
			}
			names[fl.Name] = true
			if !slices.Contains(formFieldTypes, fl.Type) {
				return fmt.Errorf("builder: unknown form field type %q", fl.Type)
			}
			if fl.Type == "choice" {
				if len(fl.Choices) == 0 {
					return fmt.Errorf("builder: choice field %q needs choices", fl.Name)
				}
				for _, c := range fl.Choices {
					if strings.TrimSpace(c) == "" {
						return fmt.Errorf("builder: choice field %q has an empty choice", fl.Name)
					}
				}
			}
		}
	}
	return nil
}

// publishedForms lists the visible form sections of doc, never nil.
func publishedForms(doc Document) []PublishedForm {
	out := []PublishedForm{}
	for _, s := range doc.Sections {
		if s.Kind != "form" || s.Hidden || s.Form == nil {
			continue
		}
		out = append(out, PublishedForm{Slug: s.Form.Slug, Title: strings.TrimSpace(s.Title), SubmitLabel: s.Form.SubmitLabel, Success: s.Form.Success, Fields: s.Form.Fields})
	}
	return out
}

// checkFormSlugsFree rejects a slug that another page's published document
// uses (ruling R5). Pages whose content was trashed still hold their slugs:
// their site forms stay registered until the page is deleted.
func (s *service) checkFormSlugsFree(ctx context.Context, id string, forms []PublishedForm) error {
	if len(forms) == 0 {
		return nil
	}
	rows, err := s.d.DB.QueryContext(ctx, `SELECT published_document_json FROM builder_documents WHERE content_id <> ? AND published_document_json LIKE '%"kind":"form"%'`, id)
	if err != nil {
		return fmt.Errorf("builder: form slugs: %w", err)
	}
	defer rows.Close()
	taken := map[string]bool{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		var doc Document
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			s.d.Log.Warn("builder: form slugs: unreadable published document", "err", err)
			continue
		}
		for _, f := range publishedForms(doc) {
			taken[f.Slug] = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, f := range forms {
		if taken[f.Slug] {
			return &FormSlugTakenError{Slug: f.Slug}
		}
	}
	return nil
}
