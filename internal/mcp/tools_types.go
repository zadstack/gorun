package mcp

import (
	"context"
	"errors"
	"strings"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

type slugInput struct {
	Slug string `json:"slug" jsonschema:"content type slug"`
}

type createTypeInput struct {
	Slug         string            `json:"slug" jsonschema:"lowercase slug, e.g. service"`
	Name         map[string]string `json:"name" jsonschema:"plural label by locale, e.g. {\"ar\":\"الخدمات\",\"en\":\"Services\"}"`
	Singular     map[string]string `json:"singular,omitempty"`
	Plural       map[string]string `json:"plural,omitempty"`
	Icon         string            `json:"icon,omitempty" jsonschema:"Lucide icon name"`
	Public       *bool             `json:"public,omitempty" jsonschema:"default true"`
	HasArchive   bool              `json:"has_archive,omitempty"`
	Hierarchical bool              `json:"hierarchical,omitempty"`
	ArchiveSlug  string            `json:"archive_slug,omitempty"`
	Supports     []string          `json:"supports,omitempty"`
	Fields       []content.Field   `json:"fields,omitempty"`
	Taxonomies   []string          `json:"taxonomies,omitempty"`
	Templates    []string          `json:"templates,omitempty"`
	MenuPosition int               `json:"menu_position,omitempty"`
}

type updateFieldsInput struct {
	Slug   string          `json:"slug"`
	Fields []content.Field `json:"fields" jsonschema:"the complete new field list (replaces the existing fields)"`
}

func typeTools(svc content.Service) []kernel.MCPTool {
	return []kernel.MCPTool{
		{Name: "list_content_types", Scopes: []string{scopeView},
			Description: "List content types with their custom field schemas.",
			Handler: bind(func(ctx context.Context, _ struct{}) (any, error) {
				types, err := svc.Types(ctx)
				if types == nil {
					types = []content.Type{}
				}
				return map[string]any{"types": types}, err
			})},
		{Name: "get_content_type", Scopes: []string{scopeView}, Input: slugInput{},
			Description: "Get one content type with its field schema.",
			Handler: bind(func(ctx context.Context, in slugInput) (any, error) {
				return svc.GetType(ctx, in.Slug)
			})},
		{Name: "create_content_type", Scopes: []string{scopeTypes}, Input: createTypeInput{},
			Description: "Create a content type with custom fields (field types: text, textarea, richtext, number, date, datetime, boolean, select, multiselect, repeater, relationship, image, file, json).",
			Handler: bind(func(ctx context.Context, in createTypeInput) (any, error) {
				slug := strings.TrimSpace(in.Slug)
				if slug == "" {
					return nil, invalid("slug is required", map[string]string{"slug": "required"})
				}
				if _, err := svc.GetType(ctx, slug); err == nil {
					return nil, &ToolError{Code: codeConflict, Message: "content type " + slug + " already exists; use update_content_type_fields"}
				} else if !errors.Is(err, content.ErrNotFound) {
					return nil, err
				}
				public := in.Public == nil || *in.Public
				t := content.Type{Slug: slug, Name: in.Name, Singular: in.Singular, Plural: in.Plural, Icon: in.Icon, Public: public,
					HasArchive: in.HasArchive, Hierarchical: in.Hierarchical, ArchiveSlug: in.ArchiveSlug, Supports: in.Supports,
					Fields: in.Fields, Taxonomies: in.Taxonomies, Templates: in.Templates, MenuPosition: in.MenuPosition}
				if t.Fields == nil {
					t.Fields = []content.Field{}
				}
				if err := svc.SaveType(ctx, t); err != nil {
					return nil, err
				}
				return svc.GetType(ctx, slug)
			})},
		{Name: "update_content_type_fields", Scopes: []string{scopeTypes}, Input: updateFieldsInput{},
			Description: "Replace the custom field list of a content type (send every field to keep).",
			Handler: bind(func(ctx context.Context, in updateFieldsInput) (any, error) {
				t, err := svc.GetType(ctx, in.Slug)
				if err != nil {
					return nil, err
				}
				t.Fields = in.Fields
				if t.Fields == nil {
					t.Fields = []content.Field{}
				}
				if err := svc.SaveType(ctx, *t); err != nil {
					return nil, err
				}
				return svc.GetType(ctx, in.Slug)
			})},
	}
}
