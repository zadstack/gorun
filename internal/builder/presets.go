package builder

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/theme"
)

// A purpose preset is the editable section structure for an about, service,
// methodology or contact page. It invents nothing: the hero carries the page
// title the author typed, the contact block renders the site's real contact
// form, and every other section starts empty and hidden with only a
// structural heading, so nothing unfinished reaches visitors until the author
// writes it and shows the section.

type presetSection struct {
	kind    string
	native  bool
	heading [2]string // en, ar
	visible bool
}

var purposePresets = map[string][]presetSection{
	"about": {
		{kind: "text", heading: [2]string{"Our story", "قصتنا"}},
		{kind: "features", native: true, heading: [2]string{"What sets us apart", "ما يميزنا"}},
		{kind: "cta", heading: [2]string{"Work with us", "اعمل معنا"}},
	},
	"service": {
		{kind: "features", native: true, heading: [2]string{"What is included", "ما تشمله الخدمة"}},
		{kind: "steps", native: true, heading: [2]string{"How it works", "كيف تعمل"}},
		{kind: "faq", native: true, heading: [2]string{"Questions", "أسئلة شائعة"}},
		{kind: "cta", heading: [2]string{"Request this service", "اطلب الخدمة"}},
	},
	"methodology": {
		{kind: "steps", native: true, heading: [2]string{"Our process", "مراحل العمل"}},
		{kind: "text", heading: [2]string{"Our principles", "مبادئنا"}},
		{kind: "faq", native: true, heading: [2]string{"Questions", "أسئلة شائعة"}},
	},
	"contact": {
		{kind: "contact", heading: [2]string{"Write to us", "راسلنا"}, visible: true},
		{kind: "text", heading: [2]string{"When we reply", "متى نرد"}},
	},
}

// ErrPreset reports an invalid purpose page request.
var ErrPreset = errors.New("builder: invalid purpose page")

// PurposePreset returns the layout for a new page with purpose and title.
func PurposePreset(purpose, title, locale string) (Document, error) {
	specs, ok := purposePresets[purpose]
	title = strings.TrimSpace(title)
	if !ok || !slices.Contains(theme.PagePurposes, purpose) || title == "" || utf8.RuneCountInString(title) > 200 {
		return Document{}, ErrPreset
	}
	lang := 0
	if locale == "ar" {
		lang = 1
	}
	doc := Document{Version: 1, Sections: []Section{{Library: SectionLibrary, ID: db.NewID(), Kind: "hero", Variant: "default", Title: title, Items: []Entry{}}}}
	for _, p := range specs {
		s := Section{ID: db.NewID(), Kind: p.kind, Variant: "default", Title: p.heading[lang], Items: []Entry{}, Hidden: !p.visible}
		if p.native {
			s.Library = SectionLibrary
		}
		doc.Sections = append(doc.Sections, s)
	}
	return doc, Validate(doc)
}

// PurposePageInput asks for a new draft builder page.
type PurposePageInput struct {
	Purpose string `json:"purpose" enum:"about,service,methodology,contact" doc:"What the page is for; stored in the page's purpose field."`
	Title   string `json:"title" doc:"The page title, 1 to 200 characters; it becomes the hero heading."`
	Locale  string `json:"locale,omitempty" enum:",ar,en" doc:"The page language; empty for the site language."`
}

// PurposePage is a created draft page and its builder draft.
type PurposePage struct {
	ID      string `json:"id" doc:"The new page's content ID"`
	EditURL string `json:"edit_url" doc:"The builder editor for the page"`
	Draft   *Draft `json:"draft"`
}

// CreatePurposePage creates a private draft page with purpose in, and saves its
// preset layout as the builder draft. Nothing is published.
func (s *service) CreatePurposePage(ctx context.Context, in PurposePageInput) (*PurposePage, error) {
	if !auth.Can(ctx, "content.create") || !auth.Can(ctx, "content.edit") {
		return nil, ErrForbidden
	}
	if in.Locale != "" && !slices.Contains(i18n.Supported, in.Locale) {
		return nil, ErrPreset
	}
	locale := in.Locale
	if locale == "" {
		locale = s.d.Settings.GetString(ctx, "site.locale", "ar")
	}
	doc, err := PurposePreset(in.Purpose, in.Title, locale)
	if err != nil {
		return nil, err
	}
	cs, err := s.content()
	if err != nil {
		return nil, err
	}
	body, err := Render(doc)
	if err != nil {
		return nil, err
	}
	it, err := cs.Create(ctx, content.Input{Type: "page", Title: strings.TrimSpace(in.Title), Status: "draft", Locale: in.Locale, Template: "builder",
		Body: body, Fields: map[string]any{"purpose": in.Purpose}})
	if err != nil {
		return nil, err
	}
	draft, err := s.Save(ctx, it.ID, doc, content.EditToken(it))
	if err != nil {
		return nil, err
	}
	return &PurposePage{ID: it.ID, EditURL: "/admin/builder/" + it.ID, Draft: draft}, nil
}

// createPurposePage handles the admin form on the pages list: it creates the
// draft and opens it in the builder.
func (h *adminHandlers) createPurposePage(w http.ResponseWriter, r *http.Request) {
	page, err := h.svc.CreatePurposePage(r.Context(), PurposePageInput{Purpose: r.PostFormValue("purpose"), Title: r.PostFormValue("title"), Locale: r.PostFormValue("locale")})
	if err != nil {
		h.d.Log.Warn("builder purpose page rejected", "error", err)
		httpx.SetFlash(w, "error", "builder.preset_error")
		http.Redirect(w, r, "/admin/content/page", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, page.EditURL, http.StatusSeeOther)
}
