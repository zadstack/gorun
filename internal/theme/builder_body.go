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
	var sections []*html.Node
	hasHeading := false
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "h1" {
			hasHeading = true
		}
		if node.Type == html.ElementNode && node.Data == "section" && hasClass(node, "gp-section") {
			sections = append(sections, node)
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
