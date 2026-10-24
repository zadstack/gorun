package brand

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
)

type Identity struct {
	Accent     string `json:"accent"`
	Background string `json:"background"`
	Text       string `json:"text"`
	Font       string `json:"font"`
	Width      string `json:"width"`
}

var (
	ErrColors         = errors.New("brand: colors must use #RRGGBB")
	ErrFont           = errors.New("brand: invalid font")
	ErrWidth          = errors.New("brand: invalid page width")
	ErrTextContrast   = errors.New("brand: text needs at least 4.5:1 contrast against the background")
	ErrButtonContrast = errors.New("brand: no readable label color reaches AA on this accent; choose a less mid-toned accent")
)
var colorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
var fonts = map[string]string{"zad": `"Zad",system-ui,-apple-system,"Segoe UI",Tahoma,sans-serif`, "system": `system-ui,-apple-system,"Segoe UI",Tahoma,sans-serif`, "serif": `Georgia,"Times New Roman",serif`, "mono": `ui-monospace,SFMono-Regular,Consolas,monospace`}
var widths = map[string]string{"narrow": "800px", "standard": "1080px", "wide": "1280px"}

func Default() Identity {
	return Identity{Accent: "#167252", Background: "#f8faf9", Text: "#172b29", Font: "zad", Width: "standard"}
}
func Validate(identity Identity) error {
	for _, value := range []string{identity.Accent, identity.Background, identity.Text} {
		if !colorPattern.MatchString(value) {
			return ErrColors
		}
	}
	if _, ok := fonts[identity.Font]; !ok {
		return ErrFont
	}
	if _, ok := widths[identity.Width]; !ok {
		return ErrWidth
	}
	if contrast(identity.Text, identity.Background) < 4.5 {
		return ErrTextContrast
	}
	// A readable label is derived (white, near-black or black), so a light
	// accent such as amber is accepted instead of being forced darker. For any
	// opaque color the contrasts of white and black multiply to 21, so one
	// always reaches at least sqrt(21) ~= 4.58:1; this guard is therefore
	// defensive and does not fire for a valid #RRGGBB accent.
	if contrast(OnAccent(identity.Accent), identity.Accent) < 4.5 {
		return ErrButtonContrast
	}
	return nil
}

// OnAccent is the label color for a fill of accent: white when it reaches WCAG
// AA 4.5:1, else near-black, else black. For any opaque color white and black
// multiply to a contrast of 21, so one always reaches at least sqrt(21) ~=
// 4.58:1 — a readable label always exists, and a light accent such as amber
// keeps readable labels instead of being rejected. The builder's brand CSS
// uses this; the theme identity layer applies the same rule and will share this
// implementation in a later change. accent must be #RRGGBB (as Validate
// requires); other input returns white.
func OnAccent(accent string) string {
	if !colorPattern.MatchString(accent) {
		return "#ffffff"
	}
	for _, c := range []string{"#ffffff", "#111111"} {
		if contrast(c, accent) >= 4.5 {
			return c
		}
	}
	return "#000000"
}
func luminance(color string) float64 {
	sum := 0.0
	for i, weight := range []float64{.2126, .7152, .0722} {
		value, _ := strconv.ParseUint(color[1+i*2:3+i*2], 16, 8)
		channel := float64(value) / 255
		if channel <= .04045 {
			channel /= 12.92
		} else {
			channel = math.Pow((channel+.055)/1.055, 2.4)
		}
		sum += channel * weight
	}
	return sum
}
func contrast(a, b string) float64 {
	light, dark := luminance(a), luminance(b)
	if light < dark {
		light, dark = dark, light
	}
	return (light + .05) / (dark + .05)
}

// CSS emits only validated tokens, shared by live themes and sandbox previews.
func CSS(identity Identity) string {
	if Validate(identity) != nil {
		return ""
	}
	on := OnAccent(identity.Accent)
	return fmt.Sprintf(`:root{--accent:%s;--accent-hover:var(--accent);--accent-ink:var(--accent);--on-accent:%s;--gp-on-accent:%s;--gp-accent:%s;--gp-bg:%s;--gp-text:%s;--gp-width:%s;--green:%s;--green-hover:%s;--blue:%s;--blue-hover:%s;--bg:%s;--panel:%s;--panel-2:color-mix(in srgb,%s 94%%,%s);--ink:%s;--head:var(--ink);--ink-2:var(--ink);--muted:%s;--line:color-mix(in srgb,%s 20%%,%s);--line-strong:color-mix(in srgb,%s 40%%,%s);--font:%s;--font-ui:%s;--radius:0px;}body{font-family:var(--font);background:var(--bg);color:var(--ink);margin:0;}.gp-site-width{max-width:var(--gp-width,64rem)!important;}`, identity.Accent, on, on, identity.Accent, identity.Background, identity.Text, widths[identity.Width], identity.Accent, identity.Accent, identity.Text, identity.Text, identity.Background, identity.Background, identity.Background, identity.Text, identity.Text, identity.Text, identity.Text, identity.Background, identity.Text, identity.Background, fonts[identity.Font], fonts[identity.Font])
}
