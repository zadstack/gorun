package content

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

const (
	defaultPerPage = 20
	maxPerPage     = 100
)

// orderColumns are the core columns Query may order by.
var orderColumns = map[string]string{
	"title": "c.title", "slug": "c.slug", "created_at": "c.created_at", "updated_at": "c.updated_at",
	"published_at": "c.published_at", "publish_at": "c.publish_at", "menu_order": "c.menu_order", "status": "c.status",
}

var filterOps = map[string]string{"eq": "=", "ne": "!=", "gt": ">", "lt": "<", "gte": ">=", "lte": "<=", "in": "IN", "like": "LIKE"}

// likeEscape escapes LIKE wildcards; use with ESCAPE '\'.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

type queryBuilder struct {
	joins, where []string
	args         []any
	joinArgs     []any
	rank         bool // order by search relevance (a full-text query without an explicit order)
}

func (b *queryBuilder) add(cond string, args ...any) {
	b.where = append(b.where, cond)
	b.args = append(b.args, args...)
}

// Query runs the content query DSL. Field filters and field ordering are
// only allowed on indexed fields of q.Type; Order.By is whitelisted (core
// columns or indexed keys) and Dir must be asc or desc. Without a status
// filter trashed items are excluded unless IncludeTrash. Status accepts a
// comma-separated list.
func (s *service) Query(ctx context.Context, q Query) (Page[Item], error) {
	page := Page[Item]{Items: []Item{}, Page: max(q.Page, 1), PerPage: q.PerPage}
	if page.PerPage <= 0 {
		page.PerPage = defaultPerPage
	}
	page.PerPage = min(page.PerPage, maxPerPage)

	var typ *Type
	if q.Type != "" {
		t, err := s.GetType(ctx, q.Type)
		if err != nil {
			if err == ErrNotFound {
				return page, invalid(FieldErrors{"type": "content.validation.unknown_type"})
			}
			return page, err
		}
		typ = t
	}
	b := &queryBuilder{}
	restrictStaffRead(ctx, b)
	if err := s.buildWhere(ctx, b, q, typ); err != nil {
		return page, err
	}
	orderBy, err := buildOrder(b, q.Order, typ)
	if err != nil {
		return page, err
	}
	if b.rank {
		orderBy = searchRank + ", " + orderBy
	}
	from := ` FROM content c ` + strings.Join(b.joins, " ")
	where := ""
	if len(b.where) > 0 {
		where = " WHERE " + strings.Join(b.where, " AND ")
	}
	countArgs := append(slices.Clone(b.joinArgs), b.args...)
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*)`+from+where, countArgs...).Scan(&page.Total); err != nil {
		return page, fmt.Errorf("content: count: %w", err)
	}
	args := append(countArgs, page.PerPage, (page.Page-1)*page.PerPage)
	rows, err := s.db.QueryContext(ctx, `SELECT `+itemColumns+from+where+` ORDER BY `+orderBy+` LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return page, fmt.Errorf("content: query: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, *it)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	rows.Close()
	for i := range page.Items {
		s.fillTerms(ctx, &page.Items[i])
	}
	return page, nil
}

func (s *service) buildWhere(ctx context.Context, b *queryBuilder, q Query, typ *Type) error {
	if q.PublicOnly {
		for _, id := range publicExclusions(ctx) {
			b.add("c.id != ?", id)
		}
		b.add("EXISTS (SELECT 1 FROM content_types t WHERE t.slug = c.type AND t.public = 1)")
		b.add("NOT EXISTS (SELECT 1 FROM content_page_access pa WHERE pa.content_id=c.id AND pa.mode!='public')")
	}
	if q.Template != "" {
		b.add("c.template = ?", q.Template)
	}
	if typ != nil {
		b.add("c.type = ?", typ.Slug)
	}
	switch {
	case q.Status != "":
		var sts []any
		for _, st := range strings.Split(q.Status, ",") {
			st = strings.TrimSpace(st)
			if !slices.Contains(Statuses, Status(st)) {
				return invalid(FieldErrors{"status": "content.validation.status"})
			}
			sts = append(sts, st)
		}
		b.add("c.status IN ("+placeholders(len(sts))+")", sts...)
	case !q.IncludeTrash:
		b.add("c.status != 'trash'")
	}
	if q.Search != "" && addSearch(b, q.Search) && len(q.Order) == 0 {
		b.rank = true
	}
	if q.AuthorID != "" {
		b.add("c.author_id = ?", q.AuthorID)
	}
	switch q.ParentID {
	case "":
	case "0", "root":
		b.add("c.parent_id IS NULL")
	default:
		b.add("c.parent_id = ?", q.ParentID)
	}
	if q.Locale != "" {
		b.add("c.locale = ?", q.Locale)
	}
	if q.PublicLocale != "" {
		b.add("(c.locale = ? OR c.locale = '')", q.PublicLocale)
	}
	taxes := make([]string, 0, len(q.Taxonomy))
	for tax := range q.Taxonomy {
		taxes = append(taxes, tax)
	}
	slices.Sort(taxes)
	for _, tax := range taxes {
		slugs := q.Taxonomy[tax]
		if len(slugs) == 0 {
			continue
		}
		var ids []string
		if tb := s.terms(); tb != nil {
			var err error
			if ids, err = tb.ContentIDs(ctx, tax, slugs); err != nil {
				return fmt.Errorf("content: taxonomy filter: %w", err)
			}
		}
		raw, _ := json.Marshal(append([]string{}, ids...))
		b.add("c.id IN (SELECT value FROM json_each(?))", string(raw))
	}
	keys := make([]string, 0, len(q.Fields))
	for k := range q.Fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if err := addFieldFilter(b, typ, key, q.Fields[key]); err != nil {
			return err
		}
	}
	return nil
}

func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }

// indexedField returns the indexed scalar field key of typ, or nil.
func indexedField(typ *Type, key string) *Field {
	if typ == nil {
		return nil
	}
	for i := range typ.Fields {
		f := &typ.Fields[i]
		if f.Key == key && f.Indexed && f.Type.IsScalar() {
			return f
		}
	}
	return nil
}

func addFieldFilter(b *queryBuilder, typ *Type, key string, flt Filter) error {
	f := indexedField(typ, key)
	if f == nil {
		return invalid(FieldErrors{"fields." + key: "content.validation.not_indexed"})
	}
	op, ok := filterOps[flt.Op]
	if !ok {
		return invalid(FieldErrors{"fields." + key: "content.validation.filter_op"})
	}
	col := "ci." + indexColumn(f.Type)
	sub := `EXISTS (SELECT 1 FROM content_index ci WHERE ci.content_id = c.id AND ci.field = ? AND `
	switch flt.Op {
	case "in":
		var vals []any
		for _, v := range stringList(flt.Value) {
			cv, err := filterValue(f, v)
			if err != nil {
				return err
			}
			vals = append(vals, cv)
		}
		if len(vals) == 0 {
			b.add("0")
			return nil
		}
		b.add(sub+col+" IN ("+placeholders(len(vals))+"))", append([]any{key}, vals...)...)
	case "like":
		b.add(sub+col+` LIKE ? ESCAPE '\')`, key, "%"+likeEscape(scalarString(flt.Value))+"%")
	case "ne":
		v, err := filterValue(f, flt.Value)
		if err != nil {
			return err
		}
		b.add("NOT "+sub+col+" = ?)", key, v)
	default:
		v, err := filterValue(f, flt.Value)
		if err != nil {
			return err
		}
		b.add(sub+col+" "+op+" ?)", key, v)
	}
	return nil
}

// filterValue coerces a filter operand to the index column's type.
func filterValue(f *Field, v any) (any, error) {
	bad := invalid(FieldErrors{"fields." + f.Key: "content.validation.filter_value"})
	switch f.Type {
	case FieldNumber:
		n, msg := coerceNumber(Field{}, v)
		if msg != "" || n == nil {
			return nil, bad
		}
		return n, nil
	case FieldBoolean:
		bv, msg := coerceBool(v)
		if msg != "" || bv == nil {
			return nil, bad
		}
		if bv.(bool) {
			return 1.0, nil
		}
		return 0.0, nil
	case FieldDatetime:
		if ts, ok := ParseDatetime(scalarString(v)); ok {
			return ts.Format("2006-01-02T15:04:05Z"), nil
		}
		return scalarString(v), nil
	}
	return scalarString(v), nil
}

func buildOrder(b *queryBuilder, order []Order, typ *Type) (string, error) {
	var parts []string
	for i, o := range order {
		dir := strings.ToUpper(o.Dir)
		if dir == "" {
			dir = "ASC"
		}
		if dir != "ASC" && dir != "DESC" {
			return "", invalid(FieldErrors{"order": "content.validation.order"})
		}
		if col, ok := orderColumns[o.By]; ok {
			parts = append(parts, col+" "+dir)
			continue
		}
		f := indexedField(typ, o.By)
		if f == nil {
			return "", invalid(FieldErrors{"order": "content.validation.order"})
		}
		alias := fmt.Sprintf("oi%d", i)
		b.joins = append(b.joins, "LEFT JOIN content_index "+alias+" ON "+alias+".content_id = c.id AND "+alias+".field = ?")
		b.joinArgs = append(b.joinArgs, f.Key)
		parts = append(parts, alias+"."+indexColumn(f.Type)+" "+dir)
	}
	if len(parts) == 0 {
		parts = append(parts, "c.created_at DESC")
		return strings.Join(parts, ", ") + ", c.id DESC", nil
	}
	return strings.Join(parts, ", ") + ", c.id", nil
}
