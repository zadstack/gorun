package identitylogoadmin

import (
	"bytes"
	coreadmin "github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/identitylogo"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestDecimalBudgetRejectsSignsExponentsAndMalformedFractions(t *testing.T) {
	for _, raw := range []string{"1.-1", "1.+1", "+1", "-1", "1e3", "NaN", "1.", ".5", "1.0000001", "99999999", "1.2.3", " 1"} {
		if _, err := amount(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	for raw, want := range map[string]int64{"0": 0, "0.000001": 1, "1.25": 1250000, "10": 10000000} {
		got, err := amount(raw)
		if err != nil || got != want {
			t.Errorf("%q=%d %v", raw, got, err)
		}
	}
}
func TestSelectedPNGDerivativesPreserveAlphaAndActualDimensions(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 1024, 1024))
	for y := 0; y < 1024; y++ {
		for x := 0; x < 1024; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: 120, G: 30, B: 200, A: 128})
		}
	}
	var original bytes.Buffer
	if err := png.Encode(&original, source); err != nil {
		t.Fatal(err)
	}
	for slot, size := range map[string]int{"site.logo": 512, "site.logo_light": 512, "site.logo_dark": 512, "site.icon": 64} {
		raw, err := selectedLogoPNG(original.Bytes(), slot)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := png.Decode(bytes.NewReader(raw))
		if err != nil || decoded.Bounds().Dx() != size || decoded.Bounds().Dy() != size {
			t.Fatalf("wrong derivative for %s: %v", slot, err)
		}
		_, _, _, alpha := decoded.At(0, 0).RGBA()
		if alpha != 128*257 {
			t.Fatalf("alpha changed for %s: %d", slot, alpha)
		}
	}
}

// Render the shipped extension through the real resolver, including nested
// profile/review/candidate contexts. This catches a valid but undiscovered page
// or a page defining the wrong layout block.
func TestEmbeddedLogoPageRendersWorkflow(t *testing.T) {
	bundle, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	tpl, err := httpx.NewTemplates(fstest.MapFS{
		"admin/templates/layouts/admin.html": {Data: []byte(`{{template "content" .}}`)},
	}, false, bundle, httpx.Funcs{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := tpl.RegisterExtension(files); err != nil {
		t.Fatal(err)
	}
	v := pageView{CanUpload: true, Profiles: []coreadmin.LogoProfile{{ID: "profile", Name: "Synthetic", Configured: true, Estimate: "0.100000", Binding: coreadmin.LogoBinding{EstimateMicros: 100000}}}, Review: &identitylogo.Review{Token: "review"}, Candidate: &identitylogo.Candidate{ID: "candidate", Width: 1024, Height: 1024}}
	w := httptest.NewRecorder()
	tpl.Render(w, httptest.NewRequest("GET", "/admin/appearance/identity/logo", nil), "pages/identitylogo/index.html", v)
	if w.Code != 200 {
		t.Fatalf("render: %d %s", w.Code, w.Body.String())
	}
	for _, marker := range []string{"data-logo-brief", "data-logo-review", "data-logo-candidate", "data-logo-config", "Synthetic", "0.100000"} {
		if !strings.Contains(w.Body.String(), marker) {
			t.Errorf("render missing %s", marker)
		}
	}
}
