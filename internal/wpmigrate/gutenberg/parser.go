// Package gutenberg parses WordPress block markup (<!-- wp:ns/name {json} -->
// … <!-- /wp:ns/name -->) into a tree and converts it to clean HTML for
// GoRun (spec §4.7). Rendered content (no block comments, as returned by
// the public REST API) goes through CleanHTML instead.
package gutenberg

import (
	"encoding/json"
	"strings"
)

// Block is one parsed block. Name is "core/paragraph" style ("" for
// freeform HTML between blocks). Parts interleave the block's own HTML with
// its inner blocks, in document order.
type Block struct {
	Name  string
	Attrs map[string]any
	Void  bool
	Inner []*Block
	Parts []Part
}

// Part is either a chunk of HTML or an inner block.
type Part struct {
	HTML  string
	Child *Block
}

// HTML returns the block's own markup without its inner blocks.
func (b *Block) HTML() string {
	var sb strings.Builder
	for _, p := range b.Parts {
		if p.Child == nil {
			sb.WriteString(p.HTML)
		}
	}
	return sb.String()
}

// HasBlocks reports whether raw contains block delimiters.
func HasBlocks(raw string) bool { return strings.Contains(raw, "<!-- wp:") }

type token struct {
	start, end int // byte offsets of the whole comment
	name       string
	attrs      map[string]any
	closer     bool
	void       bool
}

// nextToken finds the next block delimiter at or after pos.
func nextToken(s string, pos int) (token, bool) {
	for {
		i := strings.Index(s[pos:], "<!--")
		if i < 0 {
			return token{}, false
		}
		start := pos + i
		j := strings.Index(s[start+4:], "-->")
		if j < 0 {
			return token{}, false
		}
		end := start + 4 + j + 3
		inner := s[start+4 : end-3]
		if tok, ok := parseDelimiter(inner); ok {
			tok.start, tok.end = start, end
			return tok, true
		}
		pos = end
	}
}

// parseDelimiter parses the inside of a comment: " wp:name {json} /" etc.
func parseDelimiter(inner string) (token, bool) {
	trimmed := strings.TrimLeft(inner, " \t\r\n")
	if len(trimmed) == len(inner) { // WP requires whitespace after <!--
		return token{}, false
	}
	var tok token
	if strings.HasPrefix(trimmed, "/wp:") {
		tok.closer = true
		trimmed = trimmed[4:]
	} else if strings.HasPrefix(trimmed, "wp:") {
		trimmed = trimmed[3:]
	} else {
		return token{}, false
	}
	n := strings.IndexAny(trimmed, " \t\r\n")
	if n < 0 {
		n = len(trimmed)
	}
	name := trimmed[:n]
	if strings.HasSuffix(name, "/") { // <!-- wp:separator/-->
		name, tok.void = strings.TrimSuffix(name, "/"), true
	}
	tok.name = normalizeName(name)
	if tok.name == "" {
		return token{}, false
	}
	rest := strings.TrimSpace(trimmed[n:])
	if strings.HasSuffix(rest, "/") {
		tok.void = true
		rest = strings.TrimSpace(strings.TrimSuffix(rest, "/"))
	}
	if strings.HasPrefix(rest, "{") {
		var attrs map[string]any
		if json.Unmarshal([]byte(rest), &attrs) == nil {
			tok.attrs = attrs
		}
	}
	return tok, true
}

func normalizeName(n string) string {
	for _, r := range n {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '/') {
			return ""
		}
	}
	if n == "" || strings.HasPrefix(n, "/") {
		return ""
	}
	if !strings.Contains(n, "/") {
		return "core/" + n
	}
	return n
}

// Parse builds the block tree. Mismatched closers are ignored and unclosed
// blocks are closed at the end of the input, like WordPress's parser.
func Parse(raw string) []*Block {
	var out []*Block
	var stack []*Block
	pos := 0
	addHTML := func(html string) {
		if html == "" {
			return
		}
		if len(stack) > 0 {
			top := stack[len(stack)-1]
			top.Parts = append(top.Parts, Part{HTML: html})
			return
		}
		if strings.TrimSpace(html) != "" {
			out = append(out, &Block{Parts: []Part{{HTML: html}}})
		}
	}
	addBlock := func(b *Block) {
		if len(stack) > 0 {
			top := stack[len(stack)-1]
			top.Inner = append(top.Inner, b)
			top.Parts = append(top.Parts, Part{Child: b})
			return
		}
		out = append(out, b)
	}
	for {
		tok, ok := nextToken(raw, pos)
		if !ok {
			break
		}
		addHTML(raw[pos:tok.start])
		pos = tok.end
		switch {
		case tok.closer:
			idx := -1
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i].Name == tok.name {
					idx = i
					break
				}
			}
			if idx < 0 {
				continue // stray closer
			}
			for len(stack) > idx {
				b := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				addBlock(b)
			}
		case tok.void:
			addBlock(&Block{Name: tok.name, Attrs: attrsOrEmpty(tok.attrs), Void: true})
		default:
			stack = append(stack, &Block{Name: tok.name, Attrs: attrsOrEmpty(tok.attrs)})
		}
	}
	addHTML(raw[pos:])
	for len(stack) > 0 {
		b := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		addBlock(b)
	}
	return out
}

func attrsOrEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
