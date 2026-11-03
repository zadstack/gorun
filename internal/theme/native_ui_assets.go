package theme

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sync"

	"github.com/zadstack/gorun/web"
	"golang.org/x/net/html"
)

const NativeUICSSPath = "/static/site/native-ui.css"

var nativeUICSS = sync.OnceValues(func() ([]byte, string) {
	stylesheet, err := web.FS.ReadFile("site/native-ui.css")
	if err != nil {
		return nil, ""
	}
	digest := sha256.Sum256(stylesheet)
	return stylesheet, hex.EncodeToString(digest[:6])
})

func NativeUICSS() []byte    { stylesheet, _ := nativeUICSS(); return stylesheet }
func NativeUICSSURL() string { _, digest := nativeUICSS(); return NativeUICSSPath + "?v=" + digest }

func previewNativeUIStylesheet(node *html.Node) bool {
	href, rel := previewLink(node)
	target, err := url.Parse(href)
	return rel == "stylesheet" && err == nil && !target.IsAbs() && target.Host == "" && target.Fragment == "" && target.Path == NativeUICSSPath
}
