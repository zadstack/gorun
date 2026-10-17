package builder

import "github.com/zadstack/gorun/internal/brand"

type siteDesign struct {
	Theme    string
	Identity brand.Identity
}

func starterDesign(kind string) siteDesign {
	switch kind {
	case "company":
		return siteDesign{"business", brand.Identity{Accent: "#0f766e", Background: "#f8faf9", Text: "#172b29", Font: "zad", Width: "wide"}}
	case "portfolio":
		return siteDesign{"portfolio", brand.Identity{Accent: "#b91c1c", Background: "#fafafa", Text: "#171717", Font: "zad", Width: "wide"}}
	case "magazine":
		return siteDesign{"magazine", brand.Identity{Accent: "#9f2b19", Background: "#faf9f6", Text: "#18211c", Font: "serif", Width: "wide"}}
	default:
		return siteDesign{"default", brand.Default()}
	}
}
