package builder

import (
	"context"
	"errors"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings"
)

// ProvisionInput selects a complete editable website for an empty tenant.
type ProvisionInput struct{ Title, Template, Locale, Email string }

// ProvisionStudio resumes its own interrupted import, never adopts an existing site.
func ProvisionStudio(ctx context.Context, d *kernel.Deps, in ProvisionInput) error {
	kind := in.Template
	if kind == "editorial" {
		kind = "magazine"
	}
	if kind != "company" && kind != "portfolio" && kind != "magazine" {
		return errors.New("unsupported site template")
	}
	if !starterAllowed(ctx) {
		return content.ErrForbidden
	}
	design := starterDesign(kind)
	form := starterForm{Starter: kind, Title: in.Title, Locale: in.Locale, ContactEmail: in.Email, Accent: design.Identity.Accent, Background: design.Identity.Background, Text: design.Identity.Text, Font: design.Identity.Font, Width: design.Identity.Width}
	if err := validateStarter(form); err != nil {
		return err
	}
	cs, ok := kernel.Get[content.Service](d, kernel.ServiceContent)
	if !ok {
		return errors.New("content service unavailable")
	}
	h := &starterHandler{d: d, svc: &service{d: d}}
	var saved struct {
		Token string
		Input ProvisionInput
	}
	err := d.Settings.Get(ctx, "builder.provision", &saved)
	if errors.Is(err, settings.ErrNotFound) {
		existing, e := cs.Query(ctx, content.Query{PerPage: 1, IncludeTrash: true})
		if e != nil {
			return e
		}
		if existing.Total != 0 {
			return errors.New("tenant already has content")
		}
		run, e := h.issue(ctx)
		if e != nil {
			return e
		}
		saved.Token, saved.Input = run.Token, in
		if e = d.Settings.Set(ctx, "builder.provision", saved); e != nil {
			return e
		}
	} else if err != nil {
		return err
	}
	if saved.Input != in {
		return errors.New("tenant belongs to another provisioning request")
	}
	form.Token = saved.Token
	run, err := h.create(ctx, form)
	if err != nil {
		return err
	}
	if run.Active {
		return nil
	}
	review := make([]starterExpectation, 0, len(run.Pages))
	for _, id := range run.Pages {
		item, e := cs.Get(ctx, id)
		if e != nil {
			return e
		}
		draft, e := h.svc.Load(ctx, id)
		if e != nil {
			return e
		}
		review = append(review, starterExpectation{ID: id, UpdatedAt: content.EditToken(item), DocumentUpdatedAt: draft.UpdatedAt})
	}
	return h.activateReviewed(ctx, run.Token, review)
}
