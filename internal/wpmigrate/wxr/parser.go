// Package wxr parses WordPress eXtended RSS exports (Tools → Export), the
// fallback source when the REST API hides data (postmeta such as the MCBB
// product mapping, drafts, menus).
package wxr

import (
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// File is a parsed export.
type File struct {
	Title       string
	Link        string
	Language    string
	BaseSiteURL string
	BaseBlogURL string
	Generator   string
	Version     string
	Authors     []Author
	Terms       []Term // categories, tags and wp:term entries, normalized
	Items       []Item
}

// Author is a wp:author entry.
type Author struct {
	ID          int
	Login       string
	Email       string
	DisplayName string
}

// Term is a taxonomy term. Parent is the parent's slug (WXR references
// parents by slug).
type Term struct {
	ID          int
	Taxonomy    string
	Slug        string
	Name        string
	Parent      string
	Description string
}

// ItemTerm is a <category domain="…" nicename="…"> on an item.
type ItemTerm struct {
	Domain   string
	Nicename string
	Name     string
}

// Item is an <item>: a post of any type (post, page, attachment, product,
// nav_menu_item, …).
type Item struct {
	ID            int
	Title         string
	Link          string
	Creator       string
	Content       string
	Excerpt       string
	PostDate      string
	PostDateGMT   string
	ModifiedGMT   string
	Name          string // post_name (slug, possibly percent-encoded)
	Status        string
	Parent        int
	MenuOrder     int
	Type          string
	AttachmentURL string
	Terms         []ItemTerm
	Meta          map[string]string
}

// MetaInt returns Meta[key] as an int (0 if absent or invalid).
func (it Item) MetaInt(key string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(it.Meta[key]))
	return n
}

// TermsIn returns the item's term slugs in taxonomy domain.
func (it Item) TermsIn(domain string) []string {
	var out []string
	for _, t := range it.Terms {
		if t.Domain == domain {
			out = append(out, t.Nicename)
		}
	}
	return out
}

type xmlEncoded struct {
	XMLName xml.Name
	Value   string `xml:",chardata"`
}

type xmlMeta struct {
	Key   string `xml:"meta_key"`
	Value string `xml:"meta_value"`
}

type xmlItemTerm struct {
	Domain   string `xml:"domain,attr"`
	Nicename string `xml:"nicename,attr"`
	Name     string `xml:",chardata"`
}

type xmlItem struct {
	Title         string        `xml:"title"`
	Link          string        `xml:"link"`
	Creator       string        `xml:"creator"`
	Encoded       []xmlEncoded  `xml:"encoded"`
	PostID        int           `xml:"post_id"`
	PostDate      string        `xml:"post_date"`
	PostDateGMT   string        `xml:"post_date_gmt"`
	ModifiedGMT   string        `xml:"post_modified_gmt"`
	Name          string        `xml:"post_name"`
	Status        string        `xml:"status"`
	Parent        int           `xml:"post_parent"`
	MenuOrder     int           `xml:"menu_order"`
	Type          string        `xml:"post_type"`
	AttachmentURL string        `xml:"attachment_url"`
	Categories    []xmlItemTerm `xml:"category"`
	Meta          []xmlMeta     `xml:"postmeta"`
}

type xmlChannel struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Language    string `xml:"language"`
	Version     string `xml:"wxr_version"`
	BaseSiteURL string `xml:"base_site_url"`
	BaseBlogURL string `xml:"base_blog_url"`
	Generator   string `xml:"generator"`
	Authors     []struct {
		ID          int    `xml:"author_id"`
		Login       string `xml:"author_login"`
		Email       string `xml:"author_email"`
		DisplayName string `xml:"author_display_name"`
	} `xml:"author"`
	Categories []struct {
		ID          int    `xml:"term_id"`
		Nicename    string `xml:"category_nicename"`
		Parent      string `xml:"category_parent"`
		Name        string `xml:"cat_name"`
		Description string `xml:"category_description"`
	} `xml:"category"`
	Tags []struct {
		ID          int    `xml:"term_id"`
		Slug        string `xml:"tag_slug"`
		Name        string `xml:"tag_name"`
		Description string `xml:"tag_description"`
	} `xml:"tag"`
	WPTerms []struct {
		ID          int    `xml:"term_id"`
		Taxonomy    string `xml:"term_taxonomy"`
		Slug        string `xml:"term_slug"`
		Parent      string `xml:"term_parent"`
		Name        string `xml:"term_name"`
		Description string `xml:"term_description"`
	} `xml:"term"`
	Items []xmlItem `xml:"item"`
}

// Parse reads a WXR document.
func Parse(r io.Reader) (*File, error) {
	var doc struct {
		Channel xmlChannel `xml:"channel"`
	}
	dec := xml.NewDecoder(r)
	dec.Strict = false
	dec.CharsetReader = func(charset string, in io.Reader) (io.Reader, error) {
		if strings.EqualFold(charset, "utf-8") || charset == "" {
			return in, nil
		}
		return nil, fmt.Errorf("wxr: unsupported charset %q", charset)
	}
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("wxr: %w", err)
	}
	ch := doc.Channel
	if ch.Version == "" && ch.BaseSiteURL == "" {
		return nil, fmt.Errorf("wxr: not a WordPress export (no wp:wxr_version)")
	}
	f := &File{Title: ch.Title, Link: ch.Link, Language: ch.Language, BaseSiteURL: ch.BaseSiteURL,
		BaseBlogURL: ch.BaseBlogURL, Generator: ch.Generator, Version: ch.Version}
	for _, a := range ch.Authors {
		f.Authors = append(f.Authors, Author{ID: a.ID, Login: a.Login, Email: a.Email, DisplayName: a.DisplayName})
	}
	for _, c := range ch.Categories {
		f.Terms = append(f.Terms, Term{ID: c.ID, Taxonomy: "category", Slug: c.Nicename, Name: c.Name, Parent: c.Parent, Description: c.Description})
	}
	for _, t := range ch.Tags {
		f.Terms = append(f.Terms, Term{ID: t.ID, Taxonomy: "post_tag", Slug: t.Slug, Name: t.Name, Description: t.Description})
	}
	for _, t := range ch.WPTerms {
		f.Terms = append(f.Terms, Term{ID: t.ID, Taxonomy: t.Taxonomy, Slug: t.Slug, Name: t.Name, Parent: t.Parent, Description: t.Description})
	}
	for _, x := range ch.Items {
		it := Item{ID: x.PostID, Title: x.Title, Link: x.Link, Creator: x.Creator, PostDate: x.PostDate, PostDateGMT: x.PostDateGMT,
			ModifiedGMT: x.ModifiedGMT, Name: x.Name, Status: x.Status, Parent: x.Parent, MenuOrder: x.MenuOrder,
			Type: x.Type, AttachmentURL: strings.TrimSpace(x.AttachmentURL), Meta: map[string]string{}}
		for _, e := range x.Encoded {
			switch {
			case strings.Contains(e.XMLName.Space, "excerpt"):
				it.Excerpt = e.Value
			case strings.Contains(e.XMLName.Space, "content"):
				it.Content = e.Value
			}
		}
		for _, c := range x.Categories {
			it.Terms = append(it.Terms, ItemTerm(c))
		}
		for _, m := range x.Meta {
			it.Meta[m.Key] = m.Value
		}
		f.Items = append(f.Items, it)
	}
	return f, nil
}

// ItemsOfType returns the items with post_type typ.
func (f *File) ItemsOfType(typ string) []Item {
	var out []Item
	for _, it := range f.Items {
		if it.Type == typ {
			out = append(out, it)
		}
	}
	return out
}
