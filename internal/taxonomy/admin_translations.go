package taxonomy

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/httpx"
)

// termTranslationView is one content language in the term editor's translations panel.
type termTranslationView struct {
	Locale  string
	Current bool   // the term being edited
	ID      string // the linked term in that language ("" when there is none)
	Name    string
	EditURL string
}

// translationPanel lists the content languages with edit's translations, and the terms of the
// taxonomy that can be linked to it (other languages, not already in its group).
func (h *adminHandlers) translationPanel(ctx context.Context, tax *Taxonomy, edit Term, locales []string) ([]termTranslationView, []choice) {
	base := "/admin/taxonomies/" + url.PathEscape(tax.Slug)
	members, err := h.svc.TermTranslations(ctx, edit.ID)
	if err != nil {
		h.d.Log.Error("taxonomy admin: translations", "id", edit.ID, "err", err)
		return nil, nil
	}
	byLocale := map[string]Term{}
	inGroup := map[string]bool{}
	for _, m := range members {
		byLocale[m.Locale] = m
		inGroup[m.ID] = true
	}
	views := make([]termTranslationView, 0, len(locales))
	for _, loc := range locales {
		v := termTranslationView{Locale: loc, Current: loc == edit.Locale}
		if m, ok := byLocale[loc]; ok && !v.Current {
			v.ID, v.Name, v.EditURL = m.ID, m.Name, base+"?edit="+url.QueryEscape(m.ID)
		}
		views = append(views, v)
	}
	all, _, err := h.svc.Terms(ctx, tax.Slug, TermQuery{PerPage: maxPerPage})
	if err != nil {
		return views, nil
	}
	var choices []choice
	for _, t := range all {
		if inGroup[t.ID] || t.Locale == edit.Locale {
			continue
		}
		if _, taken := byLocale[t.Locale]; taken {
			continue
		}
		choices = append(choices, choice{Value: t.ID, Label: t.Name + " (" + t.Locale + ")"})
	}
	return views, choices
}

// termFromURL loads {id} and checks it belongs to {tax}.
func (h *adminHandlers) termFromURL(w http.ResponseWriter, r *http.Request) (*Term, bool) {
	t, err := h.svc.GetTerm(r.Context(), chi.URLParam(r, "id"))
	if err == nil && t.Taxonomy != chi.URLParam(r, "tax") {
		err = ErrNotFound
	}
	if err != nil {
		h.fail(w, r, err)
		return nil, false
	}
	return t, true
}

func termEditURL(t *Term) string {
	return "/admin/taxonomies/" + url.PathEscape(t.Taxonomy) + "?edit=" + url.QueryEscape(t.ID)
}

// translateTerm creates the posted language's translation and opens it.
func (h *adminHandlers) translateTerm(w http.ResponseWriter, r *http.Request) {
	src, ok := h.termFromURL(w, r)
	if !ok {
		return
	}
	tr, err := h.svc.CreateTermTranslation(r.Context(), src.ID, Term{Locale: r.PostFormValue("locale"),
		Name: r.PostFormValue("name"), Slug: strings.TrimSpace(r.PostFormValue("slug"))})
	if err != nil {
		h.translationFailed(w, r, err, termEditURL(src))
		return
	}
	httpx.SetFlash(w, "success", "taxonomy.flash.translated")
	http.Redirect(w, r, termEditURL(tr), http.StatusSeeOther)
}

// linkTerm links the posted term (and its translations) to the term's translations.
func (h *adminHandlers) linkTerm(w http.ResponseWriter, r *http.Request) {
	t, ok := h.termFromURL(w, r)
	if !ok {
		return
	}
	if err := h.svc.LinkTermTranslation(r.Context(), t.ID, r.PostFormValue("term_id")); err != nil {
		h.translationFailed(w, r, err, termEditURL(t))
		return
	}
	httpx.SetFlash(w, "success", "taxonomy.flash.linked")
	http.Redirect(w, r, termEditURL(t), http.StatusSeeOther)
}

// unlinkTerm takes the term out of its translations.
func (h *adminHandlers) unlinkTerm(w http.ResponseWriter, r *http.Request) {
	t, ok := h.termFromURL(w, r)
	if !ok {
		return
	}
	if err := h.svc.UnlinkTermTranslation(r.Context(), t.ID); err != nil {
		h.translationFailed(w, r, err, termEditURL(t))
		return
	}
	httpx.SetFlash(w, "success", "taxonomy.flash.unlinked")
	http.Redirect(w, r, termEditURL(t), http.StatusSeeOther)
}

// translationFailed flashes a refused translation operation back on the term editor.
func (h *adminHandlers) translationFailed(w http.ResponseWriter, r *http.Request, err error, back string) {
	var ve *content.ValidationError
	if errors.Is(err, ErrNotFound) { // the posted term to link is gone
		ve = &content.ValidationError{Fields: content.FieldErrors{"term_id": "taxonomy.validation.unknown_term"}}
	} else if !errors.As(err, &ve) {
		h.fail(w, r, err)
		return
	}
	msg := "taxonomy.flash.invalid"
	for _, m := range ve.Fields {
		msg = m
		break
	}
	httpx.SetFlash(w, "error", msg)
	http.Redirect(w, r, back, http.StatusSeeOther)
}
