package content

import (
	"context"
	"errors"
	"net/http"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
)

// translationView is one supported locale in the editor's translations panel.
type translationView struct {
	Locale    string
	Current   bool   // the item being edited
	EditURL   string // the existing translation ("" when the locale has none)
	Title     string
	Status    Status
	CanCreate bool // missing, and content.create is held
}

// translationViews lists the supported locales with the item's translations, and whether the
// item can leave its group.
func (h *adminHandlers) translationViews(ctx context.Context, base string, it *Item) ([]translationView, bool) {
	members, err := h.svc.Translations(ctx, it.ID)
	if err != nil {
		h.d.Log.Error("content admin: translations", "id", it.ID, "err", err)
		return nil, false
	}
	byLocale := map[string]Item{}
	for _, m := range members {
		byLocale[m.Locale] = m
	}
	canCreate := auth.Can(ctx, "content.create")
	out := make([]translationView, 0, len(i18n.Supported))
	for _, loc := range i18n.Supported {
		v := translationView{Locale: loc, Current: loc == it.Locale}
		if m, ok := byLocale[loc]; ok && !v.Current {
			v.EditURL, v.Title, v.Status = base+"/"+m.ID, m.Title, m.Status
		}
		v.CanCreate = !v.Current && v.EditURL == "" && canCreate
		out = append(out, v)
	}
	return out, it.TranslationGroup != "" && auth.Can(ctx, "content.edit")
}

// translateItem copies the item into the posted locale and opens the new draft.
func (h *adminHandlers) translateItem(w http.ResponseWriter, r *http.Request) {
	t, it, ok := h.itemFromURL(w, r)
	if !ok {
		return
	}
	tr, err := h.svc.CreateTranslation(r.Context(), it.ID, r.PostFormValue("locale"))
	if err != nil {
		h.translationFailed(w, r, err, "/admin/content/"+t.Slug+"/"+it.ID)
		return
	}
	httpx.SetFlash(w, "success", "content.flash.translated")
	http.Redirect(w, r, "/admin/content/"+t.Slug+"/"+tr.ID, http.StatusSeeOther)
}

// unlinkItem takes the item out of its translations.
func (h *adminHandlers) unlinkItem(w http.ResponseWriter, r *http.Request) {
	t, it, ok := h.itemFromURL(w, r)
	if !ok {
		return
	}
	back := "/admin/content/" + t.Slug + "/" + it.ID
	if err := h.svc.UnlinkTranslation(r.Context(), it.ID); err != nil {
		h.translationFailed(w, r, err, back)
		return
	}
	httpx.SetFlash(w, "success", "content.flash.unlinked")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// translationFailed flashes a refused translation operation back on the editor.
func (h *adminHandlers) translationFailed(w http.ResponseWriter, r *http.Request, err error, back string) {
	var ve *ValidationError
	if !errors.As(err, &ve) {
		h.fail(w, r, err)
		return
	}
	msg := "content.flash.invalid"
	for _, m := range ve.Fields {
		msg = m
		break
	}
	httpx.SetFlash(w, "error", msg)
	http.Redirect(w, r, back, http.StatusSeeOther)
}
