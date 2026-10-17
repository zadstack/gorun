package wpmigrate

import (
	"context"
	"html"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Source reads a WordPress site; restSource (REST API) and wxrSource (WXR
// export) implement it so one pipeline serves both. Methods return
// ErrUnavailable (wrapped) when the source cannot provide the data.
type Source interface {
	Kind() string
	Site(ctx context.Context) (SiteInfo, error)
	Users(ctx context.Context) ([]WPUser, error)
	Terms(ctx context.Context, taxonomies []string) ([]WPTerm, error)
	Media(ctx context.Context) ([]WPMedia, error)
	Content(ctx context.Context, types []string) ([]WPContent, error)
	Products(ctx context.Context) ([]WPProduct, error)
	Menus(ctx context.Context) ([]WPMenu, error)
}

// SiteInfo is the source site's identity.
type SiteInfo struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Home        string `json:"home"`
	Description string `json:"description"`
	Language    string `json:"language"`
	Version     string `json:"version"`
	FrontPageID int    `json:"front_page_id"`
	// FromSettings is true when title/tagline came from /wp/v2/settings
	// (authenticated) rather than the public index.
	FromSettings bool `json:"from_settings"`
	PostsPageID  int  `json:"posts_page_id"`
}

// WPUser is an author.
type WPUser struct {
	ID    int    `json:"id"`
	Login string `json:"login"`
	Slug  string `json:"slug"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// WPTerm is a taxonomy term.
type WPTerm struct {
	ID          int    `json:"id"`
	Taxonomy    string `json:"taxonomy"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Parent      int    `json:"parent"`
	Count       int    `json:"count"`
}

// WPMedia is an attachment.
type WPMedia struct {
	ID       int      `json:"id"`
	URL      string   `json:"url"`
	Slug     string   `json:"slug"`
	Mime     string   `json:"mime"`
	Alt      string   `json:"alt"`
	Caption  string   `json:"caption"`
	Title    string   `json:"title"`
	Modified string   `json:"modified"`
	Bytes    int64    `json:"bytes"`
	Sizes    []string `json:"sizes"`
}

// WPContent is a post of any public type.
type WPContent struct {
	ID            int              `json:"id"`
	Type          string           `json:"type"`
	Slug          string           `json:"slug"`
	Title         string           `json:"title"`
	Link          string           `json:"link"`
	Status        string           `json:"status"`
	Date          string           `json:"date"` // GMT, RFC3339 or "2006-01-02 15:04:05"
	Modified      string           `json:"modified"`
	Raw           string           `json:"raw"`
	Rendered      string           `json:"rendered"`
	Excerpt       string           `json:"excerpt"`
	Template      string           `json:"template"`
	Parent        int              `json:"parent"`
	MenuOrder     int              `json:"menu_order"`
	Author        int              `json:"author"`
	FeaturedMedia int              `json:"featured_media"`
	Terms         map[string][]int `json:"terms"` // WP taxonomy → term ids
	Meta          map[string]any   `json:"meta"`
	Yoast         map[string]any   `json:"yoast"`
	ACF           map[string]any   `json:"acf"`
	IsFront       bool             `json:"is_front"`
}

// WPMenu is a navigation menu with its items.
type WPMenu struct {
	ID       int          `json:"id"`
	Slug     string       `json:"slug"`
	Name     string       `json:"name"`
	Location string       `json:"location"`
	Items    []WPMenuItem `json:"items"`
}

// WPMenuItem is a menu entry; Object/ObjectID reference a post or term
// when Type is post_type or taxonomy.
type WPMenuItem struct {
	ID       int    `json:"id"`
	Parent   int    `json:"parent"`
	Order    int    `json:"order"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Type     string `json:"type"`
	Object   string `json:"object"`
	ObjectID int    `json:"object_id"`
}

// decodeSlug turns WordPress's percent-encoded slugs (Arabic is stored as
// %d8%a7…) into UTF-8 ("استوديو…").
func decodeSlug(s string) string {
	if d, err := url.PathUnescape(s); err == nil {
		s = d
	}
	return strings.ToValidUTF8(s, "")
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

// plainText strips tags and decodes entities (titles, excerpts, captions).
func plainText(s string) string {
	return strings.TrimSpace(html.UnescapeString(tagRe.ReplaceAllString(s, "")))
}

// hostOf returns the lowercase host without "www.".
func hostOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

// pathOf returns the decoded path of a URL, without trailing slash ("/"
// for the root).
func pathOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	p := u.Path
	if !utf8.ValidString(p) {
		p = strings.ToValidUTF8(p, "")
	}
	for len(p) > 1 && strings.HasSuffix(p, "/") {
		p = strings.TrimSuffix(p, "/")
	}
	if p == "" {
		p = "/"
	}
	return p
}
