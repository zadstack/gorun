package content

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
)

// Export limits: HTTP exports stop after MaxExportLimit items (the JSON document then names the
// cursor to continue from); items are read in keyset pages of exportPageSize.
const (
	MaxExportLimit = 10000
	exportPageSize = 100
	// ExportFormatName identifies the JSON export document.
	ExportFormatName = "gorun.content-export"
	exportVersion    = 1
)

// ExportOptions selects and shapes an export. The zero value exports every item the caller may
// read (all statuses but trash) as JSON without a limit.
type ExportOptions struct {
	Format    string   `json:"format"` // json (default) or csv
	Type      string   `json:"type,omitempty"`
	Status    string   `json:"status,omitempty"` // a status or a comma-separated list
	Locale    string   `json:"locale,omitempty"`
	AuthorID  string   `json:"author_id,omitempty"`
	Search    string   `json:"search,omitempty"`
	DateField string   `json:"date_field,omitempty"` // created_at (default), updated_at or published_at
	From      string   `json:"from,omitempty"`       // inclusive: a date (start of day UTC) or RFC 3339
	To        string   `json:"to,omitempty"`         // inclusive: a date (end of day UTC) or RFC 3339
	After     string   `json:"after,omitempty"`      // continue after this item id
	Limit     int      `json:"limit,omitempty"`      // 0 = no limit
	Columns   []string `json:"columns,omitempty"`    // CSV columns; default DefaultExportColumns
	// BaseURL makes relative media URLs absolute.
	BaseURL string `json:"-"`
	// Flush is called after each page is written (HTTP handlers flush the response).
	Flush func() `json:"-"`
}

// ExportResult is what an export wrote.
type ExportResult struct {
	Count int
	// NextAfter is the last exported id when the limit stopped the export early ("" when done).
	NextAfter string
}

// ExportAuthor is an item's author. Email is filled only for the caller's own items, for callers
// who may manage users, and for the system (CLI).
type ExportAuthor struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
}

// ExportMedia is a media item referenced by the featured image or an image/file field.
type ExportMedia struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// ExportItem is one item of the JSON export: everything needed to recreate it elsewhere.
type ExportItem struct {
	ID               string              `json:"id"`
	Type             string              `json:"type"`
	Slug             string              `json:"slug"`
	Title            string              `json:"title"`
	Excerpt          string              `json:"excerpt"`
	Body             string              `json:"body"`
	BodyFormat       string              `json:"body_format"`
	Status           Status              `json:"status"`
	Locale           string              `json:"locale"`
	TranslationGroup string              `json:"translation_group"`
	ParentID         string              `json:"parent_id"`
	MenuOrder        int                 `json:"menu_order"`
	Template         string              `json:"template"`
	Fields           map[string]any      `json:"fields"`
	SEO              map[string]any      `json:"seo"`
	Terms            map[string][]string `json:"terms"`
	Author           ExportAuthor        `json:"author"`
	FeaturedMediaID  string              `json:"featured_media_id"`
	Media            []ExportMedia       `json:"media"`
	CreatedAt        string              `json:"created_at"`
	UpdatedAt        string              `json:"updated_at"`
	PublishedAt      string              `json:"published_at"`
	PublishAt        string              `json:"publish_at"`
}

// MediaURLResolver maps media ids to their URLs; the media module's service provides it
// (resolved through kernel.ServiceMedia, as content cannot import media).
type MediaURLResolver interface {
	MediaURLs(ctx context.Context, ids []string) (map[string]string, error)
}

// Exporter streams content exports; the content service implements it (kernel.ServiceContent).
type Exporter interface {
	Export(ctx context.Context, w io.Writer, opts ExportOptions) (ExportResult, error)
}

var _ Exporter = (*service)(nil)

// DefaultExportColumns are the CSV columns when none are chosen.
var DefaultExportColumns = []string{"id", "type", "slug", "title", "status", "locale", "author_name", "created_at", "updated_at", "published_at", "terms"}

// ExportColumns are the core CSV columns; custom fields are "fields.<key>".
var ExportColumns = []string{"id", "type", "slug", "title", "excerpt", "body", "status", "locale", "translation_group", "parent_id",
	"author_id", "author_name", "author_email", "created_at", "updated_at", "published_at", "publish_at",
	"featured_media_id", "featured_media_url", "seo_title", "seo_description", "terms"}

var (
	exportDateFields = []string{"created_at", "updated_at", "published_at"}
	exportFieldKey   = regexp.MustCompile(`^fields\.[A-Za-z0-9_-]{1,64}$`)
)

// ParseExportOptions reads export options from query parameters (format, type, status, locale,
// author, search, date_field, from, to, after, limit, columns), validating every value. The
// limit defaults to and may not exceed MaxExportLimit.
func ParseExportOptions(q url.Values) (ExportOptions, error) {
	o := ExportOptions{Format: strings.ToLower(strings.TrimSpace(q.Get("format"))), Type: strings.TrimSpace(q.Get("type")),
		Status: strings.TrimSpace(q.Get("status")), AuthorID: strings.TrimSpace(q.Get("author")), Search: strings.TrimSpace(q.Get("search")),
		DateField: strings.TrimSpace(q.Get("date_field")), From: strings.TrimSpace(q.Get("from")), To: strings.TrimSpace(q.Get("to")),
		After: strings.TrimSpace(q.Get("after")), Limit: MaxExportLimit}
	errs := FieldErrors{}
	if o.Format == "" {
		o.Format = "json"
	}
	if o.Format != "json" && o.Format != "csv" {
		errs["format"] = "content.export.invalid_format"
	}
	if loc := strings.TrimSpace(q.Get("locale")); loc != "" {
		canonical, err := i18n.CanonicalLocale(loc)
		if err != nil {
			errs["locale"] = "content.validation.locale"
		}
		o.Locale = canonical
	}
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > MaxExportLimit {
			errs["limit"] = "content.export.invalid_limit"
		}
		o.Limit = n
	}
	if raw := strings.TrimSpace(q.Get("columns")); raw != "" {
		for _, c := range strings.Split(raw, ",") {
			if c = strings.TrimSpace(c); c != "" {
				o.Columns = append(o.Columns, c)
			}
		}
	}
	for k, v := range o.validate() {
		errs[k] = v
	}
	if len(errs) > 0 {
		return o, invalid(errs)
	}
	return o, nil
}

// validate checks the values ParseExportOptions does not parse itself (shared with Export).
func (o ExportOptions) validate() FieldErrors {
	errs := FieldErrors{}
	for _, st := range strings.Split(o.Status, ",") {
		if st = strings.TrimSpace(st); st != "" && !slices.Contains(Statuses, Status(st)) {
			errs["status"] = "content.validation.status"
		}
	}
	if o.DateField != "" && !slices.Contains(exportDateFields, o.DateField) {
		errs["date_field"] = "content.export.invalid_date_field"
	}
	if _, ok := exportBound(o.From, false); !ok {
		errs["from"] = "content.export.invalid_date"
	}
	if _, ok := exportBound(o.To, true); !ok {
		errs["to"] = "content.export.invalid_date"
	}
	for _, c := range o.Columns {
		if !slices.Contains(ExportColumns, c) && !exportFieldKey.MatchString(c) {
			errs["columns"] = "content.export.invalid_column"
		}
	}
	if len(o.Columns) > 100 {
		errs["columns"] = "content.export.invalid_column"
	}
	return errs
}

// exportBound turns a from/to value into the stored timestamp form: a date means the start (from)
// or the end (to) of that day in UTC. "" is no bound.
func exportBound(v string, end bool) (string, bool) {
	if v == "" {
		return "", true
	}
	if d, err := time.Parse("2006-01-02", v); err == nil {
		if end {
			d = d.Add(24*time.Hour - time.Second)
		}
		return db.FormatTime(d), true
	}
	if ts, err := time.Parse(time.RFC3339, v); err == nil {
		return db.FormatTime(ts), true
	}
	return "", false
}

// Export writes the items opts selects, as the caller may read them in the content list
// (restrictStaffRead), in id order. It reads keyset pages of exportPageSize and writes each
// page before reading the next, so memory does not grow with the export. Options are validated
// before anything is written; a later failure leaves the output truncated (a JSON document
// without its closing, a CSV without its last rows).
func (s *service) Export(ctx context.Context, w io.Writer, opts ExportOptions) (ExportResult, error) {
	var res ExportResult
	if opts.Format == "" {
		opts.Format = "json"
	}
	if opts.Format != "json" && opts.Format != "csv" {
		return res, invalid(FieldErrors{"format": "content.export.invalid_format"})
	}
	if errs := opts.validate(); len(errs) > 0 {
		return res, invalid(errs)
	}
	b, err := s.exportWhere(ctx, opts)
	if err != nil {
		return res, err
	}
	var typ *Type
	if opts.Type != "" {
		typ, _ = s.GetType(ctx, opts.Type)
	}
	out := newExportWriter(w, opts)
	if err := out.begin(); err != nil {
		return res, err
	}
	types := map[string]*Type{}
	if typ != nil {
		types[typ.Slug] = typ
	}
	after := opts.After
	for {
		size := exportPageSize
		if opts.Limit > 0 {
			size = min(size, opts.Limit-res.Count)
		}
		if size <= 0 {
			break
		}
		items, err := s.exportPage(ctx, b, after, size)
		if err != nil {
			return res, err
		}
		if len(items) == 0 {
			break
		}
		rows, err := s.exportItems(ctx, items, types, opts)
		if err != nil {
			return res, err
		}
		for i := range rows {
			if err := out.item(&rows[i]); err != nil {
				return res, err
			}
		}
		res.Count += len(rows)
		after = items[len(items)-1].ID
		if err := out.flush(); err != nil {
			return res, err
		}
		if opts.Flush != nil {
			opts.Flush()
		}
		if len(items) < size {
			after = ""
			break
		}
	}
	if opts.Limit > 0 && res.Count >= opts.Limit && after != "" {
		// Stopped by the limit: say where to continue only when something is left.
		if more, err := s.exportPage(ctx, b, after, 1); err == nil && len(more) > 0 {
			res.NextAfter = after
		}
	}
	return res, out.end(res)
}

// exportWhere builds the export's conditions through the content list's own query path, so the
// caller's read restrictions and the list filters apply exactly as there.
func (s *service) exportWhere(ctx context.Context, o ExportOptions) (*queryBuilder, error) {
	var typ *Type
	if o.Type != "" {
		t, err := s.GetType(ctx, o.Type)
		if err == ErrNotFound {
			return nil, invalid(FieldErrors{"type": "content.validation.unknown_type"})
		}
		if err != nil {
			return nil, err
		}
		typ = t
	}
	b := &queryBuilder{}
	restrictStaffRead(ctx, b)
	q := Query{Type: o.Type, Status: o.Status, Search: o.Search, AuthorID: o.AuthorID, Locale: o.Locale}
	if err := s.buildWhere(ctx, b, q, typ); err != nil {
		return nil, err
	}
	field := o.DateField
	if field == "" {
		field = "created_at"
	}
	col := "c." + field // allowlisted by validate
	if from, _ := exportBound(o.From, false); from != "" {
		b.add(col+" >= ?", from)
	}
	if to, _ := exportBound(o.To, true); to != "" {
		b.add(col+" <= ?", to)
	}
	return b, nil
}

// exportPage reads up to size items after the id cursor, in id order.
func (s *service) exportPage(ctx context.Context, b *queryBuilder, after string, size int) ([]Item, error) {
	where := slices.Clone(b.where)
	args := append(slices.Clone(b.joinArgs), b.args...)
	if after != "" {
		where = append(where, "c.id > ?")
		args = append(args, after)
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+itemColumns+` FROM content c `+strings.Join(b.joins, " ")+cond+` ORDER BY c.id LIMIT ?`,
		append(args, size)...)
	if err != nil {
		return nil, fmt.Errorf("content: export page: %w", err)
	}
	items, err := readQueryItems(rows)
	if err != nil {
		return nil, err
	}
	return items, s.fillQueryTerms(ctx, items)
}

// exportItems turns a page of items into export items with authors and media URLs, in two
// batched lookups per page.
func (s *service) exportItems(ctx context.Context, items []Item, types map[string]*Type, opts ExportOptions) ([]ExportItem, error) {
	authorIDs, mediaIDs := []string{}, []string{}
	refs := make([][]string, len(items))
	for i := range items {
		it := &items[i]
		typ, ok := types[it.Type]
		if !ok {
			typ, _ = s.GetType(ctx, it.Type)
			types[it.Type] = typ
		}
		if it.AuthorID != "" && !slices.Contains(authorIDs, it.AuthorID) {
			authorIDs = append(authorIDs, it.AuthorID)
		}
		refs[i] = mediaRefs(it, typ)
		for _, id := range refs[i] {
			if !slices.Contains(mediaIDs, id) {
				mediaIDs = append(mediaIDs, id)
			}
		}
	}
	authors, err := s.exportAuthors(ctx, authorIDs)
	if err != nil {
		return nil, err
	}
	urls := map[string]string{}
	if r := s.mediaResolver(); r != nil && len(mediaIDs) > 0 {
		if urls, err = r.MediaURLs(ctx, mediaIDs); err != nil {
			return nil, fmt.Errorf("content: export media: %w", err)
		}
	}
	u := auth.UserFromContext(ctx)
	seeEmails := u == nil || auth.Can(ctx, "users.manage")
	out := make([]ExportItem, len(items))
	for i := range items {
		it := &items[i]
		a := authors[it.AuthorID]
		a.ID = it.AuthorID
		if !seeEmails && u.ID != it.AuthorID {
			a.Email = ""
		}
		media := make([]ExportMedia, 0, len(refs[i]))
		for _, id := range refs[i] {
			media = append(media, ExportMedia{ID: id, URL: absoluteURL(opts.BaseURL, urls[id])})
		}
		out[i] = ExportItem{ID: it.ID, Type: it.Type, Slug: it.Slug, Title: it.Title, Excerpt: it.Excerpt, Body: it.Body,
			BodyFormat: it.BodyFormat, Status: it.Status, Locale: it.Locale, TranslationGroup: it.TranslationGroup,
			ParentID: it.ParentID, MenuOrder: it.MenuOrder, Template: it.Template, Fields: it.Fields, SEO: it.SEO, Terms: it.Terms,
			Author: a, FeaturedMediaID: it.FeaturedMediaID, Media: media, CreatedAt: it.CreatedAt, UpdatedAt: it.UpdatedAt,
			PublishedAt: it.PublishedAt, PublishAt: it.PublishAt}
	}
	return out, nil
}

func (s *service) mediaResolver() MediaURLResolver {
	if s.mediaFn == nil {
		return nil
	}
	return s.mediaFn()
}

// exportAuthors loads the names and emails of authors (an empty map when there is no users table,
// as in content-only test databases).
func (s *service) exportAuthors(ctx context.Context, ids []string) (map[string]ExportAuthor, error) {
	out := map[string]ExportAuthor{}
	if len(ids) == 0 {
		return out, nil
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'users'`).Scan(&n); err != nil || n == 0 {
		return out, err
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, email FROM users WHERE id IN (`+placeholders(len(ids))+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("content: export authors: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a ExportAuthor
		if err := rows.Scan(&a.ID, &a.Name, &a.Email); err != nil {
			return nil, err
		}
		out[a.ID] = a
	}
	return out, rows.Err()
}

// mediaRefs lists the media ids an item references: the featured image and the values of its
// type's image and file fields (inside repeaters too), without duplicates.
func mediaRefs(it *Item, typ *Type) []string {
	var out []string
	add := func(v any) {
		for _, id := range stringList(v) {
			if id != "" && !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	add(it.FeaturedMediaID)
	if typ != nil {
		collectMedia(typ.Fields, it.Fields, add)
	}
	return out
}

func collectMedia(fields []Field, values map[string]any, add func(any)) {
	for _, f := range fields {
		v, ok := values[f.Key]
		if !ok {
			continue
		}
		switch f.Type {
		case "image", "file":
			add(v)
		case "repeater":
			rows, _ := v.([]any)
			for _, row := range rows {
				if m, ok := row.(map[string]any); ok {
					collectMedia(f.SubFields, m, add)
				}
			}
		}
	}
}

func absoluteURL(base, u string) string {
	if u == "" || !strings.HasPrefix(u, "/") || strings.HasPrefix(u, "//") {
		return u
	}
	return strings.TrimRight(base, "/") + u
}

// --- writers ---

type exportWriter interface {
	begin() error
	item(*ExportItem) error
	flush() error
	end(ExportResult) error
}

func newExportWriter(w io.Writer, o ExportOptions) exportWriter {
	if o.Format == "csv" {
		cols := o.Columns
		if len(cols) == 0 {
			cols = DefaultExportColumns
		}
		return &csvExport{w: w, cw: csv.NewWriter(w), cols: cols}
	}
	return &jsonExport{w: w, opts: o}
}

type jsonExport struct {
	w    io.Writer
	opts ExportOptions
	n    int
}

func (j *jsonExport) begin() error {
	filters, _ := json.Marshal(j.opts)
	_, err := fmt.Fprintf(j.w, `{"format":%q,"version":%d,"exported_at":%q,"filters":%s,"items":[`,
		ExportFormatName, exportVersion, db.FormatTime(time.Now()), filters)
	return err
}

func (j *jsonExport) item(it *ExportItem) error {
	raw, err := json.Marshal(it)
	if err != nil {
		return fmt.Errorf("content: export item %s: %w", it.ID, err)
	}
	if j.n > 0 {
		if _, err := io.WriteString(j.w, ",\n"); err != nil {
			return err
		}
	} else if _, err := io.WriteString(j.w, "\n"); err != nil {
		return err
	}
	j.n++
	_, err = j.w.Write(raw)
	return err
}

func (j *jsonExport) flush() error { return nil }

func (j *jsonExport) end(res ExportResult) error {
	_, err := fmt.Fprintf(j.w, "\n],\"count\":%d,\"next_after\":%q}\n", res.Count, res.NextAfter)
	return err
}

type csvExport struct {
	w    io.Writer
	cw   *csv.Writer
	cols []string
}

func (c *csvExport) begin() error {
	if _, err := io.WriteString(c.w, httpx.CSVBOM); err != nil {
		return err
	}
	return c.cw.Write(c.cols)
}

func (c *csvExport) item(it *ExportItem) error {
	row := make([]string, len(c.cols))
	for i, col := range c.cols {
		row[i] = httpx.CSVCell(csvValue(it, col))
	}
	return c.cw.Write(row)
}

func (c *csvExport) flush() error {
	c.cw.Flush()
	return c.cw.Error()
}

func (c *csvExport) end(ExportResult) error { return c.flush() }

// csvValue is one CSV cell of it; custom fields that are not scalars are written as JSON.
func csvValue(it *ExportItem, col string) string {
	if key, ok := strings.CutPrefix(col, "fields."); ok {
		return cellText(it.Fields[key])
	}
	switch col {
	case "id":
		return it.ID
	case "type":
		return it.Type
	case "slug":
		return it.Slug
	case "title":
		return it.Title
	case "excerpt":
		return it.Excerpt
	case "body":
		return it.Body
	case "status":
		return string(it.Status)
	case "locale":
		return it.Locale
	case "translation_group":
		return it.TranslationGroup
	case "parent_id":
		return it.ParentID
	case "author_id":
		return it.Author.ID
	case "author_name":
		return it.Author.Name
	case "author_email":
		return it.Author.Email
	case "created_at":
		return it.CreatedAt
	case "updated_at":
		return it.UpdatedAt
	case "published_at":
		return it.PublishedAt
	case "publish_at":
		return it.PublishAt
	case "featured_media_id":
		return it.FeaturedMediaID
	case "featured_media_url":
		for _, m := range it.Media {
			if m.ID == it.FeaturedMediaID {
				return m.URL
			}
		}
		return ""
	case "seo_title":
		return cellText(it.SEO["title"])
	case "seo_description":
		return cellText(it.SEO["description"])
	case "terms":
		return termsCell(it.Terms)
	}
	return ""
}

// termsCell writes terms as "category:news|world; tag:go", taxonomies in order.
func termsCell(terms map[string][]string) string {
	taxes := make([]string, 0, len(terms))
	for tax, slugs := range terms {
		if len(slugs) > 0 {
			taxes = append(taxes, tax)
		}
	}
	slices.Sort(taxes)
	parts := make([]string, len(taxes))
	for i, tax := range taxes {
		parts[i] = tax + ":" + strings.Join(terms[tax], "|")
	}
	return strings.Join(parts, "; ")
}

func cellText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case json.Number:
		return x.String()
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(raw)
}
