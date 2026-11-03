package theme

import (
	"bytes"
	"html/template"
	"net/url"
	"strings"

	"github.com/zadstack/gorun/internal/content"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// directoryCardReturn connects only an opted-in detail template's own content
// links to discovery state. Media/category/third-party links retain their URLs.
func (f *funcs) directoryCardReturn(card template.HTML, record *content.Item, scope directoryScope) (template.HTML, error) {
	if record.Template != "public-detail" || !f.publicUI(f.st.tc) {
		return card, nil
	}
	query := scope.values(scope.localURL().Query())
	query.Del("back") // Never nest a previous return URL inside the new one.
	back := (&url.URL{Path: scope.path, RawQuery: query.Encode(), Fragment: scope.anchor}).String()
	// Keep a bounded URL rather than truncating filters or accepting authorities.
	if len(back) > 4096 || !strings.HasPrefix(back, "/") || !publicNavigationURL(back) {
		return card, nil
	}
	canonical, err := url.Parse(f.url(record))
	if err != nil {
		return card, nil
	}
	root := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	if err := appendHTML(root, string(card)); err != nil {
		return "", err
	}
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "a" {
			for i, a := range node.Attr {
				if a.Key != "href" {
					continue
				}
				link, err := url.Parse(a.Val)
				if err != nil || link.User != nil || link.Path != canonical.Path || (link.Host != "" && link.Host != canonical.Host) || (link.Scheme != "" && link.Scheme != canonical.Scheme) {
					continue
				}
				values := link.Query()
				values.Set("back", back)
				link.RawQuery = values.Encode()
				node.Attr[i].Val = link.String()
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(root)
	var out bytes.Buffer
	for node := root.FirstChild; node != nil; node = node.NextSibling {
		if err := html.Render(&out, node); err != nil {
			return "", err
		}
	}
	return template.HTML(out.String()), nil
}
