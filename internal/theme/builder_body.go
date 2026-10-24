package theme

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/i18n"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

func hasClass(node *html.Node, class string) bool {
	for _, attribute := range node.Attr {
		if attribute.Key == "class" {
			for _, value := range strings.Fields(attribute.Val) {
				if value == class {
					return true
				}
			}
		}
	}
	return false
}
func appendHTML(target *html.Node, markup string) error {
	nodes, err := html.ParseFragment(strings.NewReader(markup), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return err
	}
	for _, node := range nodes {
		target.AppendChild(node)
	}
	return nil
}
func (f *funcs) builderBody(it *content.Item) (template.HTML, error) {
	if it == nil {
		return "", nil
	}
	nodes, err := html.ParseFragment(strings.NewReader(content.Sanitize(it.Body)), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return "", err
	}
	wrapper := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	for _, node := range nodes {
		wrapper.AppendChild(node)
	}
	var sections, directories []*html.Node
	hasHeading := false
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "h1" {
			hasHeading = true
		}
		if node.Type == html.ElementNode && node.Data == "section" && hasClass(node, "gp-section") {
			sections = append(sections, node)
		}
		if node.Type == html.ElementNode && node.Data == "div" && hasClass(node, "gp-s-directory") {
			directories = append(directories, node)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(wrapper)
	hasHeroScene := false
	contacts := 0
	for _, section := range sections {
		if hasClass(section, "gp-hero") {
			var sceneWalk func(*html.Node)
			sceneWalk = func(n *html.Node) {
				if hasClass(n, "gp-hero-scene") && (hasClass(n, "gp-scene-orbit") || hasClass(n, "gp-scene-ribbons") || hasClass(n, "gp-scene-blocks")) {
					hasHeroScene = true
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					sceneWalk(c)
				}
			}
			sceneWalk(section)
		}
		target := section
		for child := section.FirstChild; child != nil; child = child.NextSibling {
			if hasClass(child, "gp-inner") {
				target = child
				break
			}
		}
		if hasClass(section, "gp-contact") {
			form, err := f.include("components.contact-form.html", []any{f.st.tc})
			if err != nil {
				return "", err
			}
			markup := string(form)
			if contacts > 0 {
				markup = strings.ReplaceAll(markup, "cf-", fmt.Sprintf("cf-builder-%d-", contacts))
			}
			contacts++
			if err := appendHTML(target, markup); err != nil {
				return "", err
			}
		}
		if hasClass(section, "gp-feed-posts") {
			posts, err := f.query(map[string]any{"type": "post", "status": "published", "per_page": 6})
			if err != nil {
				return "", err
			}
			var markup strings.Builder
			markup.WriteString(`<div class="gp-grid">`)
			for _, post := range posts.Items {
				card, err := f.include("components.card.html", []any{post})
				if err != nil {
					return "", err
				}
				markup.WriteString(string(card))
			}
			if len(posts.Items) == 0 {
				if f.st.tc.Locale == "ar" {
					markup.WriteString("<p>انشر مقالك الأول ليظهر هنا.</p>")
				} else {
					markup.WriteString("<p>Publish your first article to display it here.</p>")
				}
			}
			markup.WriteString("</div>")
			if err := appendHTML(target, markup.String()); err != nil {
				return "", err
			}
		}
	}
	for i, node := range directories {
		if err := f.directory(node, i); err != nil {
			return "", err
		}
	}
	var output bytes.Buffer
	if !hasHeading {
		fmt.Fprintf(&output, `<h1 class="sr-only">%s</h1>`, template.HTMLEscapeString(it.Title))
	}
	for node := wrapper.FirstChild; node != nil; node = node.NextSibling {
		if err := html.Render(&output, node); err != nil {
			return "", err
		}
	}
	if hasHeroScene {
		output.WriteString(`<link rel="stylesheet" href="/static/builder/hero-scenes.css"><script type="module" src="/static/builder/hero-scenes.js"></script>`)
	}
	return template.HTML(output.String()), nil
}

// RenderBuilderBody renders dynamic sections for the authenticated builder's
// sandbox preview, using the same components and public-only query as live pages.
func (s *service) RenderBuilderBody(ctx context.Context, it *content.Item) (string, error) {
	locale := it.Locale
	if locale == "" {
		locale = s.d.Settings.GetString(ctx, "site.locale", "ar")
	}
	ctx = i18n.WithLocale(ctx, locale)
	tc := &Context{Content: it, Locale: locale, Dir: s.d.I18n.Dir(locale), Extra: map[string]any{}, Request: &http.Request{URL: &url.URL{Path: "/" + it.Slug}}}
	set, err := s.set(ctx, s.activeSlug(ctx), false)
	if err != nil {
		return "", err
	}
	st := &reqState{ctx: ctx, tc: tc, set: set}
	s.cloneMu.Lock()
	compiled, err := set.base.Clone()
	s.cloneMu.Unlock()
	if err != nil {
		return "", err
	}
	compiled = compiled.Funcs(s.funcMap(st))
	if _, err = compiled.New("builder.preview").Parse(`{{builderBody .Content}}`); err != nil {
		return "", err
	}
	st.tmpl = compiled
	var buffer bytes.Buffer
	if err := compiled.ExecuteTemplate(&buffer, "builder.preview", tc); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

// directoryLimit caps the items one directory section lists; a search
// narrows them.
const directoryLimit = 24

// directory fills a native directory section with a search form and the
// published items of its content type in the page locale, matched by the
// request's q. A type that is not public lists nothing.
func (f *funcs) directory(node *html.Node, index int) error {
	listType := ""
	for _, attribute := range node.Attr {
		if attribute.Key == "class" {
			for _, value := range strings.Fields(attribute.Val) {
				if strings.HasPrefix(value, "gp-s-dir-") {
					listType = strings.TrimPrefix(value, "gp-s-dir-")
				}
			}
		}
	}
	if listType == "" {
		return nil
	}
	path, search := "/", ""
	if r := f.st.tc.Request; r != nil && r.URL != nil {
		path = r.URL.Path
		search = strings.TrimSpace(r.URL.Query().Get("q"))
	}
	if runes := []rune(search); len(runes) > 100 {
		search = string(runes[:100])
	}
	args := map[string]any{"type": listType, "status": "published", "per_page": directoryLimit}
	if search != "" {
		args["search"] = search
	}
	items, err := f.query(args)
	if err != nil {
		return nil // an unknown or private type lists nothing rather than failing the page
	}
	ar := f.st.tc.Locale == "ar"
	word := func(en, arabic string) string {
		if ar {
			return arabic
		}
		return en
	}
	esc := template.HTMLEscapeString
	id := fmt.Sprintf("gp-dir-%d-q", index)
	var markup strings.Builder
	fmt.Fprintf(&markup, `<form class="gp-core gp-s-dir-search" role="search" method="get" action="%s"><label for="%s">%s</label><input id="%s" type="search" name="q" value="%s" maxlength="100"><button type="submit">%s</button></form>`,
		esc(path), id, word("Search", "بحث"), id, esc(search), word("Search", "بحث"))
	if search != "" {
		fmt.Fprintf(&markup, `<p class="gp-s-dir-count">%s <a href="%s">%s</a></p>`,
			esc(fmt.Sprintf(word("%d results for “%s”.", "%d نتيجة لـ «%s»."), items.Total, search)), esc(path), word("Show all", "عرض الكل"))
	}
	if len(items.Items) == 0 {
		if search != "" {
			markup.WriteString(`<p class="gp-s-dir-empty">` + word("Nothing matches this search yet.", "لا شيء يطابق هذا البحث بعد.") + `</p>`)
		} else {
			markup.WriteString(`<p class="gp-s-dir-empty">` + word("Nothing is published here yet.", "لم يُنشر شيء هنا بعد.") + `</p>`)
		}
		return appendHTML(node, markup.String())
	}
	markup.WriteString(`<div class="gp-grid">`)
	for _, item := range items.Items {
		card, err := f.include("components.card.html", []any{item})
		if err != nil {
			return err
		}
		markup.WriteString(string(card))
	}
	markup.WriteString("</div>")
	if items.Total > len(items.Items) {
		fmt.Fprintf(&markup, `<p class="gp-s-dir-more">%s</p>`, esc(fmt.Sprintf(word("Showing %d of %d. Search to narrow the list.", "يُعرض %d من %d. ابحث لتضييق القائمة."), len(items.Items), items.Total)))
	}
	return appendHTML(node, markup.String())
}
