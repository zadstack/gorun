package builder

import "github.com/zadstack/gorun/internal/brand"

type siteDesign struct {
	Theme    string
	Identity brand.Identity
}

func starterDesign(kind string) siteDesign {
	switch kind {
	case "company":
		return siteDesign{"business", brand.Identity{Accent: "#1e6650", Background: "#f8fbf9", Text: "#172d25", Font: "zad", Width: "wide"}}
	case "portfolio":
		return siteDesign{"portfolio", brand.Identity{Accent: "#6b3fa0", Background: "#fbf9fd", Text: "#292132", Font: "zad", Width: "wide"}}
	case "magazine":
		return siteDesign{"magazine", brand.Identity{Accent: "#89402e", Background: "#f4f0e8", Text: "#211e19", Font: "serif", Width: "wide"}}
	default:
		return siteDesign{"default", brand.Default()}
	}
}
