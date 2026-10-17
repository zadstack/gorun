package wpmigrate

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zadstack/gorun/internal/wpmigrate/wxr"
)

// wxrSource reads a parsed WXR export. It exposes postmeta (MCBB product
// mapping, Yoast fields) that the public REST API hides.
type wxrSource struct {
	f        *wxr.File
	currency string
	termIDs  map[string]int // taxonomy + "\x00" + slug → term id
}

// wxrCache keeps parsed exports in memory, keyed by path and invalidated
// when the file's size or modification time changes (a new upload gets a
// new path), so one run parses a 200 MB export once instead of per step.
type wxrCache struct {
	mu      sync.Mutex
	entries map[string]wxrEntry
	order   []string
	parses  atomic.Int64
}

type wxrEntry struct {
	size  int64
	mtime time.Time
	file  *wxr.File
}

const wxrCacheSize = 4

func newWXRCache() *wxrCache { return &wxrCache{entries: map[string]wxrEntry{}} }

func (c *wxrCache) get(path string) (*wxr.File, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSource, err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[path]; ok && e.size == fi.Size() && e.mtime.Equal(fi.ModTime()) {
		return e.file, nil
	}
	fh, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSource, err)
	}
	defer fh.Close()
	f, err := wxr.Parse(fh)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSource, err)
	}
	c.parses.Add(1)
	if _, ok := c.entries[path]; !ok {
		c.order = append(c.order, path)
		if len(c.order) > wxrCacheSize {
			delete(c.entries, c.order[0])
			c.order = c.order[1:]
		}
	}
	c.entries[path] = wxrEntry{size: fi.Size(), mtime: fi.ModTime(), file: f}
	return f, nil
}

// evict forgets a cached export (end of a run).
func (c *wxrCache) evict(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, path)
	for i, p := range c.order {
		if p == path {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
}

func (c *wxrCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// openWXR parses an export without caching (upload validation).
func openWXR(path, currency string) (*wxrSource, error) {
	f, err := newWXRCache().get(path)
	if err != nil {
		return nil, err
	}
	return newWXRSource(f, currency), nil
}

func newWXRSource(f *wxr.File, currency string) *wxrSource {
	s := &wxrSource{f: f, currency: currency, termIDs: map[string]int{}}
	for _, t := range f.Terms {
		s.termIDs[t.Taxonomy+"\x00"+t.Slug] = t.ID
	}
	return s
}

func (s *wxrSource) Kind() string { return "wxr" }

var wxrGenerator = regexp.MustCompile(`\?v=([^&\s]+)`)

func (s *wxrSource) Site(context.Context) (SiteInfo, error) {
	si := SiteInfo{Name: s.f.Title, URL: firstNonEmpty(s.f.BaseBlogURL, s.f.Link, s.f.BaseSiteURL), Home: s.f.Link,
		Language: s.f.Language}
	if m := wxrGenerator.FindStringSubmatch(s.f.Generator); m != nil {
		si.Version = m[1]
	}
	return si, nil
}

func (s *wxrSource) Users(context.Context) ([]WPUser, error) {
	var out []WPUser
	for _, a := range s.f.Authors {
		out = append(out, WPUser{ID: a.ID, Login: a.Login, Slug: a.Login, Name: a.DisplayName, Email: strings.TrimSpace(a.Email)})
	}
	return out, nil
}

// authorID maps dc:creator (login) to the author id.
func (s *wxrSource) authorID(login string) int {
	for _, a := range s.f.Authors {
		if a.Login == login {
			return a.ID
		}
	}
	return 0
}

func (s *wxrSource) Terms(_ context.Context, taxonomies []string) ([]WPTerm, error) {
	want := map[string]bool{}
	for _, t := range taxonomies {
		want[t] = true
	}
	var out []WPTerm
	for _, t := range s.f.Terms {
		if !want[t.Taxonomy] {
			continue
		}
		out = append(out, WPTerm{ID: t.ID, Taxonomy: t.Taxonomy, Slug: t.Slug, Name: t.Name, Description: t.Description,
			Parent: s.termIDs[t.Taxonomy+"\x00"+t.Parent]})
	}
	return out, nil
}

func (s *wxrSource) Media(context.Context) ([]WPMedia, error) {
	var out []WPMedia
	for _, it := range s.f.ItemsOfType("attachment") {
		if it.AttachmentURL == "" {
			continue
		}
		out = append(out, WPMedia{ID: it.ID, URL: it.AttachmentURL, Slug: it.Name, Alt: it.Meta["_wp_attachment_image_alt"],
			Caption: plainText(it.Excerpt), Title: it.Title, Modified: it.ModifiedGMT})
	}
	return append(out, s.inlineImages(out)...), nil
}

// wxrSkipTypes are item types that are not content.
var wxrSkipTypes = map[string]bool{"attachment": true, "nav_menu_item": true, "product": true, "product_variation": true,
	"shop_order": true, "shop_coupon": true, "wp_block": true, "wp_template": true, "wp_template_part": true,
	"wp_global_styles": true, "wp_navigation": true, "custom_css": true, "customize_changeset": true, "oembed_cache": true}

func (s *wxrSource) Content(_ context.Context, types []string) ([]WPContent, error) {
	want := map[string]bool{}
	for _, t := range types {
		want[t] = true
	}
	var out []WPContent
	for _, it := range s.f.Items {
		if !want[it.Type] || wxrSkipTypes[it.Type] {
			continue
		}
		c := WPContent{ID: it.ID, Type: it.Type, Slug: it.Name, Title: it.Title, Link: it.Link, Status: it.Status,
			Date: wxrDate(it), Modified: it.ModifiedGMT, Raw: it.Content, Excerpt: plainText(it.Excerpt), Parent: it.Parent,
			MenuOrder: it.MenuOrder, Author: s.authorID(it.Creator), FeaturedMedia: it.MetaInt("_thumbnail_id"),
			Terms: map[string][]int{}, Meta: map[string]any{}}
		if tpl := it.Meta["_wp_page_template"]; tpl != "" && tpl != "default" {
			c.Template = tpl
		}
		for _, t := range it.Terms {
			if id, ok := s.termIDs[t.Domain+"\x00"+t.Nicename]; ok {
				c.Terms[t.Domain] = append(c.Terms[t.Domain], id)
			}
		}
		for k, v := range it.Meta {
			if !strings.HasPrefix(k, "_") {
				c.Meta[k] = v
			}
		}
		c.Yoast = yoastFromMeta(it.Meta)
		out = append(out, c)
	}
	return out, nil
}

// yoastFromMeta rebuilds the yoast_head_json subset GoRun maps from the
// _yoast_wpseo_* postmeta of an export.
func yoastFromMeta(meta map[string]string) map[string]any {
	y := map[string]any{}
	if v := meta["_yoast_wpseo_title"]; v != "" {
		y["title"] = v
	}
	if v := meta["_yoast_wpseo_metadesc"]; v != "" {
		y["description"] = v
	}
	if v := meta["_yoast_wpseo_canonical"]; v != "" {
		y["canonical"] = v
	}
	if meta["_yoast_wpseo_meta-robots-noindex"] == "1" {
		y["robots"] = map[string]any{"index": "noindex", "follow": "follow"}
	}
	if len(y) == 0 {
		return nil
	}
	return y
}

func (s *wxrSource) Products(context.Context) ([]WPProduct, error) {
	att := map[int]string{}
	for _, it := range s.f.ItemsOfType("attachment") {
		att[it.ID] = it.AttachmentURL
	}
	names := map[string]string{}
	for _, t := range s.f.Terms {
		if t.Taxonomy == "product_cat" {
			names[t.Slug] = t.Name
		}
	}
	var out []WPProduct
	for _, it := range s.f.ItemsOfType("product") {
		p := WPProduct{ID: it.ID, Slug: decodeSlug(it.Name), Name: it.Title, Description: it.Content,
			ShortDescription: it.Excerpt, Currency: s.currency, Meta: map[string]string{}}
		p.PriceMinor, _ = decimalToMinor(firstNonEmpty(it.Meta["_price"], it.Meta["_regular_price"]), minorUnits(s.currency))
		for _, t := range it.Terms {
			if t.Domain == "product_cat" {
				p.Categories = append(p.Categories, firstNonEmpty(names[t.Nicename], t.Name, t.Nicename))
			}
		}
		if u := att[it.MetaInt("_thumbnail_id")]; u != "" {
			p.ImageURLs = append(p.ImageURLs, u)
		}
		for _, id := range strings.Split(it.Meta["_product_image_gallery"], ",") {
			n, _ := strconv.Atoi(strings.TrimSpace(id))
			if u := att[n]; u != "" {
				p.ImageURLs = append(p.ImageURLs, u)
			}
		}
		for k, v := range it.Meta {
			p.Meta[k] = v
		}
		out = append(out, p)
	}
	return out, nil
}

// decimalToMinor converts "11990.50" to 1199050 with exact decimal
// arithmetic (no float rounding).
func decimalToMinor(s string, minorUnit int) (int64, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	if s == "" {
		return 0, nil
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return 0, fmt.Errorf("invalid price %q", s)
	}
	scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(minorUnit)), nil))
	r.Mul(r, scale)
	n := new(big.Int).Quo(r.Num(), r.Denom())
	return n.Int64(), nil
}

func (s *wxrSource) Menus(context.Context) ([]WPMenu, error) {
	bySlug := map[string]*WPMenu{}
	var order []string
	for _, t := range s.f.Terms {
		if t.Taxonomy == "nav_menu" {
			bySlug[t.Slug] = &WPMenu{ID: t.ID, Slug: decodeSlug(t.Slug), Name: t.Name}
			order = append(order, t.Slug)
		}
	}
	for _, it := range s.f.ItemsOfType("nav_menu_item") {
		menus := it.TermsIn("nav_menu")
		if len(menus) == 0 {
			continue
		}
		m := bySlug[menus[0]]
		if m == nil {
			m = &WPMenu{Slug: decodeSlug(menus[0]), Name: menus[0]}
			bySlug[menus[0]] = m
			order = append(order, menus[0])
		}
		m.Items = append(m.Items, WPMenuItem{ID: it.ID, Parent: it.MetaInt("_menu_item_menu_item_parent"), Order: it.MenuOrder,
			Title: it.Title, URL: it.Meta["_menu_item_url"], Type: it.Meta["_menu_item_type"], Object: it.Meta["_menu_item_object"],
			ObjectID: it.MetaInt("_menu_item_object_id")})
	}
	out := make([]WPMenu, 0, len(order))
	for _, slug := range order {
		out = append(out, *bySlug[slug])
	}
	return out, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// wxrDate prefers post_date_gmt (UTC) over the site-local post_date.
func wxrDate(it wxr.Item) string {
	if g := strings.TrimSpace(it.PostDateGMT); g != "" && !strings.HasPrefix(g, "0000") {
		return g
	}
	return it.PostDate
}

// minorUnits is the number of decimals of an ISO 4217 currency (WooCommerce
// prices in a WXR export are decimal strings; 2 when unknown).
func minorUnits(currency string) int {
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "BIF", "CLP", "DJF", "GNF", "ISK", "JPY", "KMF", "KRW", "PYG", "RWF", "UGX", "UYI", "VND", "VUV", "XAF", "XOF", "XPF":
		return 0
	case "BHD", "IQD", "JOD", "KWD", "LYD", "OMR", "TND":
		return 3
	}
	return 2
}
