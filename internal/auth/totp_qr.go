package auth

import (
	"fmt"
	"html/template"
	"strings"

	"rsc.io/qr"
)

// qrQuietZone is the light margin around the code, in modules (ISO/IEC
// 18004 asks for four).
const qrQuietZone = 4

// qrSVG draws text as a QR code (error correction level M) in one inline
// SVG element: one path with a rectangle per horizontal run of dark modules,
// on a white square. It is rendered on the server, so the page needs no
// script, image request or data: URI, and the Content-Security-Policy is
// unchanged. label is the accessible name.
func qrSVG(text, label string) (template.HTML, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", fmt.Errorf("auth: encode QR code: %w", err)
	}
	size := code.Size + 2*qrQuietZone
	var d strings.Builder
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; {
			if !code.Black(x, y) {
				x++
				continue
			}
			run := 1
			for x+run < code.Size && code.Black(x+run, y) {
				run++
			}
			fmt.Fprintf(&d, "M%d %dh%dv1h-%dz", x+qrQuietZone, y+qrQuietZone, run, run)
			x += run
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" role="img" aria-label="%s" shape-rendering="crispEdges" class="totp-qr" data-totp-qr>`,
		size, size, template.HTMLEscapeString(label))
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="%s"/></svg>`, size, size, d.String())
	return template.HTML(b.String()), nil //nolint:gosec // built from integers and an escaped label
}
