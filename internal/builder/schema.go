package builder

import "slices"

// The section schema: one declarative table of what each section kind
// accepts, read by the validator (validateNative), by the editor
// (builder.js builds each section's form from it instead of carrying its
// own copy of the rules), and by agents and integrations through
// GET /v1/builder/schema and the get_section_schema tool. A new kind is one
// row here plus its template; the editor form follows.

// KindSchema describes one section kind under one library.
type KindSchema struct {
	Kind string `json:"kind"`
	// Label is the kind's name in the requested locale (filled by Schema).
	Label string `json:"label,omitempty"`
	// Variants are the layouts accepted besides "default".
	Variants []string `json:"variants"`
	// Fields are the section-level fields the kind accepts: title, text,
	// image, image_alt, link, link_label.
	Fields []string `json:"fields"`
	// NativeFields are the native options (Section.native) the kind accepts.
	// empty_text applies only with a non-default features layout; search only
	// to a task_entry in the search_first layout.
	NativeFields []string `json:"native_fields,omitempty"`
	// Items reports whether the kind carries repeated entries.
	Items bool `json:"items"`
	// ItemFields are the entry fields the kind accepts: title, text, image,
	// image_alt, link, price, icon, featured.
	ItemFields []string `json:"item_fields,omitempty"`
	// Required names what publication or validation insists on: "title",
	// "item.title", "item.link", "native.list_type", "native.columns".
	Required []string `json:"required,omitempty"`
}

// SectionSchema is the whole contract.
type SectionSchema struct {
	Version int `json:"version"`
	// Legacy is what every kind accepts under the empty library.
	Legacy KindSchema `json:"legacy"`
	// Kinds lists every kind with its legacy label.
	Kinds []string `json:"kinds"`
	// Native lists the kinds that accept library "sections" and what each
	// accepts there.
	Native []KindSchema `json:"native"`
	// NativeOnly are the kinds that exist only in the section library.
	NativeOnly  []string       `json:"native_only"`
	Icons       []string       `json:"icons"`
	Backgrounds []string       `json:"backgrounds"`
	HeroScenes  []string       `json:"hero_scenes"`
	Feeds       []string       `json:"feeds"`
	Limits      map[string]int `json:"limits"`
}

var (
	legacyFields     = []string{"title", "text", "image", "image_alt", "link", "link_label"}
	nativeTextFields = []string{"title", "text", "link", "link_label"}
	legacySchema     = KindSchema{Kind: "*", Variants: []string{"split", "centered"}, Fields: legacyFields, Items: true, ItemFields: []string{"title", "text", "image", "image_alt", "link", "price"}}
)

// nativeSchemas is the per-kind table for the section library. Order is
// the editor's catalog order.
var nativeSchemas = []KindSchema{
	{Kind: "hero", Variants: []string{"split", "centered"}, Fields: legacyFields, NativeFields: []string{"background", "eyebrow", "secondary_link", "secondary_label", "facts", "caption", "illustration"}},
	{Kind: "features", Variants: []string{"compact_rows", "image_led", "featured_secondary"}, Fields: nativeTextFields, NativeFields: []string{"background", "empty_text"}, Items: true, ItemFields: []string{"title", "text", "image", "image_alt", "link", "icon", "featured"}},
	{Kind: "steps", Fields: nativeTextFields, NativeFields: []string{"background"}, Items: true, ItemFields: []string{"title", "text", "image", "image_alt"}},
	{Kind: "comparison", Fields: nativeTextFields, NativeFields: []string{"background", "columns", "rows"}, Required: []string{"title", "native.columns"}},
	{Kind: "faq", Variants: []string{"split"}, Fields: nativeTextFields, NativeFields: []string{"background"}, Items: true, ItemFields: []string{"title", "text"}, Required: []string{"item.title"}},
	{Kind: "product_preview", Fields: legacyFields, NativeFields: []string{"background", "caption", "illustration"}},
	{Kind: "media_text", Variants: []string{"image_start", "image_end"}, Fields: nativeTextFields, NativeFields: []string{"background"}, Items: true, ItemFields: []string{"title", "text", "image", "image_alt"}},
	{Kind: "switcher", Fields: nativeTextFields, NativeFields: []string{"background"}, Items: true, ItemFields: []string{"title", "text", "image", "image_alt"}, Required: []string{"item.title"}},
	{Kind: "directory", Variants: []string{"compact"}, Fields: nativeTextFields, NativeFields: []string{"background", "list_type"}, Required: []string{"native.list_type"}},
	{Kind: "task_entry", Variants: []string{"search_first", "choice_first", "service_first"}, Fields: nativeTextFields, NativeFields: []string{"background", "search"}, Items: true, ItemFields: []string{"title", "text", "link", "icon"}, Required: []string{"item.title", "item.link"}},
}

// nativeSchema returns the section-library schema of kind (ok false for a
// kind the library does not have).
func nativeSchema(kind string) (KindSchema, bool) {
	for _, s := range nativeSchemas {
		if s.Kind == kind {
			return s, true
		}
	}
	return KindSchema{}, false
}

// Accepts reports whether the kind accepts a section field, a native option
// ("native.<name>") or an entry field ("item.<name>").
func (k KindSchema) Accepts(field string) bool {
	switch {
	case len(field) > 7 && field[:7] == "native.":
		return slices.Contains(k.NativeFields, field[7:])
	case len(field) > 5 && field[:5] == "item.":
		return k.Items && slices.Contains(k.ItemFields, field[5:])
	}
	return slices.Contains(k.Fields, field)
}

// Schema returns the section contract with labels in locale.
func Schema(locale string) SectionSchema {
	labels := map[string]string{}
	for _, spec := range Catalog(locale) {
		labels[spec.Kind] = spec.Label
	}
	native := make([]KindSchema, len(nativeSchemas))
	for i, s := range nativeSchemas {
		native[i] = s
		native[i].Label = labels[s.Kind]
	}
	return SectionSchema{Version: 1, Legacy: legacySchema, Kinds: slices.Clone(kinds), Native: native, NativeOnly: slices.Clone(nativeOnlyKinds),
		Icons: slices.Clone(sectionIcons), Backgrounds: slices.Clone(sectionBackgrounds), HeroScenes: []string{"", "orbit", "ribbons", "blocks"}, Feeds: []string{"", "posts"},
		Limits: map[string]int{"sections": maxSections, "items": maxRepeatedEntries, "facts": maxNativeFacts, "comparison_columns": maxComparisonColumns, "comparison_rows": maxRepeatedEntries, "label_bytes": maxNativeLabelBytes, "text_bytes": maxNativeTextBytes}}
}
