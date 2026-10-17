package content

import (
	"strings"
	"unicode"
)

// SEO drift: a stored meta title/description written for an earlier version
// of a page keeps showing in search results and browser tabs after the page
// is rewritten. The editor warns when the stored values share too few words
// with the current title or text. Empty SEO fields never warn (the page
// title and excerpt are used then).

const (
	minTitleOverlap = 0.34
	minDescOverlap  = 0.2
	driftBodyChars  = 1200
)

func seoWords(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) {
		w = strings.TrimPrefix(w, "ال") // Arabic definite article
		if len([]rune(w)) >= 3 {
			out[w] = true
		}
	}
	return out
}

// overlap is the share of a's words that also appear in b (1 when a has none).
func overlap(a, b map[string]bool) float64 {
	if len(a) == 0 {
		return 1
	}
	n := 0
	for w := range a {
		if b[w] {
			n++
		}
	}
	return float64(n) / float64(len(a))
}

// seoDrift lists the SEO fields ("title", "description") that no longer
// match the item.
func seoDrift(it *Item) []string {
	var out []string
	if t := displayString(it.SEO["title"]); t != "" && overlap(seoWords(t), seoWords(it.Title)) < minTitleOverlap {
		out = append(out, "title")
	}
	if d := displayString(it.SEO["description"]); d != "" {
		text := plainText(it.Body)
		if r := []rune(text); len(r) > driftBodyChars {
			text = string(r[:driftBodyChars])
		}
		if overlap(seoWords(d), seoWords(it.Title+" "+it.Excerpt+" "+text)) < minDescOverlap {
			out = append(out, "description")
		}
	}
	return out
}
