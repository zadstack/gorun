package wpmigrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// restSource reads the WordPress REST API. With credentials it asks for
// context=edit (raw block markup, emails, menus). Credential failures never
// fall back to a less complete public collection.
type restSource struct {
	c *Client

	once     sync.Once
	types    map[string]wpType // slug → type
	taxes    map[string]wpType // slug → taxonomy
	metaErr  error
	idx      map[string]any
	idxErr   error
	idxOnce  sync.Once
	authOnce sync.Once
	authOK   bool
	authErr  error
	// warn reports differences between readable rows and source header bounds.
	warn func(msg string, meta map[string]any)
}

type wpType struct {
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	RestBase      string `json:"rest_base"`
	RestNamespace string `json:"rest_namespace"`
}

func (t wpType) path() string {
	ns := t.RestNamespace
	if ns == "" {
		ns = "wp/v2"
	}
	return "/" + ns + "/" + t.RestBase
}

// internalTypes are WordPress post types that are never content.
var internalTypes = map[string]bool{"attachment": true, "nav_menu_item": true, "wp_block": true, "wp_template": true,
	"wp_template_part": true, "wp_global_styles": true, "wp_navigation": true, "wp_font_family": true, "wp_font_face": true}

func newRESTSource(c *Client) *restSource { return &restSource{c: c} }

func (s *restSource) Kind() string { return "rest" }

func (s *restSource) index(ctx context.Context) (map[string]any, error) {
	s.idxOnce.Do(func() {
		var idx map[string]any
		if _, err := s.c.GetJSON(ctx, "/", nil, &idx); err != nil {
			s.idxErr = err
			return
		}
		if _, ok := idx["namespaces"]; !ok {
			s.idxErr = fmt.Errorf("%w: /wp-json/ has no namespaces", ErrNotWordPress)
			return
		}
		s.idx = idx
	})
	return s.idx, s.idxErr
}

func (s *restSource) meta(ctx context.Context) error {
	s.authed(ctx)
	if s.authErr != nil {
		return s.authErr
	}
	s.once.Do(func() {
		s.types, s.taxes = map[string]wpType{}, map[string]wpType{}
		var types, taxes map[string]wpType
		if _, err := s.c.GetJSON(ctx, "/wp/v2/types", nil, &types); err != nil {
			s.metaErr = err
			return
		}
		if _, err := s.c.GetJSON(ctx, "/wp/v2/taxonomies", nil, &taxes); err != nil {
			s.metaErr = err
			return
		}
		for k, v := range types {
			if v.Slug == "" {
				v.Slug = k
			}
			s.types[v.Slug] = v
		}
		for k, v := range taxes {
			if v.Slug == "" {
				v.Slug = k
			}
			s.taxes[v.Slug] = v
		}
	})
	return s.metaErr
}

// authed reports whether credentials work (cached).
func (s *restSource) authed(ctx context.Context) bool {
	s.authOnce.Do(func() {
		if !s.c.HasAuth() {
			return
		}
		_, err := s.c.Get(ctx, "/wp/v2/users/me", url.Values{"context": {"edit"}})
		s.authOK = err == nil
		if err != nil {
			s.authErr = err
		}
	})
	return s.authOK
}

const contentStatuses = "publish,draft,pending,future,private"

func contentQuery(authed bool) url.Values {
	if authed {
		return url.Values{"context": {"edit"}, "status": {contentStatuses}, "orderby": {"id"}, "order": {"asc"}}
	}
	return url.Values{"status": {"publish"}, "orderby": {"id"}, "order": {"asc"}}
}

// all walks a collection without retrying denied edit access as public access.
func (s *restSource) all(ctx context.Context, path string, q url.Values, fn func(json.RawMessage) error) error {
	qq := cloneValues(q)
	if s.authed(ctx) {
		qq.Set("context", "edit")
	}
	if s.authErr != nil {
		return s.authErr
	}
	read, err := s.c.ReadCollection(ctx, path, qq, 0, fn)
	if err == nil && s.warn != nil && (!read.CountKnown || read.Visible != read.ReportedTotal) {
		s.warn("collection_permission_bounds", map[string]any{"path": path, "collection": read})
	}
	return err
}

var generatorRe = regexp.MustCompile(`<generator>https?://wordpress\.org/\?v=([^<\s]+)</generator>`)
var languageRe = regexp.MustCompile(`<language>([^<]+)</language>`)

func (s *restSource) Site(ctx context.Context) (SiteInfo, error) {
	s.authed(ctx)
	if s.authErr != nil {
		return SiteInfo{}, s.authErr
	}
	idx, err := s.index(ctx)
	if err != nil {
		return SiteInfo{}, err
	}
	str := func(k string) string { v, _ := idx[k].(string); return v }
	num := func(k string) int { v, _ := idx[k].(float64); return int(v) }
	si := SiteInfo{Name: plainText(str("name")), URL: str("url"), Home: str("home"), Description: plainText(str("description")),
		FrontPageID: num("page_on_front"), PostsPageID: num("page_for_posts")}
	if si.URL == "" {
		si.URL = s.c.SiteURL()
	}
	if str("show_on_front") != "page" && str("show_on_front") != "" {
		si.FrontPageID = 0
	}
	if s.authed(ctx) {
		var st struct {
			Title       string `json:"title"`
			Description string `json:"description"`
		}
		if _, err := s.c.GetJSON(ctx, "/wp/v2/settings", nil, &st); err == nil {
			si.Name, si.Description, si.FromSettings = plainText(st.Title), plainText(st.Description), true
		} else if !optionalRESTUnavailable(err) {
			return SiteInfo{}, err
		}
	}
	if resp, err := s.c.GetURL(ctx, s.c.SiteURL()+"/feed/"); err == nil {
		if m := generatorRe.FindSubmatch(resp.Body); m != nil {
			si.Version = string(m[1])
		}
		if m := languageRe.FindSubmatch(resp.Body); m != nil {
			si.Language = strings.TrimSpace(string(m[1]))
		}
	} else if !optionalRESTUnavailable(err) {
		return SiteInfo{}, err
	}
	return si, nil
}

func (s *restSource) Users(ctx context.Context) ([]WPUser, error) {
	var out []WPUser
	q := url.Values{}
	if s.authed(ctx) {
		q.Set("context", "edit")
	}
	if s.authErr != nil {
		return nil, s.authErr
	}
	_, err := s.c.All(ctx, "/wp/v2/users", q, 0, func(r json.RawMessage) error {
		var u struct {
			ID                          int
			Name, Slug, Email, Username string
		}
		if err := json.Unmarshal(r, &u); err != nil {
			return err
		}
		out = append(out, WPUser{ID: u.ID, Login: u.Username, Slug: u.Slug, Name: u.Name, Email: strings.TrimSpace(u.Email)})
		return nil
	})
	if isForbidden(err) {
		return nil, fmt.Errorf("%w: users: %v", ErrUnavailable, err)
	}
	return out, err
}

func (s *restSource) Terms(ctx context.Context, taxonomies []string) ([]WPTerm, error) {
	if err := s.meta(ctx); err != nil {
		return nil, err
	}
	var out []WPTerm
	for _, tax := range taxonomies {
		t, ok := s.taxes[tax]
		if !ok || t.RestBase == "" {
			continue
		}
		_, err := s.c.All(ctx, t.path(), nil, 0, func(r json.RawMessage) error {
			var x struct {
				ID, Parent, Count       int
				Slug, Name, Description string
			}
			if err := json.Unmarshal(r, &x); err != nil {
				return err
			}
			out = append(out, WPTerm{ID: x.ID, Taxonomy: tax, Slug: x.Slug, Name: plainText(x.Name),
				Description: x.Description, Parent: x.Parent, Count: x.Count})
			return nil
		})
		if err != nil && !isForbidden(err) {
			return nil, err
		}
	}
	return out, nil
}

type rendered struct {
	Rendered string `json:"rendered"`
	Raw      string `json:"raw"`
}

func (r rendered) text() string {
	if r.Raw != "" {
		return r.Raw
	}
	return plainText(r.Rendered)
}

func (s *restSource) Media(ctx context.Context) ([]WPMedia, error) {
	var out []WPMedia
	err := s.all(ctx, "/wp/v2/media", nil, func(r json.RawMessage) error {
		var m struct {
			ID           int
			Slug         string
			SourceURL    string   `json:"source_url"`
			MimeType     string   `json:"mime_type"`
			AltText      string   `json:"alt_text"`
			Caption      rendered `json:"caption"`
			Title        rendered `json:"title"`
			Modified     string   `json:"modified_gmt"`
			MediaDetails struct {
				Filesize int64 `json:"filesize"`
				Sizes    map[string]struct {
					SourceURL string `json:"source_url"`
				} `json:"sizes"`
			} `json:"media_details"`
		}
		if err := json.Unmarshal(r, &m); err != nil {
			return err
		}
		wm := WPMedia{ID: m.ID, URL: m.SourceURL, Slug: m.Slug, Mime: m.MimeType, Alt: m.AltText,
			Caption: plainText(m.Caption.Rendered), Title: m.Title.text(), Modified: m.Modified, Bytes: m.MediaDetails.Filesize}
		for _, sz := range m.MediaDetails.Sizes {
			if sz.SourceURL != "" {
				wm.Sizes = append(wm.Sizes, sz.SourceURL)
			}
		}
		out = append(out, wm)
		return nil
	})
	return out, err
}

func (s *restSource) Content(ctx context.Context, types []string) ([]WPContent, error) {
	if err := s.meta(ctx); err != nil {
		return nil, err
	}
	site, err := s.Site(ctx)
	if err != nil {
		return nil, err
	}
	restToTax := map[string]string{}
	for slug, t := range s.taxes {
		if t.RestBase != "" {
			restToTax[t.RestBase] = slug
		}
	}
	var out []WPContent
	for _, typ := range types {
		t, ok := s.types[typ]
		if !ok || t.RestBase == "" || internalTypes[typ] {
			return nil, fmt.Errorf("%w: selected content type %s has no supported REST collection", ErrUnavailable, typ)
		}
		err := s.all(ctx, t.path(), contentQuery(s.c.HasAuth()), func(r json.RawMessage) error {
			c, err := decodeRESTContent(r, typ, restToTax)
			if err != nil {
				return err
			}
			if err := validateRESTContentState(c, s.c.HasAuth()); err != nil {
				return err
			}
			c.IsFront = typ == "page" && site.FrontPageID != 0 && c.ID == site.FrontPageID
			out = append(out, c)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func decodeRESTContent(r json.RawMessage, typ string, restToTax map[string]string) (WPContent, error) {
	var x struct {
		ID            int            `json:"id"`
		Slug          string         `json:"slug"`
		Link          string         `json:"link"`
		Status        string         `json:"status"`
		DateGMT       string         `json:"date_gmt"`
		ModifiedGMT   string         `json:"modified_gmt"`
		Title         rendered       `json:"title"`
		Content       rendered       `json:"content"`
		Excerpt       rendered       `json:"excerpt"`
		Template      string         `json:"template"`
		Parent        int            `json:"parent"`
		MenuOrder     int            `json:"menu_order"`
		Author        int            `json:"author"`
		FeaturedMedia int            `json:"featured_media"`
		Meta          any            `json:"meta"`
		Yoast         map[string]any `json:"yoast_head_json"`
		ACF           any            `json:"acf"`
	}
	if err := json.Unmarshal(r, &x); err != nil {
		return WPContent{}, err
	}
	c := WPContent{ID: x.ID, Type: typ, Slug: x.Slug, Title: x.Title.text(), Link: x.Link, Status: x.Status,
		Date: x.DateGMT, Modified: x.ModifiedGMT, Raw: x.Content.Raw, Rendered: x.Content.Rendered,
		Excerpt: plainText(x.Excerpt.Rendered), Template: x.Template, Parent: x.Parent, MenuOrder: x.MenuOrder,
		Author: x.Author, FeaturedMedia: x.FeaturedMedia, Yoast: x.Yoast, Terms: map[string][]int{}}
	if m, ok := x.Meta.(map[string]any); ok {
		c.Meta = m
	}
	if m, ok := x.ACF.(map[string]any); ok {
		c.ACF = m
	}
	var all map[string]json.RawMessage
	_ = json.Unmarshal(r, &all)
	for base, tax := range restToTax {
		raw, ok := all[base]
		if !ok {
			continue
		}
		var ids []int
		if json.Unmarshal(raw, &ids) == nil && len(ids) > 0 {
			c.Terms[tax] = ids
		}
	}
	return c, nil
}

func (s *restSource) Products(ctx context.Context) ([]WPProduct, error) {
	s.authed(ctx)
	if s.authErr != nil {
		return nil, s.authErr
	}
	var out []WPProduct
	_, err := s.c.All(ctx, "/wc/store/v1/products", nil, 0, func(r json.RawMessage) error {
		p, err := decodeStoreProduct(r)
		if err != nil {
			return err
		}
		out = append(out, p)
		return nil
	})
	if err != nil {
		var se *StatusError
		if asStatus(err, &se) && se.Status == 404 {
			return nil, fmt.Errorf("%w: WooCommerce Store API not found", ErrUnavailable)
		}
		return nil, err
	}
	if s.authed(ctx) {
		meta := map[int]map[string]string{}
		_, verr := s.c.All(ctx, "/wc/v3/products", nil, 0, func(r json.RawMessage) error {
			var x struct {
				ID       int `json:"id"`
				MetaData []struct {
					Key   string `json:"key"`
					Value any    `json:"value"`
				} `json:"meta_data"`
			}
			if err := json.Unmarshal(r, &x); err != nil {
				return err
			}
			m := map[string]string{}
			for _, md := range x.MetaData {
				if v, ok := md.Value.(string); ok {
					m[md.Key] = v
				} else if f, ok := md.Value.(float64); ok {
					m[md.Key] = strconv.FormatFloat(f, 'f', -1, 64)
				}
			}
			meta[x.ID] = m
			return nil
		})
		if verr == nil {
			for i := range out {
				if m, ok := meta[out[i].ID]; ok {
					out[i].Meta = m
				}
			}
		} else if !optionalRESTUnavailable(verr) {
			return nil, verr
		}
	}
	return out, nil
}

// decodeStoreProduct maps a Store API product. prices.price is already in
// minor units (currency_minor_unit decimals).
func decodeStoreProduct(r json.RawMessage) (WPProduct, error) {
	var x struct {
		ID               int    `json:"id"`
		Name             string `json:"name"`
		Slug             string `json:"slug"`
		Description      string `json:"description"`
		ShortDescription string `json:"short_description"`
		Prices           struct {
			Price        string `json:"price"`
			CurrencyCode string `json:"currency_code"`
		} `json:"prices"`
		Images []struct {
			Src string `json:"src"`
		} `json:"images"`
		Categories []struct {
			Name string `json:"name"`
			Slug string `json:"slug"`
		} `json:"categories"`
	}
	if err := json.Unmarshal(r, &x); err != nil {
		return WPProduct{}, err
	}
	price, _ := strconv.ParseInt(strings.TrimSpace(x.Prices.Price), 10, 64)
	p := WPProduct{ID: x.ID, Slug: decodeSlug(x.Slug), Name: plainText(x.Name), Description: x.Description,
		ShortDescription: x.ShortDescription, PriceMinor: price, Currency: x.Prices.CurrencyCode, Meta: map[string]string{}}
	for _, c := range x.Categories {
		p.Categories = append(p.Categories, plainText(c.Name))
	}
	for _, im := range x.Images {
		if im.Src != "" {
			p.ImageURLs = append(p.ImageURLs, im.Src)
		}
	}
	return p, nil
}

func (s *restSource) Menus(ctx context.Context) ([]WPMenu, error) {
	var menus []struct {
		ID        int      `json:"id"`
		Name      string   `json:"name"`
		Slug      string   `json:"slug"`
		Locations []string `json:"locations"`
	}
	if _, err := s.c.GetJSON(ctx, "/wp/v2/menus", url.Values{"per_page": {"100"}}, &menus); err != nil {
		if isForbidden(err) {
			return nil, fmt.Errorf("%w: menus need an application password", ErrUnavailable)
		}
		return nil, err
	}
	var out []WPMenu
	for _, m := range menus {
		wm := WPMenu{ID: m.ID, Slug: decodeSlug(m.Slug), Name: plainText(m.Name)}
		if len(m.Locations) > 0 {
			wm.Location = m.Locations[0]
		}
		_, err := s.c.All(ctx, "/wp/v2/menu-items", url.Values{"menus": {strconv.Itoa(m.ID)}}, 0, func(r json.RawMessage) error {
			var it struct {
				ID        int      `json:"id"`
				Title     rendered `json:"title"`
				URL       string   `json:"url"`
				Parent    int      `json:"parent"`
				MenuOrder int      `json:"menu_order"`
				Object    string   `json:"object"`
				ObjectID  int      `json:"object_id"`
				Type      string   `json:"type"`
			}
			if err := json.Unmarshal(r, &it); err != nil {
				return err
			}
			wm.Items = append(wm.Items, WPMenuItem{ID: it.ID, Parent: it.Parent, Order: it.MenuOrder, Title: it.Title.text(),
				URL: it.URL, Type: it.Type, Object: it.Object, ObjectID: it.ObjectID})
			return nil
		})
		if err != nil {
			return nil, err
		}
		out = append(out, wm)
	}
	return out, nil
}

func asStatus(err error, se **StatusError) bool { return errors.As(err, se) }
