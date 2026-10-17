package gutenberg

import (
	"html"
	"regexp"
	"sort"
	"strings"

	xhtml "golang.org/x/net/html"
)

// Options tune conversion.
type Options struct {
	// KeepClasses keeps WordPress classes (wp-block-*, has-*, is-*, …) and
	// inline styles. Note that content.Sanitize strips non-editor classes and
	// all styles on save anyway; the option matters for raw exports.
	KeepClasses bool
}

// Result is a conversion outcome.
type Result struct {
	HTML string
	// Unknown counts blocks without a core converter (inner HTML kept).
	Unknown map[string]int
	// Shortcodes counts shortcode tags left in place.
	Shortcodes map[string]int
}

// UnknownNames returns the unknown block names sorted.
func (r Result) UnknownNames() []string {
	out := make([]string, 0, len(r.Unknown))
	for k := range r.Unknown {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ConvertRaw converts raw post content: block markup when present,
// otherwise it is treated as classic/rendered HTML and cleaned.
func ConvertRaw(raw string, o Options) Result {
	if !HasBlocks(raw) {
		out := CleanHTML(raw, o)
		return Result{HTML: out, Unknown: map[string]int{}, Shortcodes: Shortcodes(out)}
	}
	return Convert(Parse(raw), o)
}

// Convert renders blocks to clean HTML.
func Convert(blocks []*Block, o Options) Result {
	c := &converter{o: o, unknown: map[string]int{}}
	var sb strings.Builder
	for _, b := range blocks {
		if out := strings.TrimSpace(c.block(b)); out != "" {
			sb.WriteString(out)
			sb.WriteString("\n\n")
		}
	}
	out := tidy(sb.String())
	return Result{HTML: out, Unknown: c.unknown, Shortcodes: Shortcodes(out)}
}

type converter struct {
	o       Options
	unknown map[string]int
}

// inner renders the block's parts with its children converted.
func (c *converter) inner(b *Block) string {
	var sb strings.Builder
	for _, p := range b.Parts {
		if p.Child != nil {
			sb.WriteString(c.block(p.Child))
		} else {
			sb.WriteString(p.HTML)
		}
	}
	return sb.String()
}

// children renders only the inner blocks.
func (c *converter) children(b *Block) string {
	var sb strings.Builder
	for _, ch := range b.Inner {
		sb.WriteString(c.block(ch))
	}
	return sb.String()
}

func (c *converter) block(b *Block) string {
	if fn, ok := coreBlocks[b.Name]; ok {
		return fn(c, b)
	}
	c.unknown[b.Name]++
	return c.clean(c.inner(b))
}

func (c *converter) clean(s string) string { return CleanHTML(s, c.o) }

var shortcodeRe = regexp.MustCompile(`\[([a-z][a-z0-9_-]*)(?:\s[^\]]*)?/?\]`)

// Shortcodes counts shortcode tags ([tag ...]) in s; closing tags are not
// counted. Tags must look like WordPress shortcode names (lowercase,
// starting with a letter), so text such as [PDF] or [1] is ignored.
func Shortcodes(s string) map[string]int {
	out := map[string]int{}
	for _, m := range shortcodeRe.FindAllStringSubmatch(s, -1) {
		out[m[1]]++
	}
	return out
}

// wpClass matches classes WordPress generates for block styling.
var wpClass = regexp.MustCompile(`^(wp-|has-|is-|are-|align(left|right|center|wide|full)$|size-|columns-\d|alignnone$)`)

// CleanHTML strips WordPress block classes and inline styles (unless
// o.KeepClasses) from rendered HTML, drops empty class attributes and
// collapses runs of blank lines. Text, comments and end tags are kept byte
// for byte.
func CleanHTML(s string, o Options) string {
	if s == "" {
		return ""
	}
	if o.KeepClasses {
		return tidy(s)
	}
	z := xhtml.NewTokenizer(strings.NewReader(s))
	var sb strings.Builder
	for {
		tt := z.Next()
		switch tt {
		case xhtml.ErrorToken:
			return tidy(sb.String())
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			raw := string(z.Raw())
			tok := z.Token()
			sb.WriteString(rebuildTag(tok, raw, tt == xhtml.SelfClosingTagToken))
		default:
			sb.Write(z.Raw())
		}
	}
}

// rebuildTag re-serializes a start tag without WordPress classes and
// styles; tags that need no change are written as they came.
func rebuildTag(tok xhtml.Token, raw string, selfClosing bool) string {
	changed := false
	attrs := make([]xhtml.Attribute, 0, len(tok.Attr))
	for _, a := range tok.Attr {
		switch a.Key {
		case "style":
			changed = true
			continue
		case "class":
			var keep []string
			for _, cl := range strings.Fields(a.Val) {
				if !wpClass.MatchString(cl) {
					keep = append(keep, cl)
				}
			}
			if strings.Join(keep, " ") != a.Val {
				changed = true
			}
			if len(keep) == 0 {
				continue
			}
			a.Val = strings.Join(keep, " ")
		}
		attrs = append(attrs, a)
	}
	if !changed {
		return raw
	}
	var sb strings.Builder
	sb.WriteByte('<')
	sb.WriteString(tok.Data)
	for _, a := range attrs {
		sb.WriteByte(' ')
		sb.WriteString(a.Key)
		sb.WriteString(`="`)
		sb.WriteString(html.EscapeString(a.Val))
		sb.WriteByte('"')
	}
	if selfClosing {
		sb.WriteString("/")
	}
	sb.WriteByte('>')
	return sb.String()
}

var blankLines = regexp.MustCompile(`\n[ \t]*\n(?:[ \t]*\n)+`)

// tidy collapses 3+ newlines to one blank line and trims.
func tidy(s string) string {
	return strings.TrimSpace(blankLines.ReplaceAllString(s, "\n\n"))
}

var blockTag = regexp.MustCompile(`(?i)<(p|div|h[1-6]|ul|ol|table|figure|blockquote|pre|section)\b`)

// Autop wraps classic-editor content (no block markup, no block-level
// tags) in paragraphs split on blank lines, with single newlines as <br>,
// like WordPress's wpautop for the simple case.
func Autop(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if s == "" || blockTag.MatchString(s) {
		return s
	}
	var sb strings.Builder
	for _, para := range regexp.MustCompile(`\n\s*\n`).Split(s, -1) {
		if para = strings.TrimSpace(para); para != "" {
			sb.WriteString("<p>" + strings.ReplaceAll(para, "\n", "<br>\n") + "</p>\n")
		}
	}
	return sb.String()
}
