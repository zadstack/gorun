package theme

import (
	"github.com/zadstack/gorun/internal/brand"
	"html/template"
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
