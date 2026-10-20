package builder

import "github.com/zadstack/gorun/internal/brand"

type siteDesign struct {
	Theme    string
	Identity brand.Identity
}

func starterDesign(kind string) siteDesign {
	switch kind {
	case "company":
		return siteDesign{"business", brand.Identity{Accent: "#0f766e", Background: "#fafbfc", Text: "#0b0f14", Font: "zad", Width: "wide"}}
	case "portfolio":
		return siteDesign{"portfolio", brand.Identity{Accent: "#3f5d4a", Background: "#f1efe9", Text: "#2a2824", Font: "zad", Width: "wide"}}
	case "magazine":
		return siteDesign{"magazine", brand.Identity{Accent: "#1e3a5f", Background: "#f4f5f7", Text: "#151a22", Font: "serif", Width: "wide"}}
	default:
		return siteDesign{"default", brand.Default()}
	}
}
