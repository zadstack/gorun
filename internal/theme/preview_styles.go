package theme

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/zadstack/gorun/web"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const previewAssetLimit = 2 << 20
const previewTotalLimit = 8 << 20

func previewLink(node *html.Node) (string, string) {
	var href, rel string
	for _, attr := range node.Attr {
		switch attr.Key {
		case "href":
			href = attr.Val
		case "rel":
			rel = attr.Val
		}
	}
	return href, rel
}

func previewAsset(href string, chain []Info) (Info, string, bool) {
	u, err := url.Parse(href)
	if err != nil || u.IsAbs() || u.Host != "" || u.User != nil || u.Fragment != "" || strings.Contains(u.Path, "\\") || path.Clean(u.Path) != u.Path {
		return Info{}, "", false
	}
	for _, info := range chain {
		prefix := "/themes/" + info.Slug + "/assets/"
		if strings.HasPrefix(u.Path, prefix) {
			return info, strings.TrimPrefix(u.Path, prefix), true
		}
	}
	return Info{}, "", false
}

func previewStylesheet(node *html.Node, chain []Info) bool {
	href, rel := previewLink(node)
	_, name, ok := previewAsset(href, chain)
	return rel == "stylesheet" && ((ok && strings.HasSuffix(name, ".css")) || href == "/static/admin/zad-font.css")
}

func previewBuilderStylesheet(node *html.Node) bool {
	href, rel := previewLink(node)
	return rel == "stylesheet" && href == "/static/builder/builder.css"
}

func previewInlineStyle(css string) *html.Node {
	node := &html.Node{Type: html.ElementNode, Data: "style", DataAtom: atom.Style}
	node.AppendChild(&html.Node{Type: html.TextNode, Data: strings.ReplaceAll(css, "</", "<\\/")})
	return node
}

// OpenRoot confines symlinks and relative paths to public installed-theme assets.
// No remote request, application file or tenant secret can enter the preview.
func readPreviewAsset(href string, chain []Info, budget *int) ([]byte, error) {
	// Only the embedded font stylesheet and its explicit font files are allowed.
	if name, ok := embeddedPreviewFontAsset(href); ok {
		raw, err := web.FS.ReadFile("admin/static/" + name)
		if err != nil {
			return nil, err
		}
		*budget += len(raw)
		if len(raw) > previewAssetLimit || *budget > previewTotalLimit {
			return nil, fmt.Errorf("theme: preview asset budget exceeded")
		}
		return raw, nil
	}
	info, name, ok := previewAsset(href, chain)
	if !ok {
		return nil, fmt.Errorf("theme: preview asset outside selected theme")
	}
	assetDir := path.Join(info.Dir, "assets")
	entry, err := os.Lstat(assetDir)
	if err != nil {
		return nil, err
	}
	if !entry.IsDir() || entry.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("theme: unsafe preview asset root")
	}
	root, err := os.OpenRoot(assetDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Size() > previewAssetLimit {
		return nil, fmt.Errorf("theme: preview asset too large or nonregular")
	}
	raw, err := io.ReadAll(io.LimitReader(file, previewAssetLimit+1))
	if err != nil {
		return nil, err
	}
	*budget += len(raw)
	if len(raw) > previewAssetLimit || *budget > previewTotalLimit {
		return nil, fmt.Errorf("theme: preview asset budget exceeded")
	}
	return raw, nil
}

var previewCSSURL = regexp.MustCompile(`(?i)url\(\s*(?:"([^"]*)"|'([^']*)'|([^\s)]+))\s*\)`)
var previewMIME = map[string]string{".woff2": "font/woff2", ".woff": "font/woff", ".ttf": "font/ttf", ".otf": "font/otf", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp", ".gif": "image/gif", ".svg": "image/svg+xml"}

func inlinePreviewCSS(href string, chain []Info, budget *int) (string, error) {
	raw, err := readPreviewAsset(href, chain, budget)
	if err != nil {
		return "", err
	}
	base, err := url.Parse(href)
	if err != nil {
		return "", err
	}
	var failure error
	css := previewCSSURL.ReplaceAllStringFunc(string(raw), func(match string) string {
		if failure != nil {
			return match
		}
		parts := previewCSSURL.FindStringSubmatch(match)
		ref := ""
		for _, part := range parts[1:] {
			if part != "" {
				ref = part
				break
			}
		}
		u, e := url.Parse(ref)
		if e != nil || ref == "" || strings.HasPrefix(ref, "#") || u.IsAbs() || u.Host != "" {
			return match
		}
		resolved := base.ResolveReference(u)
		mime := previewMIME[strings.ToLower(path.Ext(resolved.Path))]
		if mime == "" {
			return match
		}
		_, _, ok := previewAsset(resolved.String(), chain)
		_, embedded := embeddedPreviewFontAsset(resolved.String())
		if !ok && !embedded {
			return match
		}
		asset, e := readPreviewAsset(resolved.String(), chain, budget)
		if e != nil {
			failure = e
			return match
		}
		return `url("data:` + mime + `;base64,` + base64.StdEncoding.EncodeToString(asset) + `")`
	})
	return css, failure
}

func embeddedPreviewFontAsset(href string) (string, bool) {
	for _, name := range []string{"zad-font.css", "fonts/zad/Zad-Light.woff2", "fonts/zad/Zad-Regular.woff2", "fonts/zad/Zad-Medium.woff2", "fonts/zad/Zad-SemiBold.woff2", "fonts/zad/Zad-Bold.woff2"} {
		if href == "/static/admin/"+name {
			return name, true
		}
	}
	return "", false
}
