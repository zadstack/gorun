package mcp

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/theme"
)

type templateNameInput struct {
	Name string `json:"name" jsonschema:"template name as listed by list_templates, e.g. single.html or parts.header.html"`
}

type templateDraftInput struct {
	Name string `json:"name"`
	Body string `json:"body" jsonschema:"Go html/template source using the theme functions"`
}

// UnmarshalJSON distinguishes a missing body from an intentional empty template.
func (in *templateDraftInput) UnmarshalJSON(raw []byte) error {
	var decoded struct {
		Name string  `json:"name"`
		Body *string `json:"body"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if decoded.Body == nil {
		return invalid("body is required and must be a string", map[string]string{"body": "required"})
	}
	in.Name, in.Body = decoded.Name, *decoded.Body
	return nil
}

func templateTools(svc theme.Service) []kernel.MCPTool {
	return []kernel.MCPTool{
		{Name: "list_templates", Scopes: []string{scopeThemes},
			Description: "List the active theme's templates and the saved overrides (drafts included).",
			Handler: bind(func(ctx context.Context, _ struct{}) (any, error) {
				files, err := svc.Templates(ctx)
				if err != nil {
					return nil, err
				}
				ovs, err := svc.Overrides(ctx)
				if err != nil {
					return nil, err
				}
				if files == nil {
					files = []theme.TemplateFile{}
				}
				if ovs == nil {
					ovs = []theme.Override{}
				}
				return map[string]any{"theme": svc.Active(ctx), "templates": files, "overrides": ovs}, nil
			})},
		{Name: "get_template", Scopes: []string{scopeThemes}, Input: templateNameInput{},
			Description: "Read published template body, source and saved draft (null if absent). Reading never publishes a draft.",
			Handler: bind(func(ctx context.Context, in templateNameInput) (any, error) {
				return svc.TemplateDetails(ctx, in.Name)
			})},
		{Name: "create_template_draft", Sensitive: true, Scopes: []string{scopeThemes}, Input: templateDraftInput{},
			Description: "Save a template override as a DRAFT. It is never published by this tool: a human previews and publishes it in Appearance → Templates.",
			Handler: bind(func(ctx context.Context, in templateDraftInput) (any, error) {
				if err := svc.SaveOverride(ctx, in.Name, in.Body, true); err != nil {
					return nil, err
				}
				return map[string]any{"name": in.Name, "draft": true,
					"message": "Draft saved. A human must preview and publish it in Appearance → Templates."}, nil
			})},
	}
}
