package media

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// ErrInvalidSVG is returned for documents that are not well-formed SVG.
var ErrInvalidSVG = errors.New("media: invalid SVG document")

// svgElements is the element allowlist (lower-cased local names). Anything
// else — script, foreignObject, iframe, animate/set (which can rewrite href),
// feImage, prefixed editor metadata — is dropped with its whole subtree.
var svgElements = toSet("svg", "g", "defs", "title", "desc", "symbol", "use", "path", "rect", "circle",
	"ellipse", "line", "polyline", "polygon", "text", "tspan", "textpath", "lineargradient", "radialgradient",
	"stop", "clippath", "mask", "pattern", "filter", "marker", "metadata", "style", "a", "image", "switch",
	"feblend", "fecolormatrix", "fecomponenttransfer", "fecomposite", "feconvolvematrix", "fediffuselighting",
	"fedisplacementmap", "fedistantlight", "fedropshadow", "feflood", "fefunca", "fefuncb", "fefuncg", "fefuncr",
	"fegaussianblur", "femerge", "femergenode", "femorphology", "feoffset", "fepointlight", "fespecularlighting",
	"fespotlight", "fetile", "feturbulence")

// svgNSAttrs are the namespaced attributes kept (prefix:local, lower-cased).
var svgNSAttrs = toSet("xlink:href", "xlink:title", "xml:space", "xml:lang")

func toSet(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// normalizeValue lower-cases v and removes whitespace/control characters so
// "java\tscript:" or " JaVaScRiPt:" cannot slip through.
func normalizeValue(v string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(v) {
		if r > 0x20 && r != 0x7f {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// dangerousValue reports values that can execute or load script.
func dangerousValue(v string) bool {
	n := normalizeValue(v)
	return strings.Contains(n, "javascript:") || strings.Contains(n, "vbscript:") ||
		strings.Contains(n, "data:text/html") || strings.Contains(n, "expression(") || strings.Contains(n, "@import")
}

// cssLoaders are CSS functions that fetch a resource without url():
// image-set (and -webkit-image-set), src, image, cross-fade. They are never
// needed in a self-contained SVG, so any use is rejected.
var cssLoaders = []string{"image-set(", "src(", "image(", "cross-fade("}

// dangerousCSS additionally rejects url() references that are not local
// fragments (url(#id)), the cssLoaders, and any backslash: CSS escapes
// ("u\72 l(", "@\69 mport") would otherwise smuggle those past the
// substring checks, and plain SVG never needs them.
func dangerousCSS(v string) bool {
	if dangerousValue(v) || strings.ContainsRune(v, '\\') {
		return true
	}
	n := normalizeValue(v)
	for _, f := range cssLoaders {
		if strings.Contains(n, f) {
			return true
		}
	}
	for i := strings.Index(n, "url("); i >= 0; i = strings.Index(n, "url(") {
		rest := strings.TrimLeft(n[i+4:], `'"`)
		if !strings.HasPrefix(rest, "#") {
			return true
		}
		n = n[i+4:]
	}
	return false
}

var safeImageData = []string{"data:image/png;", "data:image/jpeg;", "data:image/gif;", "data:image/webp;"}

// safeHref validates href/xlink:href per element: <use> may only reference
// local fragments, <image> only embedded raster data, <a> http(s), relative
// or fragment links.
func safeHref(elem, v string) bool {
	n := normalizeValue(v)
	switch elem {
	case "use", "textpath", "lineargradient", "radialgradient", "pattern", "filter", "mask", "clippath":
		return strings.HasPrefix(n, "#")
	case "image":
		for _, p := range safeImageData {
			if strings.HasPrefix(n, p) {
				return true
			}
		}
		return false
	case "a":
		if dangerousValue(v) {
			return false
		}
		if i := strings.Index(n, ":"); i >= 0 && !strings.ContainsAny(n[:i], "/?#") {
			return strings.HasPrefix(n, "http:") || strings.HasPrefix(n, "https:") || strings.HasPrefix(n, "mailto:")
		}
		return true
	}
	return strings.HasPrefix(n, "#")
}

func qname(n xml.Name) string {
	if n.Space != "" {
		return n.Space + ":" + n.Local
	}
	return n.Local
}

func newSVGDecoder(data []byte) *xml.Decoder {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = true
	d.Entity = xml.HTMLEntity
	return d
}

// isSVG reports whether data is well-formed XML whose root element is svg.
func isSVG(data []byte) bool {
	d := newSVGDecoder(data)
	for {
		tok, err := d.RawToken()
		if err != nil {
			return false
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se.Name.Space == "" && se.Name.Local == "svg"
		}
	}
}

// Namespaces allowed on declarations: the default namespace must be SVG,
// and only the xlink and svg prefixes may be declared.
const (
	svgNS   = "http://www.w3.org/2000/svg"
	xlinkNS = "http://www.w3.org/1999/xlink"
)

var allowedPrefixNS = map[string]string{"xlink": xlinkNS, "svg": svgNS}

// sanitizeSVG re-serializes data keeping only allowlisted elements and safe
// attributes: no event handlers, no script-bearing or external URLs (every
// attribute value gets the url(#local)-only rule), no comments, processing
// instructions or DTDs, and no foreign namespaces. The root element must be
// svg in the SVG namespace. A <style> element's text (CharData and CDATA
// joined, comments and child elements dropped) is validated as a whole and
// the element is dropped entirely when it is unsafe.
func sanitizeSVG(data []byte) ([]byte, error) {
	d := newSVGDecoder(data)
	var out bytes.Buffer
	var stack []string // open (kept) element names, for matching end tags
	skip := 0          // depth inside a dropped subtree
	seenRoot := false

	// <style> state: styleDepth > 0 while inside a kept <style>.
	styleDepth := 0
	var styleStart string
	var styleText bytes.Buffer

	for {
		tok, err := d.RawToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, ErrInvalidSVG
		}
		if styleDepth > 0 {
			switch t := tok.(type) {
			case xml.StartElement:
				styleDepth++ // child elements inside <style> are dropped
			case xml.EndElement:
				styleDepth--
				if styleDepth == 0 {
					if !dangerousCSS(styleText.String()) {
						out.WriteString(styleStart)
						_ = xml.EscapeText(&out, styleText.Bytes())
						out.WriteString("</" + stack[len(stack)-1] + ">")
					}
					stack = stack[:len(stack)-1]
				}
			case xml.CharData:
				if styleDepth == 1 {
					styleText.Write(t) // CDATA arrives as CharData too
				}
			}
			continue // comments, PIs and directives inside <style> vanish
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if skip > 0 {
				skip++
				continue
			}
			local := strings.ToLower(t.Name.Local)
			if !seenRoot {
				if t.Name.Space != "" || local != "svg" || defaultNS(t) != svgNS {
					return nil, ErrInvalidSVG
				}
				seenRoot = true
			}
			if t.Name.Space != "" || !svgElements[local] {
				skip = 1
				continue
			}
			var start bytes.Buffer
			writeStart(&start, t, local)
			stack = append(stack, qname(t.Name))
			if local == "style" {
				styleDepth, styleStart = 1, start.String()
				styleText.Reset()
				continue
			}
			out.Write(start.Bytes())
		case xml.EndElement:
			if skip > 0 {
				skip--
				continue
			}
			if len(stack) == 0 {
				return nil, ErrInvalidSVG
			}
			out.WriteString("</" + stack[len(stack)-1] + ">")
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if skip > 0 || len(stack) == 0 {
				continue
			}
			_ = xml.EscapeText(&out, t)
		}
		// Comments, processing instructions and directives are dropped.
	}
	if !seenRoot || len(stack) != 0 || styleDepth != 0 {
		return nil, ErrInvalidSVG
	}
	return out.Bytes(), nil
}

// defaultNS returns the element's default namespace declaration.
func defaultNS(t xml.StartElement) string {
	for _, a := range t.Attr {
		if a.Name.Space == "" && a.Name.Local == "xmlns" {
			return strings.TrimSpace(a.Value)
		}
	}
	return ""
}

func writeStart(out *bytes.Buffer, t xml.StartElement, local string) {
	out.WriteString("<" + qname(t.Name))
	for _, a := range t.Attr {
		name := qname(a.Name)
		lname := strings.ToLower(name)
		switch {
		case a.Name.Space == "" && a.Name.Local == "xmlns":
			if strings.TrimSpace(a.Value) != svgNS {
				continue // only the SVG default namespace
			}
		case a.Name.Space == "xmlns":
			if want, ok := allowedPrefixNS[a.Name.Local]; !ok || strings.TrimSpace(a.Value) != want {
				continue
			}
		case strings.HasPrefix(strings.ToLower(a.Name.Local), "on"):
			continue
		case a.Name.Space != "" && !svgNSAttrs[lname]:
			continue
		case lname == "href" || lname == "xlink:href":
			if !safeHref(local, a.Value) {
				continue
			}
		default:
			// style, fill, filter, mask, clip-path, marker-* … : no script
			// URLs and url() may only reference local fragments.
			if dangerousCSS(a.Value) {
				continue
			}
		}
		out.WriteString(" " + name + `="`)
		_ = xml.EscapeText(out, []byte(a.Value))
		out.WriteString(`"`)
	}
	out.WriteString(">")
}
