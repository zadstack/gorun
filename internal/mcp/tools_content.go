package mcp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

// Statuses an agent may set without approval; going live (or back from
// live) uses the sensitive publish_content / unpublish_content tools.
var draftStatuses = map[content.Status]bool{"": true, "draft": true, "pending": true}

type listContentInput struct {
	Type     string                    `json:"type,omitempty"`
	Status   string                    `json:"status,omitempty" jsonschema:"draft, pending, scheduled, published or private"`
	Search   string                    `json:"search,omitempty"`
	AuthorID string                    `json:"author_id,omitempty"`
	ParentID string                    `json:"parent_id,omitempty"`
	Locale   string                    `json:"locale,omitempty"`
	Taxonomy map[string][]string       `json:"taxonomy,omitempty" jsonschema:"taxonomy slug → term slugs, e.g. {\"category\":[\"news\"]}"`
	Fields   map[string]content.Filter `json:"fields,omitempty" jsonschema:"indexed field key → {op: eq|ne|gt|lt|gte|lte|in|like, value}"`
	Order    []content.Order           `json:"order,omitempty" jsonschema:"[{by, dir: asc|desc}]"`
	Page     int                       `json:"page,omitempty"`
	PerPage  int                       `json:"per_page,omitempty" jsonschema:"default 20, max 100"`
}

type getContentInput struct {
	ID     string `json:"id,omitempty"`
	Type   string `json:"type,omitempty" jsonschema:"with slug, when id is not given"`
	Slug   string `json:"slug,omitempty"`
	Locale string `json:"locale,omitempty"`
}

type createContentInput struct {
	Type            string              `json:"type"`
	Title           string              `json:"title"`
	Slug            string              `json:"slug,omitempty"`
	Excerpt         string              `json:"excerpt,omitempty"`
	Body            string              `json:"body,omitempty" jsonschema:"HTML"`
	Fields          map[string]any      `json:"fields,omitempty" jsonschema:"custom field values keyed by field key"`
	Status          string              `json:"status,omitempty" jsonschema:"draft (default) or pending; publish with publish_content"`
	ParentID        string              `json:"parent_id,omitempty"`
	MenuOrder       *int                `json:"menu_order,omitempty"`
	Template        string              `json:"template,omitempty"`
	FeaturedMediaID string              `json:"featured_media_id,omitempty"`
	SEO             map[string]any      `json:"seo,omitempty"`
	Locale          string              `json:"locale,omitempty"`
	Terms           map[string][]string `json:"terms,omitempty" jsonschema:"taxonomy slug → term slugs"`
}

type updateContentInput struct {
	ID              string              `json:"id"`
	Title           string              `json:"title,omitempty"`
	Slug            string              `json:"slug,omitempty"`
	Excerpt         string              `json:"excerpt,omitempty"`
	Body            string              `json:"body,omitempty"`
	Fields          map[string]any      `json:"fields,omitempty" jsonschema:"merged into the existing values key by key"`
	Status          string              `json:"status,omitempty" jsonschema:"draft or pending (only for items that are not live)"`
	ParentID        string              `json:"parent_id,omitempty"`
	MenuOrder       *int                `json:"menu_order,omitempty"`
	Template        string              `json:"template,omitempty"`
	FeaturedMediaID string              `json:"featured_media_id,omitempty"`
	SEO             map[string]any      `json:"seo,omitempty"`
	Locale          string              `json:"locale,omitempty"`
	Terms           map[string][]string `json:"terms,omitempty" jsonschema:"replaces the item's terms for each taxonomy given"`
}

type idInput struct {
	ID string `json:"id"`
}

type translationInput struct {
	ID     string `json:"id"`
	Locale string `json:"locale" jsonschema:"the locale of the new translation, e.g. en"`
}

type linkTranslationInput struct {
	ID            string `json:"id"`
	TranslationID string `json:"translation_id" jsonschema:"the item (with its own translations) to link as a translation of id"`
}

type deleteContentInput struct {
	ID   string `json:"id"`
	Hard bool   `json:"hard,omitempty" jsonschema:"true deletes permanently; default moves to trash"`
}

func contentTools(svc content.Service) []kernel.MCPTool {
	return []kernel.MCPTool{
		{Name: "list_content", Scopes: []string{scopeView}, Input: listContentInput{},
			Description: "Query content with the GoRun query DSL (type, status, search, taxonomy, indexed field filters, order, paging).",
			Handler: bind(func(ctx context.Context, in listContentInput) (any, error) {
				page, per := paging(in.Page, in.PerPage)
				p, err := svc.Query(ctx, content.Query{Type: in.Type, Status: in.Status, Search: in.Search, AuthorID: in.AuthorID,
					ParentID: in.ParentID, Locale: in.Locale, Taxonomy: in.Taxonomy, Fields: in.Fields, Order: in.Order, Page: page, PerPage: per})
				if err != nil {
					return nil, err
				}
				return newPage(p.Items, p.Total, p.Page, p.PerPage), nil
			})},
		{Name: "get_content", Scopes: []string{scopeView}, Input: getContentInput{},
			Description: "Get one content item by id, or by type + slug (+ locale).",
			Handler: bind(func(ctx context.Context, in getContentInput) (any, error) {
				switch {
				case in.ID != "":
					return svc.Get(ctx, in.ID)
				case in.Type != "" && in.Slug != "":
					return svc.GetBySlug(ctx, in.Type, in.Slug, in.Locale)
				}
				return nil, invalid("give id, or type and slug", map[string]string{"id": "required"})
			})},
		{Name: "create_content", Scopes: []string{scopeCreate}, Input: createContentInput{},
			Description: "Create a content item as a draft (or pending review). Field values are validated against the type's schema.",
			Handler: bind(func(ctx context.Context, in createContentInput) (any, error) {
				if !draftStatuses[content.Status(in.Status)] {
					return nil, invalid("create_content only creates drafts; use publish_content to go live", map[string]string{"status": "draft_or_pending"})
				}
				status := content.Status(in.Status)
				if status == "" {
					status = "draft"
				}
				return svc.Create(ctx, content.Input{Type: in.Type, Title: in.Title, Slug: in.Slug, Excerpt: in.Excerpt, Body: in.Body,
					Fields: in.Fields, AuthorID: actingUserID(ctx), Status: status, ParentID: in.ParentID, MenuOrder: in.MenuOrder,
					Template: in.Template, FeaturedMediaID: in.FeaturedMediaID, SEO: in.SEO, Locale: in.Locale, Terms: in.Terms})
			})},
		{Name: "update_content", Scopes: []string{scopeEdit}, Input: updateContentInput{},
			Description: "Update a content item: only the keys you send change (fields are merged key by key). Editing a live (published, private or scheduled) item needs human approval.",
			Handler:     func(ctx context.Context, args json.RawMessage) (any, error) { return updateContent(ctx, svc, args) }},
		{Name: "publish_content", Scopes: []string{scopePublish}, Sensitive: true, Input: idInput{},
			Description: "Publish a content item (goes live).",
			Handler: bind(func(ctx context.Context, in idInput) (any, error) {
				if err := svc.Publish(ctx, in.ID); err != nil {
					return nil, err
				}
				return svc.Get(ctx, in.ID)
			})},
		{Name: "unpublish_content", Scopes: []string{scopePublish}, Sensitive: true, Input: idInput{},
			Description: "Unpublish a content item (back to draft).",
			Handler: bind(func(ctx context.Context, in idInput) (any, error) {
				if err := svc.Unpublish(ctx, in.ID); err != nil {
					return nil, err
				}
				return svc.Get(ctx, in.ID)
			})},
		{Name: "list_translations", Scopes: []string{scopeView}, Input: idInput{},
			Description: "List a content item and its translations (the same content in other locales), by locale.",
			Handler:     bind(func(ctx context.Context, in idInput) (any, error) { return svc.Translations(ctx, in.ID) })},
		{Name: "create_translation", Scopes: []string{scopeCreate}, Input: translationInput{},
			Description: "Copy a content item into another locale as a draft linked to it as a translation; then translate its text with update_content.",
			Handler: bind(func(ctx context.Context, in translationInput) (any, error) {
				return svc.CreateTranslation(ctx, in.ID, in.Locale)
			})},
		{Name: "link_translation", Scopes: []string{scopeEdit}, Input: linkTranslationInput{},
			Description: "Link an existing item (and its translations) as translations of another item of the same type; each locale once.",
			Handler: bind(func(ctx context.Context, in linkTranslationInput) (any, error) {
				if err := svc.LinkTranslation(ctx, in.ID, in.TranslationID); err != nil {
					return nil, err
				}
				return svc.Translations(ctx, in.ID)
			})},
		{Name: "unlink_translation", Scopes: []string{scopeEdit}, Input: idInput{},
			Description: "Take a content item out of its translations (the item itself is kept).",
			Handler: bind(func(ctx context.Context, in idInput) (any, error) {
				if err := svc.UnlinkTranslation(ctx, in.ID); err != nil {
					return nil, err
				}
				return svc.Get(ctx, in.ID)
			})},
		{Name: "delete_content", Scopes: []string{scopeDelete}, Sensitive: true, Input: deleteContentInput{},
			Description: "Move a content item to the trash (hard: delete permanently).",
			Handler: bind(func(ctx context.Context, in deleteContentInput) (any, error) {
				if err := svc.Delete(ctx, in.ID, in.Hard); err != nil {
					return nil, err
				}
				return map[string]any{"id": in.ID, "deleted": true, "hard": in.Hard}, nil
			})},
	}
}

// liveStatuses are the states in which an item is (or will go) live.
var liveStatuses = map[content.Status]bool{"published": true, "private": true, "scheduled": true}

// liveItemRule makes update_content sensitive when the target item is live.
func liveItemRule(svc content.Service) func(context.Context, json.RawMessage) bool {
	return func(ctx context.Context, args json.RawMessage) bool {
		var in struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(normalizeArgs(args), &in); err != nil || in.ID == "" {
			return false
		}
		it, err := svc.Get(ctx, in.ID)
		return err == nil && liveStatuses[it.Status]
	}
}

func actingUserID(ctx context.Context) string {
	if u := auth.UserFromContext(ctx); u != nil {
		return u.ID
	}
	return ""
}

// updateContent loads the item, applies only the keys present in args and
// saves the full Input (content.Service.Update replaces everything).
func updateContent(ctx context.Context, svc content.Service, args json.RawMessage) (any, error) {
	var in updateContentInput
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal(normalizeArgs(args), &present); err != nil {
		return nil, invalid("invalid arguments", nil)
	}
	if strings.TrimSpace(in.ID) == "" {
		return nil, invalid("id is required", map[string]string{"id": "required"})
	}
	it, err := svc.Get(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	out := content.InputFromItem(it)
	has := func(k string) bool { _, ok := present[k]; return ok }
	if has("status") && in.Status != "" && content.Status(in.Status) != it.Status {
		if !draftStatuses[content.Status(in.Status)] || !draftStatuses[it.Status] {
			return nil, invalid("status changes to or from live states use publish_content / unpublish_content / delete_content",
				map[string]string{"status": "draft_or_pending"})
		}
		out.Status = content.Status(in.Status)
	}
	setIf(has("title"), &out.Title, in.Title)
	setIf(has("slug"), &out.Slug, in.Slug)
	setIf(has("excerpt"), &out.Excerpt, in.Excerpt)
	setIf(has("body"), &out.Body, in.Body)
	setIf(has("parent_id"), &out.ParentID, in.ParentID)
	setIf(has("template"), &out.Template, in.Template)
	setIf(has("featured_media_id"), &out.FeaturedMediaID, in.FeaturedMediaID)
	setIf(has("locale"), &out.Locale, in.Locale)
	if has("menu_order") {
		out.MenuOrder = in.MenuOrder
	}
	if has("seo") {
		out.SEO = in.SEO
	}
	if has("fields") {
		merged := make(map[string]any, len(out.Fields)+len(in.Fields))
		for k, v := range out.Fields {
			merged[k] = v
		}
		for k, v := range in.Fields {
			merged[k] = v
		}
		out.Fields = merged
	}
	if has("terms") {
		terms := make(map[string][]string, len(out.Terms)+len(in.Terms))
		for k, v := range out.Terms {
			terms[k] = v
		}
		for k, v := range in.Terms {
			terms[k] = v
		}
		out.Terms = terms
	}
	return svc.Update(ctx, in.ID, out)
}

func setIf(ok bool, dst *string, v string) {
	if ok {
		*dst = v
	}
}

// liveGroupRule makes a translation tool sensitive when any item it touches, or any of their
// translations, is live: linking changes the public pages' alternates and language links.
func liveGroupRule(svc content.Service, keys ...string) func(context.Context, json.RawMessage) bool {
	return func(ctx context.Context, args json.RawMessage) bool {
		var in map[string]any
		if err := json.Unmarshal(normalizeArgs(args), &in); err != nil {
			return false
		}
		for _, k := range keys {
			id, _ := in[k].(string)
			if id == "" {
				continue
			}
			members, err := svc.Translations(ctx, id)
			if err != nil {
				continue
			}
			for _, m := range members {
				if liveStatuses[m.Status] {
					return true
				}
			}
		}
		return false
	}
}
