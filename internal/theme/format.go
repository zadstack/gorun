package theme

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zadstack/gorun/internal/content"
)

const (
	defaultPerPage = 10
	maxPerPage     = 100
)

// toInt converts template numbers (int kinds, floats, numeric strings).
func toInt(v any) (int64, error) {
	switch x := v.(type) {
	case int:
		return int64(x), nil
	case int8:
		return int64(x), nil
	case int16:
		return int64(x), nil
	case int32:
		return int64(x), nil
	case int64:
		return x, nil
	case uint:
		return int64(x), nil
	case uint32:
		return int64(x), nil
	case uint64:
		return int64(x), nil
	case float32:
		return int64(x), nil
	case float64:
		return int64(x), nil
	case json.Number:
		return x.Int64()
	case string:
		return strconv.ParseInt(strings.TrimSpace(x), 10, 64)
	}
	return 0, fmt.Errorf("not a number: %v (%T)", v, v)
}

// BuildQuery turns a template query map into a content.Query. Keys: type,
// status, search, parent, locale, page, per_page (1–100, default 10),
// order ("published_at desc, title asc", "-published_at" or []content.Order),
// taxonomy (map tax → slug or slugs, or "tax:a,b"), fields (map key → value
// for eq, or content.Filter / {"op","value"}). Without canView the status
// is always "published".
func BuildQuery(args map[string]any, canView bool) (content.Query, error) {
	q := content.Query{Status: "published", Page: 1, PerPage: defaultPerPage}
	for k, v := range args {
		var err error
		switch k {
		case "type":
			q.Type = fmt.Sprint(v)
		case "status":
			q.Status = fmt.Sprint(v)
		case "search":
			q.Search = fmt.Sprint(v)
		case "parent":
			q.ParentID = fmt.Sprint(v)
		case "locale":
			q.Locale = fmt.Sprint(v)
		case "page", "per_page":
			var n int64
			if n, err = toInt(v); err == nil {
				if k == "page" {
					q.Page = int(max(n, 1))
				} else {
					q.PerPage = int(min(max(n, 1), maxPerPage))
				}
			}
		case "order":
			q.Order, err = parseOrder(v)
		case "taxonomy":
			q.Taxonomy, err = parseTaxonomy(v)
		case "fields":
			q.Fields, err = parseFields(v)
		default:
			err = fmt.Errorf("unknown key")
		}
		if err != nil {
			return content.Query{}, fmt.Errorf("query %s: %w", k, err)
		}
	}
	if !canView {
		q.Status = "published"
	}
	return q, nil
}

func parseOrder(v any) ([]content.Order, error) {
	switch x := v.(type) {
	case []content.Order:
		return x, nil
	case content.Order:
		return []content.Order{x}, nil
	case string:
		var out []content.Order
		for _, part := range strings.Split(x, ",") {
			f := strings.Fields(part)
			if len(f) == 0 {
				continue
			}
			o := content.Order{By: f[0], Dir: "asc"}
			if strings.HasPrefix(o.By, "-") {
				o.By, o.Dir = o.By[1:], "desc"
			}
			if len(f) > 1 {
				o.Dir = strings.ToLower(f[1])
			}
			if o.Dir != "asc" && o.Dir != "desc" {
				return nil, fmt.Errorf("direction %q", o.Dir)
			}
			out = append(out, o)
		}
		return out, nil
	}
	return nil, fmt.Errorf("unsupported value %T", v)
}

func strList(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, len(x))
		for i, s := range x {
			out[i] = fmt.Sprint(s)
		}
		return out
	case string:
		return strings.Split(x, ",")
	}
	return []string{fmt.Sprint(v)}
}

func parseTaxonomy(v any) (map[string][]string, error) {
	out := map[string][]string{}
	switch x := v.(type) {
	case map[string][]string:
		return x, nil
	case map[string]any:
		for k, s := range x {
			out[k] = strList(s)
		}
	case map[string]string:
		for k, s := range x {
			out[k] = strList(s)
		}
	case string:
		tax, slugs, ok := strings.Cut(x, ":")
		if !ok {
			return nil, fmt.Errorf("want \"tax:slug,slug\"")
		}
		out[tax] = strList(slugs)
	default:
		return nil, fmt.Errorf("unsupported value %T", v)
	}
	return out, nil
}

func parseFields(v any) (map[string]content.Filter, error) {
	switch x := v.(type) {
	case map[string]content.Filter:
		return x, nil
	case map[string]any:
		out := map[string]content.Filter{}
		for k, fv := range x {
			switch f := fv.(type) {
			case content.Filter:
				out[k] = f
			case map[string]any:
				op, _ := f["op"].(string)
				if op == "" {
					op = "eq"
				}
				out[k] = content.Filter{Op: op, Value: f["value"]}
			default:
				out[k] = content.Filter{Op: "eq", Value: fv}
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("unsupported value %T", v)
}

// NewPagination computes page metadata; page and perPage are clamped to ≥ 1.
func NewPagination(page, perPage, total int) Pagination {
	page, perPage = max(page, 1), max(perPage, 1)
	pages := max((total+perPage-1)/perPage, 1)
	return Pagination{Page: page, PerPage: perPage, Total: total, Pages: pages, HasPrev: page > 1, HasNext: page < pages}
}

// paginate: paginate <content.Page> | paginate <Pagination> |
// paginate page total | paginate page perPage total.
func paginate(args ...any) (Pagination, error) {
	if len(args) == 1 {
		switch x := args[0].(type) {
		case content.Page[content.Item]:
			return NewPagination(x.Page, x.PerPage, x.Total), nil
		case Pagination:
			return NewPagination(x.Page, x.PerPage, x.Total), nil
		}
	}
	nums := make([]int, len(args))
	for i, a := range args {
		n, err := toInt(a)
		if err != nil {
			return Pagination{}, fmt.Errorf("paginate: %w", err)
		}
		nums[i] = int(n)
	}
	switch len(nums) {
	case 2:
		return NewPagination(nums[0], defaultPerPage, nums[1]), nil
	case 3:
		return NewPagination(nums[0], nums[1], nums[2]), nil
	}
	return Pagination{}, fmt.Errorf("paginate: want a content page, or page [per_page] total")
}

var arabicMonths = [12]string{"يناير", "فبراير", "مارس", "أبريل", "مايو", "يونيو", "يوليو", "أغسطس", "سبتمبر", "أكتوبر", "نوفمبر", "ديسمبر"}

var tzCache sync.Map // name → *time.Location

func location(name string) *time.Location {
	if name == "" {
		return time.UTC
	}
	if l, ok := tzCache.Load(name); ok {
		return l.(*time.Location)
	}
	l, err := time.LoadLocation(name)
	if err != nil {
		l = time.UTC
	}
	tzCache.Store(name, l)
	return l
}

func parseDate(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, !x.IsZero()
	case *time.Time:
		if x != nil {
			return *x, !x.IsZero()
		}
	case string:
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
			if t, err := time.Parse(layout, strings.TrimSpace(x)); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

// FormatDate formats v (RFC3339 string or time) for locale in timezone tz.
// Layouts: "long" (default: "10 أكتوبر 2026" / "October 10, 2026"),
// "datetime" (long + 15:04), "short" (2006-01-02), "iso" (RFC3339), or a
// Go layout. Digits stay Latin. Unparseable strings are returned as-is.
func FormatDate(v any, locale, tz string, layout ...string) string {
	t, ok := parseDate(v)
	if !ok {
		if s, isStr := v.(string); isStr {
			return s
		}
		return ""
	}
	t = t.In(location(tz))
	l := "long"
	if len(layout) > 0 && layout[0] != "" {
		l = layout[0]
	}
	long := func() string {
		if strings.HasPrefix(locale, "ar") {
			return fmt.Sprintf("%d %s %d", t.Day(), arabicMonths[t.Month()-1], t.Year())
		}
		return t.Format("January 2, 2006")
	}
	switch l {
	case "long":
		return long()
	case "datetime":
		return long() + " " + t.Format("15:04")
	case "short":
		return t.Format("2006-01-02")
	case "iso":
		return t.Format(time.RFC3339)
	}
	return t.Format(l)
}

func (f *funcs) date(v any, layout ...string) string {
	tz := f.st.cached(&f.st.tz, func() string {
		if f.s.d.Settings == nil {
			return ""
		}
		return f.s.d.Settings.GetString(f.st.ctx, SettingTZ, "")
	})
	return FormatDate(v, f.st.tc.Locale, tz, layout...)
}

var (
	zeroDecimal     = map[string]bool{"JPY": true, "KRW": true, "VND": true, "CLP": true, "ISK": true, "UGX": true, "XAF": true, "XOF": true}
	threeDecimal    = map[string]bool{"KWD": true, "BHD": true, "OMR": true, "JOD": true, "TND": true, "LYD": true, "IQD": true}
	currencySymbols = map[string]string{"USD": "$", "EUR": "€", "GBP": "£", "JPY": "¥"}
)

// Money formats an amount in minor units: Money(123450, "USD", "en") =
// "$1,234.50"; for Arabic the code follows the number ("1,234.50 USD").
// Digits stay Latin.
func Money(amount any, currency, locale string) (string, error) {
	minor, err := toInt(amount)
	if err != nil {
		return "", fmt.Errorf("money: %w", err)
	}
	cur := strings.ToUpper(strings.TrimSpace(currency))
	dec := 2
	switch {
	case zeroDecimal[cur]:
		dec = 0
	case threeDecimal[cur]:
		dec = 3
	}
	neg := minor < 0
	if neg {
		minor = -minor
	}
	pow := int64(1)
	for range dec {
		pow *= 10
	}
	num := groupThousands(strconv.FormatInt(minor/pow, 10))
	if dec > 0 {
		num += "." + fmt.Sprintf("%0*d", dec, minor%pow)
	}
	if neg {
		num = "-" + num
	}
	if sym, ok := currencySymbols[cur]; ok && !strings.HasPrefix(locale, "ar") {
		if neg {
			return "-" + sym + num[1:], nil
		}
		return sym + num, nil
	}
	return strings.TrimSpace(num + " " + cur), nil
}

func groupThousands(s string) string {
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}
