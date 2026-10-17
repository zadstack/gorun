package brand

import (
	"strings"
	"testing"
)

func TestBrandValidationAndSafeStyles(t *testing.T) {
	good := Identity{Accent: "#167252", Background: "#101010", Text: "#eeeeee", Font: "system", Width: "wide"}
	if err := Validate(good); err != nil {
		t.Fatal(err)
	}
	if css := CSS(good); !strings.Contains(css, "--gp-width:1280px") {
		t.Fatal(css)
	}
	for _, bad := range []Identity{
		{Accent: "#ff0000;}</style>", Background: good.Background, Text: good.Text, Font: good.Font, Width: good.Width},
		{Accent: good.Accent, Background: good.Background, Text: good.Text, Font: "url(https://evil.test)", Width: good.Width},
		{Accent: good.Accent, Background: good.Background, Text: good.Text, Font: good.Font, Width: "10000px"},
		{Accent: good.Accent, Background: "#eeeeee", Text: "#eeeeee", Font: good.Font, Width: good.Width},
	} {
		if Validate(bad) == nil || CSS(bad) != "" {
			t.Errorf("accepted unsafe or unreadable identity %+v", bad)
		}
	}
}

func TestZadFontIsOptInForExistingIdentities(t *testing.T) {
	identity := Identity{Accent: "#167252", Background: "#ffffff", Text: "#171717", Font: "zad", Width: "standard"}
	if err := Validate(identity); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(CSS(identity), `--font:"Zad",`) {
		t.Fatal("Zad choice missing")
	}
	identity.Font = "system"
	if strings.Contains(CSS(identity), `"Zad"`) {
		t.Fatal("explicit system identity was replaced")
	}
}
