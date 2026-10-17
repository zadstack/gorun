package content

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// fieldView is what a fields/<type> partial renders. Every stored value is
// turned into strings defensively, so a value that no longer matches an
// edited schema still renders (Review Focus 4).
type fieldView struct {
	Key, Name, ID, Type, Label, Help string
	Required, Multiple, Checked      bool
	Value                            string
	Values                           []string
	Options                          []choice
	Min, Max, Step, Mime             string
	Error                            string
	Rows                             []rowView
	RowURL                           string
	Picker                           *pickView
	CanPick                          bool // image/file: offer the media picker
	StartHTML                        bool // richtext holds markup the visual editor would flatten
}

// visualUnsafe matches markup Quill cannot represent; such bodies open on
// the HTML tab, and switching to Visual asks first.
var visualUnsafe = regexp.MustCompile(`(?i)<(table|details|summary|figure|figcaption|iframe|video|audio|picture|pre)\b`)

func unsupportedMarkup(html string) bool { return visualUnsafe.MatchString(html) }

// pickView is the HTMX search picker (relationship fields, parent): the
// current selection renders as removable chips (always, however many items
// exist), and the search box fetches /admin/x/content/pick results.
type pickView struct {
	Name, ID, SearchURL string
	Multiple            bool
	Picked              []choice
}

func newPickView(name, id, target, exclude string, multiple bool, picked []choice) *pickView {
	q := url.Values{"type": {target}}
	if exclude != "" {
		q.Set("exclude", exclude)
	}
	return &pickView{Name: name, ID: id, SearchURL: "/admin/x/content/pick?" + q.Encode(), Multiple: multiple, Picked: picked}
}

type rowView struct {
	Index  string
	Fields []fieldView
}

type choice struct {
	Value, Label string
	Selected     bool
}

// fieldCtx carries what building field views needs.
type fieldCtx struct {
	ctx      context.Context
	svc      *service
	locale   string
	typeSlug string
	errs     FieldErrors
	itemID   string
	canPick  bool // media picker available (see adminHandlers.canPickMedia)
}

func floatString(p *float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'f', -1, 64)
}

// displayString renders any stored value as text without panicking.
func displayString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case []any, map[string]any, []map[string]any:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x)
		}
		return string(b)
	}
	return fmt.Sprint(v)
}

// displayList renders a stored value as a list of strings.
func displayList(v any) []string {
	switch x := v.(type) {
	case nil:
		return nil
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			out = append(out, displayString(e))
		}
		return out
	}
	return []string{displayString(v)}
}

// localDatetime turns a stored RFC3339 value into a datetime-local value.
func localDatetime(v string) string {
	if ts, ok := ParseDatetime(v); ok {
		return ts.Format("2006-01-02T15:04")
	}
	return v
}

func (fc *fieldCtx) views(schema []Field, values map[string]any, namePrefix, errPrefix string) []fieldView {
	out := make([]fieldView, 0, len(schema))
	for _, f := range schema {
		out = append(out, fc.view(f, values[f.Key], namePrefix, errPrefix))
	}
	return out
}

func (fc *fieldCtx) view(f Field, v any, namePrefix, errPrefix string) fieldView {
	name := namePrefix + "[" + f.Key + "]"
	fv := fieldView{Key: f.Key, Name: name, ID: strings.NewReplacer("[", "-", "]", "").Replace(name), Type: string(f.Type),
		Label: localized(f.Label, fc.locale, f.Key), Help: localized(f.Help, fc.locale, ""), Required: f.Required,
		Multiple: f.Multiple, Min: floatString(f.Min), Max: floatString(f.Max), Step: floatString(f.Step),
		Mime: strings.Join(f.Mime, ","), Error: fc.errs[errPrefix+f.Key]}
	switch f.Type {
	case FieldBoolean:
		b, ok := v.(bool)
		fv.Checked = (ok && b) || slices.Contains([]string{"true", "1", "on"}, displayString(v))
	case FieldDatetime:
		fv.Value = localDatetime(displayString(v))
	case FieldRichtext:
		fv.Value = displayString(v)
		fv.StartHTML = unsupportedMarkup(fv.Value)
	case FieldSelect, FieldMultiselect:
		sel := displayList(v)
		for _, o := range f.Options {
			fv.Options = append(fv.Options, choice{Value: o.Value, Label: localized(o.Label, fc.locale, o.Value), Selected: slices.Contains(sel, o.Value)})
		}
		fv.Values = sel
	case FieldRelationship:
		fv.Values = displayList(v)
		fv.Picker = newPickView(name, fv.ID, f.TargetType, fc.itemID, f.Multiple, fc.svc.pickedChoices(fc.ctx, fv.Values))
	case FieldRepeater:
		fv.RowURL = "/admin/x/content/" + fc.typeSlug + "/row?field=" + url.QueryEscape(f.Key)
		if rows, ok := asRows(v); ok {
			for i, m := range rows {
				idx := strconv.Itoa(i)
				fv.Rows = append(fv.Rows, rowView{Index: idx, Fields: fc.views(f.SubFields, m, name+"["+idx+"]", errPrefix+f.Key+"."+idx+".")})
			}
		} else if v != nil {
			fv.Value = displayString(v) // not a list any more: show it raw
		}
	case FieldImage, FieldFile:
		fv.Values = displayList(v)
		fv.Value = strings.Join(fv.Values, ",")
		fv.CanPick = fc.canPick
	case FieldJSON:
		if s, ok := v.(string); ok {
			fv.Value = s
		} else if v != nil {
			b, err := json.MarshalIndent(v, "", "  ")
			if err == nil {
				fv.Value = string(b)
			}
		}
	default:
		fv.Value = displayString(v)
	}
	return fv
}

// pickedChoices returns id → title chips for ids, in order, skipping ids
// that no longer exist.
func (s *service) pickedChoices(ctx context.Context, ids []string) []choice {
	out := []choice{}
	for _, id := range ids {
		var title string
		if err := s.db.QueryRowContext(ctx, `SELECT title FROM content WHERE id = ?`, id).Scan(&title); err != nil {
			continue
		}
		out = append(out, choice{Value: id, Label: title, Selected: true})
	}
	return out
}

// parseFieldForm reads custom field values named prefix[key] (repeater
// rows prefix[key][i][sub]) from a submitted form, as raw values for
// Validate. Absent keys stay absent.
func parseFieldForm(form url.Values, schema []Field, prefix string) map[string]any {
	out := map[string]any{}
	for _, f := range schema {
		name := prefix + "[" + f.Key + "]"
		switch f.Type {
		case FieldRepeater:
			idx := rowIndexes(form, name)
			if len(idx) == 0 {
				if _, ok := form[name+"[]"]; ok { // marker: all rows removed
					out[f.Key] = []any{}
				}
				continue
			}
			rows := make([]any, 0, len(idx))
			for _, i := range idx {
				rows = append(rows, parseFieldForm(form, f.SubFields, name+"["+i+"]"))
			}
			out[f.Key] = rows
		case FieldMultiselect, FieldBoolean:
			if vals, ok := form[name]; ok {
				out[f.Key] = vals
			}
		case FieldRelationship:
			if vals, ok := form[name]; ok {
				if f.Multiple {
					out[f.Key] = vals
				} else {
					out[f.Key] = lastNonEmpty(vals)
				}
			}
		case FieldImage, FieldFile:
			if vals, ok := form[name]; ok {
				if f.Multiple {
					out[f.Key] = strings.Split(strings.Join(vals, ","), ",")
				} else {
					out[f.Key] = lastNonEmpty(vals)
				}
			}
		default:
			if vals, ok := form[name]; ok && len(vals) > 0 {
				out[f.Key] = vals[0]
			}
		}
	}
	return out
}

func lastNonEmpty(vals []string) string {
	for i := len(vals) - 1; i >= 0; i-- {
		if strings.TrimSpace(vals[i]) != "" {
			return vals[i]
		}
	}
	return ""
}

// rowIndexes returns the distinct row indexes under name ("name[3][x]"),
// in numeric order (new rows use timestamps, so gaps are normal).
func rowIndexes(form url.Values, name string) []string {
	seen := map[string]bool{}
	var out []string
	for k := range form {
		rest, ok := strings.CutPrefix(k, name+"[")
		if !ok {
			continue
		}
		i, _, ok := strings.Cut(rest, "]")
		if !ok || i == "" || seen[i] {
			continue
		}
		if _, err := strconv.ParseInt(i, 10, 64); err != nil {
			continue
		}
		seen[i] = true
		out = append(out, i)
	}
	sort.Slice(out, func(a, b int) bool {
		x, _ := strconv.ParseInt(out[a], 10, 64)
		y, _ := strconv.ParseInt(out[b], 10, 64)
		return x < y
	})
	return out
}

// newRowIndex is the index of a row added in the browser.
func newRowIndex() string { return strconv.FormatInt(time.Now().UnixNano()/1000, 10) }

// asRows reads repeater rows from a stored ([]any) or validated
// ([]map[string]any) value.
func asRows(v any) ([]map[string]any, bool) {
	switch x := v.(type) {
	case []map[string]any:
		return x, true
	case []any:
		out := make([]map[string]any, 0, len(x))
		for _, e := range x {
			m, _ := e.(map[string]any)
			out = append(out, m)
		}
		return out, true
	}
	return nil, false
}
