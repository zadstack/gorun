package theme

import (
	"html/template"

	"github.com/zadstack/gorun/internal/brand"
	"github.com/zadstack/gorun/internal/httpx"
)

func (f *funcs) builderStyle() template.CSS {
	if f.st.previewIdentity != nil {
		return template.CSS(brand.CSS(*f.st.previewIdentity))
	}
	var identity brand.Identity
	if err := f.s.d.Settings.Get(f.st.ctx, "builder.identity", &identity); err != nil {
		return ""
	}
	return template.CSS(brand.CSS(identity))
}

// builderIdentity emits a cached stylesheet link for builder brand tokens.
// Preview sessions (no settings-backed URL) keep a nonced inline <style>.
func (f *funcs) builderIdentity() template.HTML {
	css := string(f.builderStyle())
	if css == "" {
		return ""
	}
	if f.st.previewIdentity != nil {
		nonce := httpx.Nonce(f.st.ctx)
		return template.HTML(`<style nonce="` + template.HTMLEscapeString(nonce) + `" data-gorun-builder-identity>` + css + `</style>`)
	}
	return template.HTML(`<link rel="stylesheet" href="/site/builder-identity.css?v=` + contentHash(css) + `" data-gorun-builder-identity>`)
}

// BuilderIdentityStylesheet returns the builder brand CSS body, or "".
func BuilderIdentityStylesheet(identity brand.Identity) string {
	return brand.CSS(identity)
}
