package theme

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/zadstack/gorun/internal/brand"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/menus"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

type BuilderPreviewOptions struct {
	Theme, Title, Tagline, CSS, Logo string
	Identity                         brand.Identity // Zero means inherit the public theme without an identity override.
	Menu                             *menus.Menu
}

func (s *service) RenderBuilderThemePreview(ctx context.Context, it *content.Item, options BuilderPreviewOptions) (string, error) {
	if options.Identity != (brand.Identity{}) {
		if err := brand.Validate(options.Identity); err != nil {
			return "", err
		}
	}
	set, err := s.set(ctx, options.Theme, false)
	if err != nil {
		return "", err
	}
	request := kernel.KindRequest{Kind: KindPage, Type: "page", Slug: it.Slug, Template: "builder"}
	// The preview renders the page as it will look once published through
	// the builder. Theme heads link builder.css only for Template "builder",
	// which a page gets on its first builder publish, so a never-published
	// page must be rendered with that template too or its preview loses the
	// builder layout the public page will have.
	previewItem := *it
	previewItem.Template = "builder"
	tc := &Context{Content: &previewItem, Locale: it.Locale, Theme: set.chain[0], Extra: map[string]any{}, Query: map[string]string{}, Request: &http.Request{URL: &url.URL{Path: "/" + it.Slug}}}
	tc = s.prepare(ctx, request, tc)
	if options.Title != "" {
		tc.Site.Title = options.Title
		tc.Site.Tagline = options.Tagline
		tc.Site.Locale = it.Locale
		tc.Site.Dir = tc.Dir
	}
	name, err := resolve(request, s.d.Theme, set.exists)
	if err != nil {
		return "", err
	}
	st := &reqState{ctx: ctx, tc: tc, set: set, previewMenu: options.Menu, builderPreview: true}
	if options.Identity != (brand.Identity{}) {
		st.previewIdentity = &options.Identity
	}
	if options.Logo != "" {
		st.previewSettings = map[string]string{"site.logo": options.Logo}
	}
	tmpl, ok := set.pages[name]
	if !ok {
		return "", fmt.Errorf("theme: preview template missing")
	}
	s.cloneMu.Lock()
	tmpl, err = tmpl.Clone()
	s.cloneMu.Unlock()
	if err != nil {
		return "", err
	}
	tmpl = tmpl.Funcs(s.funcMap(st))
	st.tmpl = tmpl
	var output bytes.Buffer
	if err = tmpl.ExecuteTemplate(&output, name, tc); err != nil {
		return "", err
	}
	tree, err := html.Parse(strings.NewReader(output.String()))
	if err != nil {
		return "", err
	}
	var head *html.Node
	var assetError error
	assetBudget := 0
	var clean func(*html.Node)
	clean = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "head" {
			head = node
		}
		for child := node.FirstChild; child != nil; {
			next := child.NextSibling
			if child.Type == html.ElementNode && child.Data == "link" && previewBuilderStylesheet(child) {
				style := previewInlineStyle(options.CSS)
				node.InsertBefore(style, child)
				node.RemoveChild(child)
				child = next
				continue
			}
			if child.Type == html.ElementNode && child.Data == "link" && previewCoreStylesheet(child) {
				node.InsertBefore(previewInlineStyle(string(CoreCSS())), child)
				node.RemoveChild(child)
				child = next
				continue
			}
			if child.Type == html.ElementNode && child.Data == "link" && previewStylesheet(child, set.chain) {
				href, _ := previewLink(child)
				css, err := inlinePreviewCSS(href, set.chain, &assetBudget)
				if err != nil {
					assetError = err
					return
				}
				style := previewInlineStyle(css)
				for _, attr := range child.Attr {
					if attr.Key == "media" {
						style.Attr = append(style.Attr, attr)
					}
				}
				node.InsertBefore(style, child)
				node.RemoveChild(child)
				child = next
				continue
			}
			remove := child.Type == html.ElementNode && (child.Data == "script" || child.Data == "base" || child.Data == "link")
			if remove {
				node.RemoveChild(child)
			} else {
				clean(child)
			}
			child = next
		}
	}
	clean(tree)
	if assetError != nil {
		return "", assetError
	}
	if head == nil {
		return "", fmt.Errorf("theme: preview document head missing")
	}
	head.AppendChild(&html.Node{Type: html.ElementNode, Data: "meta", DataAtom: atom.Meta, Attr: []html.Attribute{{Key: "http-equiv", Val: "Content-Security-Policy"}, {Key: "content", Val: "default-src 'none'; img-src https: http: data:; style-src 'unsafe-inline'; font-src data:; form-action 'none'; base-uri 'none'"}}})
	output.Reset()
	if err := html.Render(&output, tree); err != nil {
		return "", err
	}
	return output.String(), nil
}
