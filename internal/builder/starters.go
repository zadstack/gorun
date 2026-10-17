package builder

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/brand"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/settings"
	"github.com/zadstack/gorun/internal/theme"
)

type starterIdentity = brand.Identity

type starterForm struct {
	Token          string `form:"token"`
	Starter        string `form:"starter"`
	Title          string `form:"title"`
	Tagline        string `form:"tagline"`
	Locale         string `form:"locale"`
	Accent         string `form:"accent"`
	Background     string `form:"background"`
	Text           string `form:"text"`
	Font           string `form:"font"`
	Width          string `form:"width"`
	Description    string `form:"description"`
	About          string `form:"about"`
	Services       string `form:"services"`
	ContactEmail   string `form:"contact_email"`
	ContactPhone   string `form:"contact_phone"`
	ContactAddress string `form:"contact_address"`
	Logo           string `form:"logo"`
	HeroImage      string `form:"hero_image"`
}
type starterRun struct {
	Token     string            `json:"token"`
	Owner     string            `json:"owner"`
	Kind      string            `json:"kind"`
	Theme     string            `json:"theme,omitempty"`
	Title     string            `json:"title"`
	Tagline   string            `json:"tagline"`
	Locale    string            `json:"locale"`
	Profile   starterProfile    `json:"profile,omitempty"`
	Identity  starterIdentity   `json:"identity"`
	Pages     map[string]string `json:"pages"`
	MenuID    string            `json:"menu_id"`
	Active    bool              `json:"active"`
	CreatedAt string            `json:"created_at"`
}
type starterHandler struct {
	d   *kernel.Deps
	svc *service
	mu  sync.Mutex
}

func starterAllowed(ctx context.Context) bool {
	u := auth.UserFromContext(ctx)
	if u == nil || u.Role != "admin" {
		return false
	}
	for _, perm := range []string{"settings.manage", "content.create", "content.edit", "content.publish", "menus.manage"} {
		if !auth.Can(ctx, perm) {
			return false
		}
	}
	return true
}

func (h *starterHandler) issue(ctx context.Context) (*starterRun, error) {
	u := auth.UserFromContext(ctx)
	if !starterAllowed(ctx) {
		return nil, content.ErrForbidden
	}
	run := &starterRun{Token: strings.ToLower(db.NewID()), Owner: u.ID, Pages: map[string]string{}, CreatedAt: db.Now()}
	if err := h.d.Settings.Set(ctx, "builder.starter."+run.Token, run); err != nil {
		return nil, err
	}
	return run, nil
}
func (h *starterHandler) run(ctx context.Context, token string) (*starterRun, error) {
	if len(token) != 26 {
		return nil, content.ErrNotFound
	}
	var run starterRun
	if err := h.d.Settings.Get(ctx, "builder.starter."+token, &run); err != nil {
		return nil, err
	}
	u := auth.UserFromContext(ctx)
	if !starterAllowed(ctx) || u.ID != run.Owner {
		return nil, content.ErrForbidden
	}
	return &run, nil
}
func validateStarter(f starterForm) error {
	if len(starterPages(f.Starter, "", "", f.Locale)) == 0 || strings.TrimSpace(f.Title) == "" || len(f.Title) > 200 || len(f.Tagline) > 600 {
		return errors.New("invalid starter or site name")
	}
	if f.Locale != "ar" && f.Locale != "en" {
		return errors.New("invalid locale")
	}
	return brand.Validate(starterIdentity{Accent: f.Accent, Background: f.Background, Text: f.Text, Font: f.Font, Width: f.Width})
}
func (h *starterHandler) create(ctx context.Context, f starterForm) (*starterRun, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	run, err := h.run(ctx, f.Token)
	if err != nil {
		return nil, err
	}
	if run.Active {
		return run, nil
	}
	if err := validateStarter(f); err != nil {
		return nil, err
	}
	if run.Kind == "" {
		profile, err := captureProfile(ctx, h.d, f)
		if err != nil {
			return nil, err
		}
		run.Profile = profile
		run.Kind, run.Title, run.Tagline, run.Locale = f.Starter, strings.TrimSpace(f.Title), strings.TrimSpace(f.Tagline), f.Locale
		run.Theme = starterDesign(f.Starter).Theme
		run.Identity = starterIdentity{Accent: f.Accent, Background: f.Background, Text: f.Text, Font: f.Font, Width: f.Width}
		if err := h.d.Settings.Set(ctx, "builder.starter."+run.Token, run); err != nil {
			return nil, err
		}
	}
	cs, ok := kernel.Get[content.Service](h.d, kernel.ServiceContent)
	if !ok {
		return nil, errors.New("content service unavailable")
	}
	pages := profilePages(starterPages(run.Kind, run.Title, run.Tagline, run.Locale), run.Profile, run.Kind, run.Locale)
	for _, p := range pages {
		if run.Pages[p.Slug] != "" {
			continue
		}
		slug := p.Slug + "-" + run.Token[len(run.Token)-8:]
		var recoveredID string
		err := h.d.DB.QueryRowContext(ctx, `SELECT id FROM content WHERE type='page' AND json_extract(seo_json,'$.builder_starter')=? AND json_extract(seo_json,'$.builder_starter_page')=? LIMIT 1`, run.Token, p.Slug).Scan(&recoveredID)
		var it *content.Item
		if errors.Is(err, sql.ErrNoRows) {
			it, err = cs.Create(ctx, content.Input{Type: "page", Slug: slug, Title: p.Title, Status: "draft", Locale: run.Locale, AuthorID: run.Owner, Template: "builder", SEO: map[string]any{"builder_starter": run.Token, "builder_starter_page": p.Slug}})
		} else if err == nil {
			it, err = cs.Get(ctx, recoveredID)
		}
		if err != nil {
			return nil, err
		}
		if it.AuthorID != run.Owner || it.Status != "draft" {
			return nil, content.ErrForbidden
		}
		run.Pages[p.Slug] = it.ID
		if err := h.d.Settings.Set(ctx, "builder.starter."+run.Token, run); err != nil {
			return nil, err
		}
	}
	for _, p := range pages {
		id := run.Pages[p.Slug]
		if draft, err := h.svc.Load(ctx, id); err == nil && len(draft.Document.Sections) > 0 {
			continue
		} else if err != nil && !errors.Is(err, content.ErrNotFound) && !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, settings.ErrNotFound) {
			return nil, err
		}
		for i := range p.Document.Sections {
			link, err := resolveStarterLink(ctx, cs, run, p.Document.Sections[i].Link)
			if err != nil {
				return nil, err
			}
			p.Document.Sections[i].Link = link
			for j := range p.Document.Sections[i].Items {
				link, err := resolveStarterLink(ctx, cs, run, p.Document.Sections[i].Items[j].Link)
				if err != nil {
					return nil, err
				}
				p.Document.Sections[i].Items[j].Link = link
			}
		}

		it, err := cs.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		body, err := Render(p.Document)
		if err != nil {
			return nil, err
		}
		in := content.InputFromItem(it)
		in.Body = body
		in.Template = "builder"
		if p.Slug == "home" {
			if run.Profile.Description != "" {
				in.Excerpt = run.Profile.Description
				in.SEO["description"] = run.Profile.Description
			}
			if run.Profile.HeroImage != "" {
				in.FeaturedMediaID = run.Profile.HeroImage
			}
		}
		it, err = cs.Update(ctx, id, in)
		if err != nil {
			return nil, err
		}
		if _, err := h.svc.Save(ctx, id, p.Document, content.EditToken(it)); err != nil {
			return nil, err
		}
	}
	if run.MenuID == "" {
		ms, ok := kernel.Get[menus.Service](h.d, kernel.ServiceMenus)
		if !ok {
			return nil, errors.New("menu service unavailable")
		}
		items := make([]menus.Item, 0, len(pages))
		for _, p := range pages {
			items = append(items, menus.Item{Label: p.Title, Kind: menus.KindContent, RefID: run.Pages[p.Slug]})
		}
		slug := "starter-" + run.Token
		menu, err := ms.Get(ctx, slug)
		if errors.Is(err, menus.ErrNotFound) {
			menu, err = ms.Save(ctx, menus.Menu{Slug: slug, Name: run.Title, Locale: run.Locale, Items: items})
		}
		if err != nil {
			return nil, err
		}
		run.MenuID = menu.ID
		if err := h.d.Settings.Set(ctx, "builder.starter."+run.Token, run); err != nil {
			return nil, err
		}
	}
	return run, nil
}

type starterExpectation struct{ ID, UpdatedAt, DocumentUpdatedAt string }

func (h *starterHandler) activateReviewed(ctx context.Context, token string, review []starterExpectation) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	run, err := h.run(ctx, token)
	if err != nil {
		return err
	}
	if run.Active {
		return nil
	}
	for _, selected := range []struct{ field, id string }{{"logo", run.Profile.Logo}, {"hero_image", run.Profile.HeroImage}} {
		if selected.id != "" {
			if _, err := profileImage(ctx, h.d, selected.field, selected.id); err != nil {
				return err
			}
		}
	}
	cs, ok := kernel.Get[content.Service](h.d, kernel.ServiceContent)
	if !ok {
		return errors.New("content service unavailable")
	}
	if ts, ok := kernel.Get[theme.Service](h.d, kernel.ServiceTheme); ok {
		installed, err := ts.List(ctx)
		if err != nil {
			return err
		}
		found := false
		for _, candidate := range installed {
			if candidate.Slug == runTheme(run) {
				found = true
			}
		}
		if !found {
			return errors.New("starter theme is unavailable")
		}
	}
	publisher, ok := cs.(content.BatchPublisher)
	if !ok {
		return errors.New("batch publishing unavailable")
	}
	if len(review) != len(run.Pages) || run.MenuID == "" {
		return errors.New("starter is incomplete")
	}
	allowed := map[string]bool{}
	for _, id := range run.Pages {
		allowed[id] = true
	}
	for _, p := range review {
		if !allowed[p.ID] {
			return content.ErrForbidden
		}
		delete(allowed, p.ID)
	}
	if len(allowed) > 0 {
		return content.ErrForbidden
	}
	expected := make([]content.PublishExpectation, 0, len(review))
	for _, p := range review {
		draft, err := h.svc.Load(ctx, p.ID)
		if err != nil {
			return err
		}
		if draft.UpdatedAt != p.DocumentUpdatedAt || draft.BaseUpdatedAt != p.UpdatedAt {
			return ErrConflict
		}
		if err := ValidatePublication(draft.Document); err != nil {
			return err
		}
		body, err := Render(draft.Document)
		if err != nil {
			return err
		}
		expected = append(expected, content.PublishExpectation{ID: p.ID, UpdatedAt: p.UpdatedAt, Body: &body, Template: "builder"})
	}
	return publisher.PublishBatch(ctx, expected, func(tx *sql.Tx, saved []*content.Item) error {
		tokens := map[string]string{}
		for _, it := range saved {
			tokens[it.ID] = content.EditToken(it)
		}
		for _, p := range review {
			timestamp := tokens[p.ID]
			result, err := tx.ExecContext(ctx, `UPDATE builder_documents SET base_updated_at=?,updated_at=?,published_document_json=document_json WHERE content_id=? AND updated_at=?`, timestamp, db.NewID(), p.ID, p.DocumentUpdatedAt)
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if n != 1 {
				return ErrConflict
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE menus SET location='' WHERE location='primary' AND locale IN (?, '')`, run.Locale); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE menus SET location='primary',updated_at=? WHERE id=?`, db.Now(), run.MenuID)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return errors.New("starter menu disappeared")
		}
		for key, value := range map[string]any{"site.title": run.Title, "site.tagline": run.Tagline, "site.locale": run.Locale, "site.front_page": run.Pages["home"], "site.theme": runTheme(run), "builder.identity": run.Identity} {
			if err := h.d.Settings.SetTx(ctx, tx, key, value); err != nil {
				return err
			}
		}
		for key, value := range map[string]string{"site.logo": run.Profile.Logo, "contact.email": run.Profile.Email, "contact.phone": run.Profile.Phone, "contact.address": run.Profile.Address} {
			if value != "" {
				if err := h.d.Settings.SetTx(ctx, tx, key, value); err != nil {
					return err
				}
			}
		}
		run.Active = true
		if err := h.d.Settings.SetTx(ctx, tx, "builder.starter."+token, run); err != nil {
			return err
		}
		h.d.Audit.RecordTx(ctx, tx, "builder.activate", "starter", token, map[string]any{"kind": run.Kind, "pages": len(run.Pages)})
		return nil
	})
}

func runTheme(run *starterRun) string {
	if run.Theme == "" {
		return "default"
	}
	return run.Theme
}
func resolveStarterLink(ctx context.Context, cs content.Service, run *starterRun, link string) (string, error) {
	if !strings.HasPrefix(link, "@") {
		return link, nil
	}
	item, err := cs.Get(ctx, run.Pages[strings.TrimPrefix(link, "@")])
	if err != nil {
		return "", err
	}
	return "/" + item.Slug, nil
}
