package content

// PatchInput documents the merge decoder in patchContent; it is deliberately
// separate from Input, whose service-level Update contract replaces all fields.
type PatchInput struct {
	Type            *string               `json:"type,omitempty" doc:"Omitted or null keeps the type. A different type is rejected; empty keeps the existing type."`
	Slug            *string               `json:"slug,omitempty" doc:"Omitted or null keeps the slug; empty regenerates it from the title."`
	Title           *string               `json:"title,omitempty" doc:"Omitted or null keeps the title; empty clears it."`
	Excerpt         *string               `json:"excerpt,omitempty" doc:"Omitted or null keeps the excerpt; empty clears it."`
	Body            *string               `json:"body,omitempty" doc:"Omitted or null keeps the body; empty clears it. HTML is sanitized."`
	Fields          *map[string]any       `json:"fields,omitempty" doc:"Omitted keeps fields; an object merges by key (empty object keeps values); null clears schema fields before defaults and validation. Historical fields outside the schema are preserved."`
	AuthorID        *string               `json:"author_id,omitempty" doc:"Omitted or null keeps the author; empty also preserves an existing author. Ownership permissions still apply."`
	Status          *Status               `json:"status,omitempty" doc:"Omitted or null keeps status; empty resets status to draft. Use DELETE to trash."`
	ParentID        *string               `json:"parent_id,omitempty" doc:"Omitted or null keeps the parent; empty clears it."`
	MenuOrder       *int                  `json:"menu_order,omitempty" doc:"Omitted keeps order; null resets it to zero."`
	Template        *string               `json:"template,omitempty" doc:"Omitted or null keeps the template; empty clears it."`
	FeaturedMediaID *string               `json:"featured_media_id,omitempty" doc:"Omitted or null keeps featured media; empty clears it."`
	SEO             *map[string]any       `json:"seo,omitempty" doc:"Omitted keeps SEO; an object merges by key (empty object keeps values); null clears all SEO."`
	Locale          *string               `json:"locale,omitempty" doc:"Omitted, null or empty keeps the existing locale."`
	PublishAt       *string               `json:"publish_at,omitempty" format:"-" doc:"Omitted or null keeps the date; empty clears the date unless scheduled status requires it. Nonempty value must be a supported datetime."`
	Terms           *map[string]*[]string `json:"terms,omitempty" doc:"Omitted keeps terms; an object merges taxonomies (empty object keeps values); null clears all terms. Empty or null taxonomy arrays clear that taxonomy."`
}

// CreateInput documents creation's required type and optional defaulted state.
type CreateInput struct {
	Type            string                `json:"type" doc:"An existing content type slug is required."`
	Slug            *string               `json:"slug,omitempty" doc:"Omitted, null or empty generates a slug from the title."`
	Title           *string               `json:"title,omitempty" doc:"Optional title; defaults to empty."`
	Excerpt         *string               `json:"excerpt,omitempty" doc:"Optional excerpt; defaults to empty."`
	Body            *string               `json:"body,omitempty" doc:"Optional sanitized HTML; defaults to empty."`
	Fields          *map[string]any       `json:"fields,omitempty" doc:"Custom fields are validated against the content type, including defaults and required fields."`
	AuthorID        *string               `json:"author_id,omitempty" doc:"Defaults to the acting user; ownership permissions apply."`
	Status          *Status               `json:"status,omitempty" doc:"Defaults to draft. Publishing requires content.publish; future publication becomes scheduled."`
	ParentID        *string               `json:"parent_id,omitempty" doc:"Optional parent ID; defaults to empty."`
	MenuOrder       *int                  `json:"menu_order,omitempty" doc:"Defaults to zero."`
	Template        *string               `json:"template,omitempty" doc:"Optional rendering template; defaults to empty."`
	FeaturedMediaID *string               `json:"featured_media_id,omitempty" doc:"Optional featured media ID; defaults to empty."`
	SEO             *map[string]any       `json:"seo,omitempty" doc:"Optional SEO object; defaults to empty."`
	Locale          *string               `json:"locale,omitempty" doc:"Defaults to the request/admin content locale when present, otherwise the configured site locale."`
	PublishAt       *string               `json:"publish_at,omitempty" format:"-" doc:"Optional supported datetime; required for scheduled status."`
	Terms           *map[string]*[]string `json:"terms,omitempty" doc:"Optional taxonomy-to-slug arrays; defaults to empty."`
}
