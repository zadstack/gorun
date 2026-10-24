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

// A light accent (amber) is accepted — the builder derives a readable near-black
// label instead of forcing the accent darker — and the brand CSS emits the
// matching --on-accent/--gp-on-accent. A dark accent keeps white labels.
func TestBuilderAcceptsLightAccentWithReadableLabel(t *testing.T) {
	amber := Identity{Accent: "#f5b800", Background: "#ffffff", Text: "#111111", Font: "zad", Width: "standard"}
	if err := Validate(amber); err != nil {
		t.Fatalf("light accent rejected: %v", err)
	}
	if on := OnAccent(amber.Accent); on != "#111111" || contrast(on, amber.Accent) < 4.5 {
		t.Fatalf("OnAccent(amber) = %s", on)
	}
	css := CSS(amber)
	if !strings.Contains(css, "--on-accent:#111111;") || !strings.Contains(css, "--gp-on-accent:#111111;") {
		t.Fatalf("amber CSS missing readable on-accent: %s", css)
	}

	// The legacy default (a dark accent) still validates and keeps white labels.
	def := Default()
	if err := Validate(def); err != nil {
		t.Fatalf("default rejected: %v", err)
	}
	if on := OnAccent(def.Accent); on != "#ffffff" {
		t.Fatalf("OnAccent(default) = %s, want #ffffff", on)
	}
	if !strings.Contains(CSS(def), "--on-accent:#ffffff;") {
		t.Fatalf("default CSS missing white on-accent: %s", CSS(def))
	}

	// Every opaque accent is accepted (white or black always reaches AA) and the
	// derived label reaches 4.5:1. None of these is skipped, so a future
	// regression that rejected one could not pass silently.
	for _, accent := range []string{"#000000", "#ffffff", "#167252", "#f5b800", "#777777", "#8a6410", "#123456"} {
		id := Identity{Accent: accent, Background: "#ffffff", Text: "#111111", Font: "zad", Width: "standard"}
		if err := Validate(id); err != nil {
			t.Errorf("valid accent %s rejected: %v", accent, err)
			continue
		}
		if r := contrast(OnAccent(accent), accent); r < 4.5 {
			t.Errorf("OnAccent(%s) only %.2f:1", accent, r)
		}
	}
	// A mid-tone where neither white nor near-black reaches AA still gets a
	// readable label — black — rather than being refused.
	if on := OnAccent("#777777"); on != "#000000" {
		t.Fatalf("OnAccent(#777777) = %s, want #000000", on)
	}
}
