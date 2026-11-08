package admin

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

func canvasSearchContains(text string, terms []string) bool {
	return !slices.ContainsFunc(terms, func(term string) bool { return !strings.Contains(text, term) })
}

// Arabic spelling, marks, tatweel and both digit sets fold identically in
// queries and indexed text. Marks and bidi controls never split a word.
func normalizeCanvasSearch(text string) string {
	var out strings.Builder
	space := true
	for _, r := range strings.ToLower(text) {
		if unicode.IsMark(r) || r == '\u0640' || unicode.Is(unicode.Cf, r) {
			continue
		}
		switch r {
		case 'أ', 'إ', 'آ', 'ٱ':
			r = 'ا'
		case 'ى', 'ئ', 'ۍ', 'ی', 'ې', 'ے':
			r = 'ي'
		case 'ؤ':
			r = 'و'
		case 'ة', 'ۀ', 'ہ', 'ھ':
			r = 'ه'
		case 'ک':
			r = 'ك'
		}
		if r >= '٠' && r <= '٩' {
			r = '0' + r - '٠'
		} else if r >= '۰' && r <= '۹' {
			r = '0' + r - '۰'
		}
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			out.WriteRune(r)
			space = false
		} else if !space {
			out.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(out.String())
}

func canvasSearchSnippet(text string, terms []string) string {
	// Keep the original spelling in excerpts, but center a long excerpt near
	// its first matching word. Rune slicing cannot break Arabic or emoji UTF-8.
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	start := 0
	for index, word := range strings.Fields(text) {
		if index > 0 {
			start++
		}
		folded := normalizeCanvasSearch(word)
		if slices.ContainsFunc(terms, func(term string) bool { return strings.Contains(folded, term) }) {
			break
		}
		start += utf8.RuneCountInString(word)
	}
	if start >= len(runes) {
		start = 0
	}
	start = max(0, start-40)
	end := min(len(runes), start+180)
	snippet := string(runes[start:end])
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(runes) {
		snippet += "…"
	}
	return snippet
}
