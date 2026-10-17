package gutenberg

import (
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
)

type blockFunc func(c *converter, b *Block) string

// coreBlocks are the core blocks with a converter (spec §4.7). Most keep
// the saved markup and only lose WordPress classes; a few are rebuilt.
var coreBlocks map[string]blockFunc

func init() {
	generic := func(c *converter, b *Block) string { return c.clean(c.inner(b)) }
	empty := func(*converter, *Block) string { return "" }
	coreBlocks = map[string]blockFunc{
		"":                  generic, // freeform / classic HTML
		"core/freeform":     generic,
		"core/paragraph":    generic,
		"core/heading":      generic,
		"core/list":         generic,
		"core/list-item":    generic,
		"core/quote":        generic,
		"core/pullquote":    generic,
		"core/verse":        generic,
		"core/image":        generic,
		"core/gallery":      generic,
		"core/columns":      generic,
		"core/column":       generic,
		"core/group":        generic,
		"core/buttons":      generic,
		"core/button":       generic,
		"core/table":        generic,
		"core/code":         generic,
		"core/preformatted": generic,
		"core/details":      generic,
		"core/media-text":   generic,
		"core/separator":    func(*converter, *Block) string { return "<hr>\n" },
		"core/spacer":       empty,
		"core/more":         empty,
		"core/nextpage":     empty,
		"core/html":         func(c *converter, b *Block) string { return c.inner(b) },
		"core/shortcode":    func(c *converter, b *Block) string { return strings.TrimSpace(c.inner(b)) + "\n" },
		"core/embed":        embed,
		"core/cover":        cover,
	}
}

var (
	youtubeID = regexp.MustCompile(`(?:youtube\.com/(?:watch\?(?:.*&)?v=|embed/|shorts/)|youtu\.be/)([A-Za-z0-9_-]{6,})`)
	vimeoID   = regexp.MustCompile(`vimeo\.com/(?:video/)?(\d+)`)
	figcap    = regexp.MustCompile(`(?s)<figcaption[^>]*>(.*?)</figcaption>`)
)

// embed turns an oEmbed URL into a sandboxable iframe (YouTube, Vimeo) or a
// plain link, keeping the caption.
func embed(c *converter, b *Block) string {
	u, _ := b.Attrs["url"].(string)
	if u == "" {
		u = strings.TrimSpace(stripTags(c.inner(b)))
	}
	caption := ""
	if m := figcap.FindStringSubmatch(c.inner(b)); m != nil {
		caption = "<figcaption>" + strings.TrimSpace(m[1]) + "</figcaption>"
	}
	var body string
	switch {
	case youtubeID.MatchString(u):
		id := youtubeID.FindStringSubmatch(u)[1]
		body = fmt.Sprintf(`<iframe src="https://www.youtube-nocookie.com/embed/%s" title="YouTube" loading="lazy" allowfullscreen></iframe>`, url.PathEscape(id))
	case vimeoID.MatchString(u):
		id := vimeoID.FindStringSubmatch(u)[1]
		body = fmt.Sprintf(`<iframe src="https://player.vimeo.com/video/%s" title="Vimeo" loading="lazy" allowfullscreen></iframe>`, id)
	case u != "":
		esc := html.EscapeString(u)
		body = `<a href="` + esc + `">` + esc + `</a>`
	default:
		return ""
	}
	return "<figure>" + body + caption + "</figure>\n"
}

// cover becomes a figure: the background image plus the inner blocks as
// the caption.
func cover(c *converter, b *Block) string {
	inner := strings.TrimSpace(c.children(b))
	u, _ := b.Attrs["url"].(string)
	if u == "" {
		return "<div>" + inner + "</div>\n"
	}
	alt, _ := b.Attrs["alt"].(string)
	if alt == "" {
		alt = attrOf(c.inner(b), "img", "alt")
	}
	return `<figure><img src="` + html.EscapeString(u) + `" alt="` + html.EscapeString(alt) + `"><figcaption>` + inner + "</figcaption></figure>\n"
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

func stripTags(s string) string { return html.UnescapeString(tagRe.ReplaceAllString(s, "")) }

// attrOf returns attribute attr of the first <tag> in s.
func attrOf(s, tag, attr string) string {
	re := regexp.MustCompile(`<` + tag + `\b[^>]*\s` + attr + `="([^"]*)"`)
	if m := re.FindStringSubmatch(s); m != nil {
		return html.UnescapeString(m[1])
	}
	return ""
}
