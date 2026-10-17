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
	ErrButtonContrast = errors.New("brand: choose a darker accent so white button labels remain readable")
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
	if contrast("#ffffff", identity.Accent) < 4.5 {
		return ErrButtonContrast
	}
	return nil
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
	return fmt.Sprintf(`:root{--gp-accent:%s;--gp-bg:%s;--gp-text:%s;--gp-width:%s;--green:%s;--green-hover:%s;--blue:%s;--blue-hover:%s;--bg:%s;--panel:%s;--panel-2:color-mix(in srgb,%s 94%%,%s);--ink:%s;--muted:%s;--line:color-mix(in srgb,%s 20%%,%s);--line-strong:color-mix(in srgb,%s 40%%,%s);--font:%s;--font-ui:%s;--radius:0px;}body{font-family:var(--font);background:var(--bg);color:var(--ink);margin:0;}.gp-site-width{max-width:var(--gp-width,64rem)!important;}`, identity.Accent, identity.Background, identity.Text, widths[identity.Width], identity.Accent, identity.Accent, identity.Text, identity.Text, identity.Background, identity.Background, identity.Background, identity.Text, identity.Text, identity.Text, identity.Text, identity.Background, identity.Text, identity.Background, fonts[identity.Font], fonts[identity.Font])
}
