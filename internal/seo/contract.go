package seo

import (
	"context"
	"encoding/json"
	"html/template"
)

type Meta struct {
	Title         string          `json:"title"`
	Description   string          `json:"description"`
	Canonical     string          `json:"canonical"`
	Robots        string          `json:"robots"`
	OGTitle       string          `json:"og_title"`
	OGDescription string          `json:"og_description"`
	OGImage       string          `json:"og_image"`
	TwitterCard   string          `json:"twitter_card"`
	Schema        json.RawMessage `json:"schema,omitempty"`
}

// Map keys used by FromMap/ToMap (content.Item.SEO).
const (
	keyTitle         = "title"
	keyDescription   = "description"
	keyCanonical     = "canonical"
	keyRobots        = "robots"
	keyOGTitle       = "og_title"
	keyOGDescription = "og_description"
	keyOGImage       = "og_image"
	keyTwitterCard   = "twitter_card"
	keySchema        = "schema"
)

// FromMap reads a content item's SEO map; unknown keys and non-string values
// are ignored; "schema" may be any JSON value.
func FromMap(m map[string]any) Meta {
	str := func(k string) string { s, _ := m[k].(string); return s }
	meta := Meta{Title: str(keyTitle), Description: str(keyDescription), Canonical: str(keyCanonical),
		Robots: str(keyRobots), OGTitle: str(keyOGTitle), OGDescription: str(keyOGDescription),
		OGImage: str(keyOGImage), TwitterCard: str(keyTwitterCard)}
	if v, ok := m[keySchema]; ok && v != nil {
		if b, err := json.Marshal(v); err == nil {
			meta.Schema = b
		}
	}
	return meta
}

// ToMap is the inverse of FromMap; empty fields are omitted.
func (m Meta) ToMap() map[string]any {
	out := map[string]any{}
	for k, v := range map[string]string{keyTitle: m.Title, keyDescription: m.Description, keyCanonical: m.Canonical,
		keyRobots: m.Robots, keyOGTitle: m.OGTitle, keyOGDescription: m.OGDescription, keyOGImage: m.OGImage,
		keyTwitterCard: m.TwitterCard} {
		if v != "" {
			out[k] = v
		}
	}
	if len(m.Schema) > 0 {
		var v any
		if json.Unmarshal(m.Schema, &v) == nil {
			out[keySchema] = v
		}
	}
	return out
}

type Service interface {
	Render(ctx context.Context, m Meta, fallback Meta) template.HTML
	Sitemap(ctx context.Context) ([]byte, error)
	// Entries is the sitemap as data (absolute Locs), for admin screens.
	Entries(ctx context.Context) ([]SitemapEntry, error)
	Robots(ctx context.Context) string
}
