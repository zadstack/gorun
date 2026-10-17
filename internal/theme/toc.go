package theme

import (
	"html/template"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// TOCItem is one h2/h3 of a body, for an "on this page" list.
type TOCItem struct {
	ID    string
	Text  string
	Level int
}

// TOC is a body whose h2 and h3 headings all carry an id, with those headings
// in order.
type TOC struct {
	Body  template.HTML
	Items []TOCItem
}

// toc anchors the h2/h3 headings of an item's body (or an HTML string):
// headings without an id get one from their text, unique within the body;
// existing ids are kept. The body is already sanitized; only the start tags
// of id-less headings are rewritten, everything else passes through as is.
func toc(v any) TOC {
	body := bodyHTML(v)
	if body == "" {
		return TOC{}
	}
	type piece struct {
		raw string
		tok html.Token
	}
	var pieces []piece
	z := html.NewTokenizer(strings.NewReader(body))
	for z.Next() != html.ErrorToken {
		raw := string(z.Raw())
		pieces = append(pieces, piece{raw: raw, tok: z.Token()})
	}
	used := map[string]int{}
	for _, p := range pieces {
		if p.tok.Type == html.StartTagToken {
			for _, a := range p.tok.Attr {
				if a.Key == "id" {
					used[a.Val]++
				}
			}
		}
	}
	var out TOC
	for i := 0; i < len(pieces); i++ {
		tok := pieces[i].tok
		if tok.Type != html.StartTagToken || (tok.DataAtom != atom.H2 && tok.DataAtom != atom.H3) {
			continue
		}
		var text strings.Builder
		for j := i + 1; j < len(pieces); j++ {
			t := pieces[j].tok
			if t.Type == html.EndTagToken && t.DataAtom == tok.DataAtom {
				break
			}
			if t.Type == html.TextToken {
				text.WriteString(t.Data)
				text.WriteByte(' ')
			}
		}
		label := strings.Join(strings.Fields(text.String()), " ")
		id := ""
		for _, a := range tok.Attr {
			if a.Key == "id" {
				id = a.Val
			}
		}
		if id == "" {
			id = uniqueAnchor(anchorSlug(label), used)
			tok.Attr = append(tok.Attr, html.Attribute{Key: "id", Val: id})
			pieces[i].raw = tok.String()
		}
		level := 2
		if tok.DataAtom == atom.H3 {
			level = 3
		}
		out.Items = append(out.Items, TOCItem{ID: id, Text: label, Level: level})
	}
	var b strings.Builder
	for _, p := range pieces {
		b.WriteString(p.raw)
	}
	out.Body = template.HTML(b.String())
	return out
}

// bodyHTML is an item's body, or the HTML string itself.
func bodyHTML(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case template.HTML:
		return string(x)
	}
	if it := itemOf(v); it != nil {
		return it.Body
	}
	return ""
}

// anchorSlug keeps letters and digits (any script), lower-cased, joined by
// single hyphens; "section" when nothing is left.
func anchorSlug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
			continue
		}
		dash = true
	}
	if b.Len() == 0 {
		return "section"
	}
	return b.String()
}

func uniqueAnchor(base string, used map[string]int) string {
	id := base
	for n := 2; used[id] > 0; n++ {
		id = base + "-" + strconv.Itoa(n)
	}
	used[id]++
	return id
}
