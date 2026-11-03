package theme

import (
	"errors"
	"fmt"
	"html/template"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/content"
	"golang.org/x/net/html"
)

const directoryLimit = 24
const directoryMaxPage = 1000000

type directoryScope struct {
	path   string
	prefix string
	anchor string
	query  url.Values
	keys   []string
}

type directorySlot struct {
	node  *html.Node
	typ   *content.Type
	index int
}

func directoryType(node *html.Node) string {
	for _, attribute := range node.Attr {
		if attribute.Key == "class" {
			for _, class := range strings.Fields(attribute.Val) {
				if strings.HasPrefix(class, "gp-s-dir-") {
					return strings.TrimPrefix(class, "gp-s-dir-")
				}
			}
		}
	}
	return ""
}

func directoryAnchor(node *html.Node, index int) string {
	for parent := node.Parent; parent != nil; parent = parent.Parent {
		if parent.Data == "section" {
			for _, attribute := range parent.Attr {
				if attribute.Key == "id" && attribute.Val != "" {
					return attribute.Val
				}
			}
		}
	}
	return fmt.Sprintf("directory-%d", index)
}

func (f *funcs) renderDirectories(nodes []*html.Node) error {
	svc, ok := f.contentService()
	if !ok {
		return nil
	}
	var slots []directorySlot
	for index, node := range nodes {
		typ, err := svc.GetType(f.st.ctx, directoryType(node))
		if errors.Is(err, content.ErrNotFound) {
			continue
		}
		if err != nil {
			f.s.log.Warn("theme: directory type unavailable", "err", err)
			if err := f.directoryUnavailable(node); err != nil {
				return err
			}
			continue
		}
		if typ != nil && typ.Public {
			slots = append(slots, directorySlot{node: node, typ: typ, index: index})
		}
	}
	for _, slot := range slots {
		scope := f.directoryScope(slot, len(slots))
		if err := f.renderDirectory(slot, scope); err != nil {
			return err
		}
	}
	return nil
}

func (f *funcs) directoryUnavailable(node *html.Node) error {
	message := "This list could not be loaded. Please try again."
	if f.st.tc.Locale == "ar" {
		message = "تعذّر تحميل القائمة. حاول مرة أخرى."
	}
	retry := "/"
	if request := f.st.tc.Request; request != nil && request.URL != nil {
		retry = (&url.URL{Path: request.URL.Path, RawQuery: request.URL.RawQuery}).String()
	}
	return appendHTML(node, `<p class="gp-s-dir-error" role="alert">`+message+` <a href="`+template.HTMLEscapeString(retry)+`">`+f.directoryWord("Retry", "إعادة المحاولة")+`</a></p>`)
}

func (f *funcs) directoryScope(slot directorySlot, count int) directoryScope {
	scope := directoryScope{path: "/", query: url.Values{}, anchor: directoryAnchor(slot.node, slot.index), keys: []string{"q", "page"}}
	if request := f.st.tc.Request; request != nil && request.URL != nil {
		scope.path = request.URL.Path
		scope.query = request.URL.Query()
	}
	if count > 1 {
		scope.prefix = "dir_" + scope.anchor + "_"
	}
	spec := LibrarySpecFor(*slot.typ)
	scope.keys = append(scope.keys, spec.Taxonomies...)
	for _, field := range spec.Fields {
		scope.keys = append(scope.keys, field.Key)
	}
	return scope
}

func (scope directoryScope) localURL() *url.URL {
	query := url.Values{}
	for _, key := range scope.keys {
		if values, ok := scope.query[scope.prefix+key]; ok {
			query[key] = append([]string(nil), values...)
		}
	}
	// Existing multi-directory pages used one shared q before scoped controls.
	if scope.prefix != "" && !scope.query.Has(scope.prefix+"q") {
		query.Set("q", scope.query.Get("q"))
	}
	search := []rune(strings.TrimSpace(query.Get("q")))
	if len(search) > 100 {
		search = search[:100]
	}
	query.Set("q", string(search))
	return &url.URL{Path: scope.path, RawQuery: query.Encode()}
}

// Links and forms change only this collection; other GET state survives.
func (scope directoryScope) values(local url.Values) url.Values {
	query := url.Values{}
	for key, values := range scope.query {
		query[key] = append([]string(nil), values...)
	}
	for _, key := range scope.keys {
		query.Del(scope.prefix + key)
	}
	if scope.prefix != "" && scope.query.Has("q") {
		query.Set(scope.prefix+"q", "")
	}
	for key, values := range local {
		query[scope.prefix+key] = append([]string(nil), values...)
	}
	return query
}

func (scope directoryScope) href(local url.Values) string {
	target := url.URL{Path: scope.path, RawQuery: scope.values(local).Encode()}
	if scope.prefix != "" {
		target.Fragment = scope.anchor
	}
	return target.String()
}

func (scope directoryScope) remap(link string) string {
	parsed, err := url.Parse(link)
	if err != nil {
		return scope.path
	}
	return scope.href(parsed.Query())
}

func directoryPage(local *url.URL) int {
	page, err := strconv.Atoi(local.Query().Get("page"))
	if err != nil || page < 1 || page > directoryMaxPage {
		return 1
	}
	return page
}

func (f *funcs) renderDirectory(slot directorySlot, scope directoryScope) error {
	svc, _ := f.contentService()
	local := scope.localURL()
	library := ParseLibrary(LibrarySpecFor(*slot.typ), local)
	query := content.Query{Status: "published", PublicOnly: true, PublicLocale: f.pageLocale(), Search: local.Query().Get("q"), Page: directoryPage(local), PerPage: directoryLimit}
	library.Apply(&query)
	collection, err := svc.Query(f.st.ctx, query)
	if err != nil {
		f.s.log.Warn("theme: directory query failed", "type", slot.typ.Slug, "err", err)
		return f.directoryUnavailable(slot.node)
	}
	filters := f.libraryFilters(&Context{Extra: map[string]any{"library": library}, Pagination: NewPagination(collection.Page, collection.PerPage, collection.Total)})
	var markup strings.Builder
	f.directorySearch(&markup, scope, library, collection.Total)
	f.directoryFilters(&markup, scope, filters)
	if len(collection.Items) == 0 {
		text := f.directoryWord("Nothing is published here yet.", "لم يُنشر شيء هنا بعد.")
		if collection.Total > 0 {
			text = f.directoryWord("This page has no results. Return to the first page.", "لا توجد نتائج في هذه الصفحة. ارجع للصفحة الأولى.")
		} else if query.Search != "" || len(library.Values) > 0 {
			text = f.directoryWord("Nothing matches this search yet.", "لا شيء يطابق هذا البحث بعد.")
		}
		fmt.Fprintf(&markup, `<p class="gp-s-dir-empty">%s</p>`, text)
		if collection.Total > 0 {
			first := local.Query()
			first.Del("page")
			fmt.Fprintf(&markup, `<a href="%s">%s</a>`, template.HTMLEscapeString(scope.href(first)), f.directoryWord("First page", "الصفحة الأولى"))
		} else if query.Search == "" && len(library.Values) > 0 {
			fmt.Fprintf(&markup, `<a class="gp-filter-clear" href="%s">%s</a>`, template.HTMLEscapeString(scope.href(nil)), f.directoryWord("Clear filters", "مسح الفلاتر"))
		}
	} else {
		markup.WriteString(`<div class="gp-grid">`)
		for _, record := range collection.Items {
			card, err := f.include("components.card.html", []any{record})
			if err != nil {
				return err
			}
			card, err = f.directoryCardReturn(card, &record, scope)
			if err != nil {
				return err
			}
			markup.WriteString(string(card))
		}
		markup.WriteString(`</div>`)
	}
	f.directoryPagination(&markup, scope, library, collection)
	return appendHTML(slot.node, markup.String())
}

func (f *funcs) directoryWord(en, ar string) string {
	if f.st.tc.Locale == "ar" {
		return ar
	}
	return en
}

func (f *funcs) directorySearch(markup *strings.Builder, scope directoryScope, library *Library, total int) {
	esc := template.HTMLEscapeString
	search := scope.localURL().Query().Get("q")
	id := scope.anchor + "-q"
	fmt.Fprintf(markup, `<form class="gp-core gp-s-dir-search" role="search" method="get" action="%s"><label for="%s">%s</label><input id="%s" type="search" name="%s" value="%s" maxlength="100">`, esc(scope.path), esc(id), f.directoryWord("Search", "بحث"), esc(id), esc(scope.prefix+"q"), esc(search))
	local := url.Values{}
	for key, value := range library.Values {
		local.Set(key, value)
	}
	hidden := scope.values(local)
	hidden.Del(scope.prefix + "q")
	keys := make([]string, 0, len(hidden))
	for key := range hidden {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		values := hidden[key]
		for _, value := range values {
			fmt.Fprintf(markup, `<input type="hidden" name="%s" value="%s">`, esc(key), esc(value))
		}
	}
	fmt.Fprintf(markup, `<button type="submit">%s</button></form>`, f.directoryWord("Search", "بحث"))
	if search == "" {
		fmt.Fprintf(markup, `<p class="gp-s-dir-count" role="status">%s</p>`, esc(fmt.Sprintf(f.directoryWord("%d results.", "%d نتيجة."), total)))
	}
	if search != "" {
		fmt.Fprintf(markup, `<p class="gp-s-dir-count" role="status">%s <a href="%s">%s</a></p>`, esc(fmt.Sprintf(f.directoryWord("%d results for “%s”.", "%d نتيجة لـ «%s»."), total, search)), esc(scope.href(nil)), f.directoryWord("Show all", "عرض الكل"))
	}
}

func (f *funcs) directoryFilters(markup *strings.Builder, scope directoryScope, filters LibraryFilters) {
	esc := template.HTMLEscapeString
	for _, group := range filters.Groups {
		fmt.Fprintf(markup, `<nav class="gp-archive-filter" aria-label="%s"><p class="gp-filter-group-label">%s</p><ul class="gp-archive-filter-list">`, esc(group.Label), esc(group.Label))
		for _, option := range group.Options {
			current := ""
			if option.Active {
				current = ` aria-current="true"`
			}
			fmt.Fprintf(markup, `<li><a class="gp-filter-link" href="%s"%s>%s <span class="gp-filter-count">%d</span></a></li>`, esc(scope.remap(option.URL)), current, esc(option.Label), option.Count)
		}
		markup.WriteString(`</ul></nav>`)
	}
	if len(filters.Active) > 0 {
		markup.WriteString(`<p class="gp-library-summary">`)
		for _, active := range filters.Active {
			fmt.Fprintf(markup, `<a class="gp-filter-chip" href="%s" aria-label="%s">%s <span aria-hidden="true">×</span></a> `, esc(scope.remap(active.RemoveURL)), esc(fmt.Sprintf(f.directoryWord("Remove filter: %s", "إزالة الفلتر: %s"), active.Label)), esc(active.Label))
		}
		fmt.Fprintf(markup, `<a href="%s">%s</a></p>`, esc(scope.remap(filters.ClearURL)), f.directoryWord("Clear filters", "مسح الفلاتر"))
	}
}

func (f *funcs) directoryPagination(markup *strings.Builder, scope directoryScope, library *Library, collection content.Page[content.Item]) {
	pagination := PageURLs(NewPagination(collection.Page, collection.PerPage, collection.Total), scope.localURL(), library)
	if !pagination.HasPrev && !pagination.HasNext {
		return
	}
	fmt.Fprintf(markup, `<nav class="gp-s-dir-pagination" aria-label="%s">`, f.directoryWord("Result pages", "صفحات النتائج"))
	if pagination.HasPrev {
		fmt.Fprintf(markup, `<a rel="prev" href="%s">%s</a> `, template.HTMLEscapeString(scope.remap(pagination.PrevURL)), f.directoryWord("Previous", "السابق"))
	}
	if pagination.HasNext {
		fmt.Fprintf(markup, `<a rel="next" href="%s">%s</a>`, template.HTMLEscapeString(scope.remap(pagination.NextURL)), f.directoryWord("Next", "التالي"))
	}
	markup.WriteString(`</nav>`)
}
