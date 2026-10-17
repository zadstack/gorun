package wpmigrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/wpmigrate/gutenberg"
	"github.com/zadstack/gorun/internal/wpmigrate/wxr"
)

// Discover scans a source and stores the report on the source (and on
// every draft/scanned migration of it). Wrong credentials give a report
// with Auth.Error "auth_failed" and ErrAuthFailed — never a silent
// anonymous scan.
func (s *service) Discover(ctx context.Context, sourceID string) (*Report, error) {
	src, err := s.getSource(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	var rep *Report
	switch src.Kind {
	case "wxr":
		rep, err = s.discoverWXR(src)
	default:
		var c *Client
		if c, err = s.client(src, Options{}); err == nil {
			rep, err = discoverREST(ctx, c)
		}
	}
	if rep == nil {
		return nil, err
	}
	rep.GeneratedAt = db.Now()
	b, _ := json.Marshal(rep)
	if _, dbErr := s.db.ExecContext(ctx, `UPDATE wp_sources SET last_discovery_json = ?, last_discovery_at = ? WHERE id = ?`,
		string(b), rep.GeneratedAt, sourceID); dbErr != nil {
		return nil, dbErr
	}
	if f, _ := rep.Find("products"); f != nil {
		if _, ok := s.importer(); !ok {
			f.Note = labels["products_no_commerce"][1]
			b, _ = json.Marshal(rep)
			_, _ = s.db.ExecContext(ctx, `UPDATE wp_sources SET last_discovery_json = ? WHERE id = ?`, string(b), sourceID)
		}
	}
	if err == nil {
		if _, dbErr := s.db.ExecContext(ctx, `UPDATE migrations SET discovery_json = ?, status = 'scanned', updated_at = ?
			WHERE source_id = ? AND status IN ('draft', 'scanned')`, string(b), db.Now(), sourceID); dbErr != nil {
			return nil, dbErr
		}
	}
	s.audit.Record(ctx, "wpmigrate.discover", "wp_source", sourceID, map[string]any{"auth": rep.Auth.Mode, "ok": err == nil})
	return rep, err
}

// client builds the REST client of a source; its requests share the
// source's rate limiter (rate from opts, the setting or the default).
func (s *service) client(src *sourceRow, opts Options) (*Client, error) {
	pw, err := s.password(src)
	if err != nil {
		return nil, err
	}
	c, err := NewClient(src.URL, src.Username, pw, s.http)
	if err != nil {
		return nil, err
	}
	if s.clientBackoff >= 0 {
		c.backoff = s.clientBackoff
	}
	c.limit = s.limiter(src.ID, s.rate(opts)).wait
	return c, nil
}

// openSourceFor is openSource whose degraded reads are logged on m.
func (s *service) openSourceFor(ctx context.Context, m *migrationRow, src *sourceRow) (Source, error) {
	source, err := s.openSource(src, m.Options)
	if rs, ok := source.(*restSource); ok {
		rs.warn = func(msg string, meta map[string]any) {
			s.log.Warn("wpmigrate: "+msg, "migration", m.ID, "meta", meta)
			s.logEntry(context.WithoutCancel(ctx), m.ID, "warn", "", msg, meta)
		}
	}
	return source, err
}

// openSource returns the Source of a source row.
func (s *service) openSource(src *sourceRow, opts Options) (Source, error) {
	if src.Kind == "wxr" {
		f, err := s.wxr.get(src.filePath)
		if err != nil {
			return nil, err
		}
		return newWXRSource(f, opts.Currency), nil
	}
	c, err := s.client(src, opts)
	if err != nil {
		return nil, err
	}
	return newRESTSource(c), nil
}

// discoverREST builds the report from the REST API.
func discoverREST(ctx context.Context, c *Client) (rep *Report, err error) {
	rep = newReport("rest")
	defer func() {
		if rep != nil && errors.Is(err, ErrAuthFailed) {
			rep.Auth.OK = false
			rep.Auth.Error = "auth_failed"
		}
		if rep != nil && errors.Is(err, ErrAuthUnavailable) {
			rep.Auth.OK = false
			rep.Auth.Error = "auth_unavailable"
		}
	}()
	src := newRESTSource(c)
	if c.HasAuth() {
		// WordPress rejects every request carrying a wrong application
		// password, so probe the credentials before anything else.
		rep.Auth.Mode = "app_password"
		if _, err := c.Get(ctx, "/wp/v2/users/me", url.Values{"context": {"edit"}}); err != nil {
			if errors.Is(err, ErrAuthUnavailable) {
				return rep, err
			}
			if !IsAuthError(err) {
				return nil, err
			}
			rep.Auth.OK, rep.Auth.Error = false, "auth_failed"
			return rep, ErrAuthFailed
		}
		src.authOnce.Do(func() { src.authOK = true })
	}
	idx, err := src.index(ctx)
	if err != nil {
		return nil, err
	}
	authed := rep.Auth.Mode == "app_password"
	site, err := src.Site(ctx)
	if err != nil {
		return rep, err
	}
	rep.Site = SiteSummary{Name: site.Name, URL: site.URL, Version: site.Version, Locale: site.Language}
	ns := map[string]bool{}
	if list, ok := idx["namespaces"].([]any); ok {
		for _, n := range list {
			if v, ok := n.(string); ok {
				ns[v] = true
			}
		}
	}
	rep.Plugins["yoast"] = ns["yoast/v1"]
	rep.Plugins["rankmath"] = ns["rankmath/v1"]
	rep.Plugins["acf"] = ns["acf/v3"]
	rep.Plugins["woocommerce"] = ns["wc/v3"] || ns["wc/store/v1"]
	rep.Plugins["elementor"] = ns["elementor/v1"]
	rep.Plugins["jetpack"] = ns["jetpack/v4"]
	if site.FromSettings {
		rep.add("available", finding("settings", 1))
	} else {
		rep.add("partial", finding("settings_public", 1))
	}

	if err := src.meta(ctx); err != nil {
		return nil, err
	}
	typeSlugs := sortedKeys(src.types)
	var samples []WPContent
	rep.Collections = map[string]CollectionRead{}
	for _, slug := range typeSlugs {
		t := src.types[slug]
		if internalTypes[slug] || t.RestBase == "" {
			continue
		}
		var rows []json.RawMessage
		read, err := c.ReadCollection(ctx, t.path(), contentQuery(authed), 0, func(raw json.RawMessage) error { rows = append(rows, raw); return nil })
		rep.Collections[slug] = read
		if err != nil {
			if errors.Is(err, ErrAuthFailed) || errors.Is(err, ErrAuthUnavailable) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return rep, err
			}
			f := typeFinding("type", slug, plainText(t.Name), 0)
			f.Note = map[string]string{"en": "Collection unavailable; readable count is unknown. " + err.Error(), "ar": "المجموعة غير متاحة؛ العدد القابل للقراءة غير معروف. " + err.Error()}
			var denied *StatusError
			if errors.As(err, &denied) && (denied.Status == 403 || denied.PermissionDenied) {
				f.Note = map[string]string{"en": "WordPress denied the requested content states for this account; readable count is unknown. " + err.Error(), "ar": "رفض WordPress حالات المحتوى المطلوبة لصلاحيات هذا الحساب؛ العدد القابل للقراءة غير معروف. " + err.Error()}
			}
			rep.add("unavailable", f)
			continue
		}
		states := map[string]int{}
		var readable []WPContent
		for _, raw := range rows {
			item, decodeErr := decodeRESTContent(raw, slug, nil)
			if decodeErr == nil {
				decodeErr = validateRESTContentState(item, authed)
			}
			if decodeErr != nil {
				err = decodeErr
				break
			}
			readable = append(readable, item)
			states[item.Status]++
		}
		if err != nil {
			f := typeFinding("type", slug, plainText(t.Name), 0)
			f.Note = map[string]string{"en": "Collection payload invalid or outside requested content states; readable count is unknown.", "ar": "بيانات المجموعة غير صالحة أو خارج حالات المحتوى المطلوبة؛ العدد القابل للقراءة غير معروف."}
			rep.add("unavailable", f)
			continue
		}
		rep.Counts[slug] = read.Visible
		f := typeFinding("type", slug, plainText(t.Name), read.Visible)
		f.Note = collectionNote(read, authed)
		bucket := "available"
		if !read.CountKnown || read.Visible != read.ReportedTotal {
			bucket = "partial"
		}
		rep.add(bucket, f)
		samples = append(samples, readable...)
		rep.ContentStateCounts[slug] = states
	}
	for _, slug := range sortedKeys(src.taxes) {
		t := src.taxes[slug]
		if t.RestBase == "" || slug == "nav_menu" || slug == "wp_pattern_category" {
			continue
		}
		n, err := c.Count(ctx, t.path(), nil)
		if err != nil {
			if !optionalRESTUnavailable(err) {
				return rep, err
			}
			f := typeFinding("taxonomy", slug, plainText(t.Name), 0)
			f.Note = map[string]string{"en": "Taxonomy unavailable; count is unknown. " + err.Error(), "ar": "التصنيف غير متاح؛ العدد غير معروف. " + err.Error()}
			rep.add("unavailable", f)
			continue
		}
		rep.Counts[slug] = n
		rep.add("available", typeFinding("taxonomy", slug, plainText(t.Name), n))
	}
	if err := discoverMedia(ctx, c, rep, site); err != nil {
		return rep, err
	}
	if err := discoverUsers(ctx, c, rep, authed); err != nil {
		return rep, err
	}
	if authed {
		var menus []json.RawMessage
		if _, err := c.GetJSON(ctx, "/wp/v2/menus", url.Values{"per_page": {"100"}}, &menus); err == nil {
			rep.Counts["menus"] = len(menus)
			rep.add("available", finding("menus", len(menus)))
		} else {
			if !optionalRESTUnavailable(err) {
				return rep, err
			}
			rep.add("partial", finding("menus_auth", 0))
		}
	} else {
		rep.add("partial", finding("menus_auth", 0))
	}
	rawFinding := finding("raw_content_auth", len(samples))
	bucket := "partial"
	if authed {
		rawFinding, bucket = finding("raw_content", len(samples)), "available"
	}
	rep.add(bucket, rawFinding)
	analyzeSamples(rep, samples, rep.Plugins["woocommerce"], false)
	if rep.Plugins["woocommerce"] {
		if err := discoverProducts(ctx, c, rep, authed); err != nil {
			return rep, err
		}
	}
	addAlwaysUnavailable(rep)
	if authed {
		if _, err := c.Get(ctx, "/wp/v2/users/me", url.Values{"context": {"edit"}}); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

func newReport(kind string) *Report {
	return &Report{SourceKind: kind, Auth: AuthSummary{Mode: "anonymous", OK: true}, Counts: map[string]int{},
		Plugins: map[string]bool{}, Shortcodes: map[string]int{}, Templates: map[string]int{},
		ContentStateCounts: map[string]map[string]int{}, MetaKeys: map[string][]string{}, Available: []Finding{}, Partial: []Finding{}, Unavailable: []Finding{}}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// discoverMedia counts media and detects files served from another host
// (offload plugins / CDN) on the first pages.
func discoverMedia(ctx context.Context, c *Client, rep *Report, site SiteInfo) error {
	siteHost := hostOf(firstNonEmpty(site.URL, c.SiteURL()))
	foreign := map[string]int{}
	q := url.Values{"_fields": {"id,source_url"}}
	if c.HasAuth() {
		q.Set("context", "edit")
	}
	read, err := c.ReadCollection(ctx, "/wp/v2/media", q, 5, func(r json.RawMessage) error {
		var m struct {
			SourceURL string `json:"source_url"`
		}
		if json.Unmarshal(r, &m) == nil && m.SourceURL != "" {
			if h := hostOf(m.SourceURL); h != "" && h != siteHost {
				foreign[h]++
			}
		}
		return nil
	})
	if err != nil {
		if !optionalRESTUnavailable(err) {
			return err
		}
		f := finding("media", 0)
		f.Note = map[string]string{"en": "Media collection unavailable; count is unknown. " + err.Error(), "ar": "مجموعة الوسائط غير متاحة؛ العدد غير معروف. " + err.Error()}
		rep.add("unavailable", f)
		return nil
	}
	rep.Collections["media"] = read
	f := finding("media", read.ReportedTotal)
	bucket := "available"
	if read.CountKnown {
		rep.Counts["media"] = read.ReportedTotal
	} else {
		bucket = "partial"
		f.Note = map[string]string{"en": "Attachment count is unknown; source did not provide a valid total.", "ar": "عدد المرفقات غير معروف؛ لم يوفر المصدر إجماليًا صالحًا."}
	}
	if read.Limited {
		bucket = "partial"
		f.Sampled = true
		f.Note = map[string]string{"en": "Attachment discovery is limited to the first five pages; the reported total is a source bound, not complete file verification.", "ar": "فحص المرفقات محدود بأول خمس صفحات؛ الإجمالي المعلن حد من المصدر وليس تحققًا من كل الملفات."}
	}
	rep.add(bucket, f)
	if len(foreign) > 0 {
		hosts := sortedCounts(foreign)
		host, _, _ := strings.Cut(hosts[0], " ")
		rep.OffloadedMediaHost = host
		rep.add("partial", finding("media_offloaded", sum(foreign), hosts...))
	}
	return nil
}

func discoverUsers(ctx context.Context, c *Client, rep *Report, authed bool) error {
	q := url.Values{}
	if authed {
		q.Set("context", "edit")
	}
	n, err := c.Count(ctx, "/wp/v2/users", q)
	if err != nil && !optionalRESTUnavailable(err) {
		return err
	}
	switch {
	case err == nil && authed:
		rep.Counts["users"] = n
		rep.add("available", finding("users", n))
	case err == nil:
		rep.Counts["users"] = n
		rep.add("partial", finding("users_private", n))
	case isForbidden(err) && !IsAuthError(err):
		rep.add("partial", finding("users_endpoint_private", 0))
	default:
		rep.add("partial", finding("users_private", 0))
	}
	return nil
}

var builderMarkers = map[string]*regexp.Regexp{
	"elementor": regexp.MustCompile(`class="[^"]*\belementor\b`),
	"divi":      regexp.MustCompile(`class="[^"]*\bet_pb_`),
	"beaver":    regexp.MustCompile(`class="[^"]*\bfl-builder`),
}

// wcPageSlugs are WooCommerce's plugin-rendered pages.
var wcPageSlugs = map[string]bool{"cart": true, "checkout": true, "my-account": true, "shop": true}

// analyzeSamples fills SEO/ACF/template/shortcode/page-builder findings.
func analyzeSamples(rep *Report, samples []WPContent, woo, sampled bool) {
	defer markSampled(rep, sampled)
	yoast, acfKey, acfEmpty := false, false, true
	builders := map[string]int{}
	var templated, pluginPages []string
	meta := map[string]map[string]bool{}
	for _, c := range samples {
		if len(c.Yoast) > 0 {
			yoast = true
		}
		if c.ACF != nil {
			acfKey = true
			if len(c.ACF) > 0 {
				acfEmpty = false
			}
		}
		if c.Template != "" {
			rep.Templates[c.Template]++
			templated = append(templated, decodeSlug(c.Slug))
		}
		body := c.Raw
		if body == "" {
			body = c.Rendered
		}
		for tag, n := range gutenberg.Shortcodes(stripCode(body)) {
			rep.Shortcodes[tag] += n
		}
		for name, re := range builderMarkers {
			if re.MatchString(c.Rendered) || re.MatchString(c.Raw) {
				builders[name]++
			}
		}
		if woo && c.Type == "page" && (wcPageSlugs[c.Slug] || strings.Contains(c.Rendered, "wp-block-woocommerce-")) {
			pluginPages = append(pluginPages, decodeSlug(c.Slug))
		}
		for k := range c.Meta {
			if !strings.HasPrefix(k, "_") && k != "footnotes" {
				if meta[c.Type] == nil {
					meta[c.Type] = map[string]bool{}
				}
				meta[c.Type][k] = true
			}
		}
		for k := range c.ACF {
			if meta[c.Type] == nil {
				meta[c.Type] = map[string]bool{}
			}
			meta[c.Type]["acf:"+k] = true
		}
	}
	for typ, keys := range meta {
		rep.MetaKeys[typ] = sortedKeys(keys)
	}
	rep.Plugins["yoast"] = rep.Plugins["yoast"] || yoast
	if rep.Plugins["yoast"] {
		rep.add("available", finding("seo_yoast", countYoast(samples)))
	}
	if rep.Plugins["rankmath"] {
		rep.add("partial", finding("seo_rankmath", 0))
	}
	rep.Plugins["acf"] = rep.Plugins["acf"] || acfKey
	switch {
	case acfKey && !acfEmpty:
		rep.add("available", finding("acf", len(samples)))
	case rep.Plugins["acf"]:
		rep.add("partial", finding("acf_hidden", 0))
	}
	if len(templated) > 0 {
		sort.Strings(templated)
		rep.add("unavailable", finding("custom_templates", len(templated), templated...))
	}
	if len(rep.Shortcodes) > 0 {
		rep.add("unavailable", finding("shortcodes", sum(rep.Shortcodes), sortedCounts(rep.Shortcodes)...))
	}
	for _, name := range sortedKeys(builders) {
		rep.Plugins[name] = true
		rep.add("unavailable", finding("page_builder:"+name, builders[name]))
	}
	if len(pluginPages) > 0 {
		sort.Strings(pluginPages)
		rep.add("unavailable", finding("plugin_pages", len(pluginPages), pluginPages...))
	}
}

func countYoast(samples []WPContent) int {
	n := 0
	for _, c := range samples {
		if len(c.Yoast) > 0 {
			n++
		}
	}
	return n
}

var codeBlock = regexp.MustCompile(`(?s)<(pre|code)[^>]*>.*?</(pre|code)>`)

// stripCode drops <pre>/<code> so code samples ([0], [i]) are not counted
// as shortcodes.
func stripCode(s string) string { return codeBlock.ReplaceAllString(s, "") }

func discoverProducts(ctx context.Context, c *Client, rep *Report, authed bool) error {
	if authed {
		if n, err := c.Count(ctx, "/wc/v3/products", nil); err == nil {
			rep.Counts["product"] = n
			rep.add("available", finding("products", n))
			rep.add("unavailable", finding("orders", 0))
			return nil
		} else if !optionalRESTUnavailable(err) {
			return err
		}
	}
	n, err := c.Count(ctx, "/wc/store/v1/products", nil)
	if err != nil {
		if !optionalRESTUnavailable(err) {
			return err
		}
		return nil
	}
	rep.Counts["product"] = n
	rep.add("available", finding("products", n))
	rep.add("partial", finding("product_meta", n))
	rep.add("unavailable", finding("orders", 0))
	return nil
}

func addAlwaysUnavailable(rep *Report) {
	rep.add("unavailable", finding("theme_settings", 0))
	rep.add("unavailable", finding("comments", 0))
	rep.add("unavailable", finding("passwords", 0))
}

// discoverWXR builds the report from an export file.
func (s *service) discoverWXR(src *sourceRow) (*Report, error) {
	f, err := s.wxr.get(src.filePath)
	if err != nil {
		return nil, err
	}
	ws := newWXRSource(f, "")
	rep := newReport("wxr")
	rep.Auth = AuthSummary{Mode: "wxr", OK: true}
	site, _ := ws.Site(context.Background())
	rep.Site = SiteSummary{Name: site.Name, URL: site.URL, Version: site.Version, Locale: site.Language}
	types := map[string]int{}
	var samples []WPContent
	all, _ := ws.Content(context.Background(), sortedKeys(itemTypes(f.Items)))
	for _, c := range all {
		types[c.Type]++
		samples = append(samples, c)
	}
	for _, typ := range sortedKeys(types) {
		rep.Counts[typ] = types[typ]
		rep.add("available", typeFinding("type", typ, typ, types[typ]))
	}
	taxes := map[string]int{}
	for _, t := range f.Terms {
		if t.Taxonomy != "nav_menu" {
			taxes[t.Taxonomy]++
		}
	}
	for _, tax := range sortedKeys(taxes) {
		rep.Counts[tax] = taxes[tax]
		rep.add("available", typeFinding("taxonomy", tax, tax, taxes[tax]))
	}
	media, _ := ws.Media(context.Background())
	rep.Counts["media"] = len(media)
	rep.add("available", finding("media", len(media)))
	rep.add("partial", finding("settings_public", 1))
	rep.Counts["users"] = len(f.Authors)
	rep.add("available", finding("users", len(f.Authors)))
	menus, _ := ws.Menus(context.Background())
	rep.Counts["menus"] = len(menus)
	rep.add("available", finding("menus", len(menus)))
	rep.add("available", finding("raw_content", len(samples)))
	for _, c := range samples {
		if len(c.Yoast) > 0 {
			rep.Plugins["yoast"] = true
		}
	}
	products, _ := ws.Products(context.Background())
	if len(products) > 0 {
		rep.Plugins["woocommerce"] = true
		rep.Counts["product"] = len(products)
		rep.add("available", finding("products", len(products)))
		rep.add("unavailable", finding("orders", 0))
	}
	analyzeSamples(rep, samples, rep.Plugins["woocommerce"], false)
	addAlwaysUnavailable(rep)
	if rep.Site.URL == "" {
		return rep, fmt.Errorf("%w: export has no site URL", ErrInvalidSource)
	}
	return rep, nil
}

// itemTypes returns the content post types present in an export.
func itemTypes(items []wxr.Item) map[string]bool {
	out := map[string]bool{}
	for _, it := range items {
		if !wxrSkipTypes[it.Type] {
			out[it.Type] = true
		}
	}
	return out
}

// markSampled flags the findings computed from the sampled pages/posts.
func markSampled(rep *Report, sampled bool) {
	if !sampled {
		return
	}
	mark := func(list []Finding, kinds map[string]bool) {
		for i := range list {
			k, _, _ := strings.Cut(list[i].Kind, ":")
			if kinds[k] {
				list[i].Sampled = true
			}
		}
	}
	kinds := map[string]bool{"seo_yoast": true, "acf": true, "acf_hidden": true, "custom_templates": true, "shortcodes": true,
		"page_builder": true, "plugin_pages": true}
	mark(rep.Available, kinds)
	mark(rep.Partial, kinds)
	mark(rep.Unavailable, kinds)
}
