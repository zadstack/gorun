package api

import (
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

// Pagination defaults.
const (
	DefaultPerPage = 20
	MaxPerPage     = 100
)

// PageParams documents ?page, ?per_page, ?sort and ?order for list
// operations: append it to APIOperation.Params.
var PageParams = []kernel.APIParam{
	{Name: "page", In: "query", Type: "integer", Description: "Page number, from 1 (default 1)."},
	{Name: "per_page", In: "query", Type: "integer", Description: "Items per page (default 20, max 100)."},
	{Name: "sort", In: "query", Type: "string", Description: "Sort field; prefix with - for descending."},
	{Name: "order", In: "query", Type: "string", Description: "asc or desc."},
}

// Pagination reads ?page and ?per_page: page defaults to 1, per_page to 20
// and is capped at 100; invalid values fall back to the defaults.
func Pagination(r *http.Request) (page, perPage int) {
	q := r.URL.Query()
	page, perPage = 1, DefaultPerPage
	if n, err := strconv.Atoi(q.Get("page")); err == nil && n > 0 {
		page = n
	}
	if n, err := strconv.Atoi(q.Get("per_page")); err == nil && n > 0 {
		perPage = min(n, MaxPerPage)
	}
	return page, perPage
}

// Sort reads ?sort and ?order against allowed (the first entry is the
// default). Without a valid ?sort it returns allowed[0] descending (newest
// first); an explicit ?sort is ascending unless ?order=desc or the field is
// prefixed with "-". Unknown fields and orders are ignored, so the result is
// safe to interpolate into ORDER BY.
func Sort(r *http.Request, allowed []string) (by, dir string) {
	q := r.URL.Query()
	field := strings.TrimSpace(q.Get("sort"))
	desc := strings.HasPrefix(field, "-")
	field = strings.TrimPrefix(field, "-")
	if field != "" && slices.Contains(allowed, field) {
		by, dir = field, "asc"
		if desc {
			dir = "desc"
		}
	} else {
		dir = "desc"
		if len(allowed) > 0 {
			by = allowed[0]
		}
	}
	switch o := strings.ToLower(q.Get("order")); o {
	case "asc", "desc":
		dir = o
	}
	return by, dir
}

// WriteList writes a 200 list envelope with pagination meta; a nil slice is
// written as [] (never null).
func WriteList(w http.ResponseWriter, items any, total, page, perPage int) {
	if v := reflect.ValueOf(items); items == nil || (v.Kind() == reflect.Slice && v.IsNil()) {
		if items == nil {
			items = []any{}
		} else {
			items = reflect.MakeSlice(v.Type(), 0, 0).Interface()
		}
	}
	httpx.JSON(w, http.StatusOK, items, httpx.Meta{Total: total, Page: page, PerPage: perPage})
}

// WriteItem writes a 200 envelope around v.
func WriteItem(w http.ResponseWriter, v any) { httpx.JSON(w, http.StatusOK, v, nil) }
