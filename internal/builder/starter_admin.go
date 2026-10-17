package builder

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

type starterReviewPage struct{ ID, Title, Preview string }
type starterView struct {
	Form      starterForm
	Choices   []starterChoice
	Run       *starterRun
	Pages     []starterReviewPage
	Expected  string
	Logo      identityMedia
	HeroImage identityMedia
	Error     string
}

func registerStarters(d *kernel.Deps, s *service) error {
	if _, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates); !ok {
		return errors.New("builder: admin templates unavailable")
	}
	h := &starterHandler{d: d, svc: s}
	r := d.Router.Admin.With(auth.RequirePerm("settings.manage"), auth.RequirePerm("content.create"), auth.RequirePerm("content.edit"), auth.RequirePerm("content.publish"), auth.RequirePerm("menus.manage"))
	d.Admin.AddNav(kernel.NavItem{ID: "site-builder", LabelKey: "starters.title", Icon: "panels-top-left", Href: "/admin/site-builder", Permission: "settings.manage", Group: "appearance", Position: 1})
	r.Get("/site-builder", h.home)
	r.Post("/site-builder", h.submit)
	r.Post("/site-builder/{token}/activate", h.publish)
	return nil
}
func (h *starterHandler) render(w http.ResponseWriter, r *http.Request, status int, data starterView) {
	ar := i18n.Locale(r.Context()) == "ar"
	data.Choices = starterChoices(ar)
	logoLabel, heroLabel := "Logo (optional)", "Hero image (optional)"
	if ar {
		logoLabel, heroLabel = "الشعار (اختياري)", "الصورة الرئيسية (اختيارية)"
	}
	data.Logo = identityMedia{Name: "logo", ID: "starter-logo", Value: data.Form.Logo, Label: logoLabel, CanPick: auth.Can(r.Context(), "media.upload")}
	data.HeroImage = identityMedia{Name: "hero_image", ID: "starter-hero-image", Value: data.Form.HeroImage, Label: heroLabel, CanPick: auth.Can(r.Context(), "media.upload")}
	tpl, _ := kernel.Get[*httpx.Templates](h.d, kernel.ServiceAdminTemplates)
	tpl.RenderWith(w, r, status, "admin", "pages/builder/starter.html", data)
}
func (h *starterHandler) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, content.ErrForbidden) {
		http.Error(w, "Forbidden", 403)
		return
	}
	h.d.Log.Error("starter workflow", "err", err)
	http.Error(w, "Unable to prepare site. Retry from Site builder.", 500)
}
func (h *starterHandler) home(w http.ResponseWriter, r *http.Request) {
	if token := r.URL.Query().Get("run"); token != "" {
		run, err := h.run(r.Context(), token)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		h.review(w, r, run)
		return
	}
	run, err := h.issue(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, 200, starterView{Form: starterForm{Token: run.Token, Starter: "company", Locale: i18n.Locale(r.Context()), Accent: starterDesign("company").Identity.Accent, Background: starterDesign("company").Identity.Background, Text: starterDesign("company").Identity.Text, Font: starterDesign("company").Identity.Font, Width: starterDesign("company").Identity.Width}})
}
func (h *starterHandler) submit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var f starterForm
	if err := httpx.Bind(r, &f); err != nil {
		http.Error(w, "Invalid form", 400)
		return
	}
	if err := validateStarter(f); err != nil {
		h.render(w, r, 422, starterView{Form: f, Error: identityError(h.d, r.Context(), err)})
		return
	}
	run, err := h.create(r.Context(), f)
	if err != nil {
		if errors.Is(err, ErrProfile) {
			h.render(w, r, 422, starterView{Form: f, Error: profileMessage(r.Context(), err)})
			return
		}
		if errors.Is(err, content.ErrForbidden) || errors.Is(err, content.ErrNotFound) {
			h.fail(w, r, err)
			return
		}
		h.d.Log.Error("starter preparation", "err", err)
		message := "Preparation interrupted. Retry this form to resume your existing draft pages."
		if i18n.Locale(r.Context()) == "ar" {
			message = "توقف تجهيز الموقع. أعد إرسال هذا النموذج لاستكمال صفحات المسودة الحالية."
		}
		h.render(w, r, 500, starterView{Form: f, Error: message})
		return
	}
	http.Redirect(w, r, "/admin/site-builder?run="+run.Token, http.StatusSeeOther)
}
func (h *starterHandler) review(w http.ResponseWriter, r *http.Request, run *starterRun) {
	h.reviewStatus(w, r, run, 200, "")
}
func (h *starterHandler) reviewStatus(w http.ResponseWriter, r *http.Request, run *starterRun, status int, message string) {
	cs, ok := kernel.Get[content.Service](h.d, kernel.ServiceContent)
	if !ok {
		http.Error(w, "Content unavailable", 500)
		return
	}
	data := starterView{Run: run, Error: message}
	expected := []starterExpectation{}
	for _, page := range starterPages(run.Kind, run.Title, run.Tagline, run.Locale) {
		id := run.Pages[page.Slug]
		if id == "" {
			continue
		}
		it, err := cs.Get(r.Context(), id)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		draft, err := h.svc.Load(r.Context(), id)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		data.Pages = append(data.Pages, starterReviewPage{ID: id, Title: it.Title, Preview: "/admin/builder/" + id + "#builder-preview"})
		expected = append(expected, starterExpectation{ID: id, UpdatedAt: content.EditToken(it), DocumentUpdatedAt: draft.UpdatedAt})
	}
	raw, err := json.Marshal(expected)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data.Expected = string(raw)
	h.render(w, r, status, data)
}
func (h *starterHandler) publish(w http.ResponseWriter, r *http.Request) {
	var form struct {
		Expected string `form:"expected"`
	}
	if err := httpx.Bind(r, &form); err != nil {
		http.Error(w, "Invalid form", 400)
		return
	}
	var expected []starterExpectation
	if err := json.Unmarshal([]byte(form.Expected), &expected); err != nil {
		http.Error(w, "Invalid review", 400)
		return
	}
	if err := h.activateReviewed(r.Context(), chi.URLParam(r, "token"), expected); err != nil {
		if errors.Is(err, ErrProfile) {
			run, loadErr := h.run(r.Context(), chi.URLParam(r, "token"))
			if loadErr != nil {
				h.fail(w, r, loadErr)
				return
			}
			message := profileMessage(r.Context(), err)
			if i18n.Locale(r.Context()) == "ar" {
				message += " الصورة المختارة لم تعد متاحة. جهّز مسودة موقع جديدة بالصور الصحيحة."
			} else {
				message += " The selected image is no longer available. Prepare a new site draft with available images."
			}
			h.reviewStatus(w, r, run, 422, message)
			return
		}
		if errors.Is(err, content.ErrPublishConflict) || errors.Is(err, ErrConflict) {
			http.Error(w, "Pages changed since review. Reload the starter review and publish again.", 409)
			return
		}
		h.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/site-builder?run="+chi.URLParam(r, "token"), http.StatusSeeOther)
}
