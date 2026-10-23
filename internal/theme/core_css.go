package theme

import (
	"crypto/sha256"
	"encoding/hex"
	"html/template"
	"sync"

	"github.com/zadstack/gorun/web"
)

// CoreCSSPath is where the core site stylesheet (web/site/core.css, built by
// tools/tailwind/build.sh) is served. Every theme links it through
// {{sections .}}: element defaults for forms, tables and long content, plus
// the utilities of the shared functional templates (store, account, product,
// intake, form result) scoped under .gp-core. Its rules have zero specificity
// or sit under .gp-core, so a theme's own CSS still wins.
const CoreCSSPath = "/static/site/core.css"

var coreCSS = sync.OnceValues(func() ([]byte, string) {
	b, err := web.FS.ReadFile("site/core.css")
	if err != nil {
		return nil, ""
	}
	sum := sha256.Sum256(b)
	return b, hex.EncodeToString(sum[:6])
})

// CoreCSS returns the embedded core site stylesheet.
func CoreCSS() []byte { b, _ := coreCSS(); return b }

// CoreCSSURL is CoreCSSPath with a content hash, so a new release never serves
// a stale copy from a browser or proxy cache.
func CoreCSSURL() string {
	_, v := coreCSS()
	return CoreCSSPath + "?v=" + v
}

// coreStylesheets are the links every theme head gets from {{sections .}}:
// the core stylesheet always, and the store stylesheet on commerce pages
// (resolved through the theme chain, so a child theme gets default's file).
func (f *funcs) coreStylesheets(tc *Context) string {
	out := `<link rel="stylesheet" href="` + template.HTMLEscapeString(CoreCSSURL()) + `">`
	if tc != nil {
		if on, _ := tc.Extra["commerce"].(bool); on {
			out += `<link rel="stylesheet" href="` + template.HTMLEscapeString(f.asset("commerce.css")) + `">`
		}
	}
	return out
}
