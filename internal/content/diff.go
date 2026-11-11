package content

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// A small line/word diff for comparing revisions. Body HTML is reduced to
// text with one line per block element, lines are compared with a longest
// common subsequence, and a removed line followed by an added one is shown as
// one changed line with a word-level diff.

// DiffOp is the kind of a diff span or line.
type DiffOp string

const (
	DiffEqual  DiffOp = "equal"
	DiffInsert DiffOp = "insert"
	DiffDelete DiffOp = "delete"
	DiffChange DiffOp = "change" // lines only: word-level spans inside
)

// DiffSpan is a run of text with one operation.
type DiffSpan struct {
	Op   DiffOp
	Text string
}

// DiffLine is one line of a body diff.
type DiffLine struct {
	Op    DiffOp
	Spans []DiffSpan
}

// maxDiffCells bounds the LCS table (after trimming the common prefix and
// suffix); larger inputs fall back to "all removed, all added".
const maxDiffCells = 1 << 20

// diffSeq returns the edit script turning a into b.
func diffSeq(a, b []string) []DiffSpan {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	out := make([]DiffSpan, 0, len(a)+len(b))
	for _, s := range a[:pre] {
		out = append(out, DiffSpan{DiffEqual, s})
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]
	n, m := len(ma), len(mb)
	if n*m > maxDiffCells {
		for _, s := range ma {
			out = append(out, DiffSpan{DiffDelete, s})
		}
		for _, s := range mb {
			out = append(out, DiffSpan{DiffInsert, s})
		}
	} else {
		// lcs[i][j]: LCS length of ma[i:] and mb[j:].
		w := m + 1
		lcs := make([]int32, (n+1)*w)
		for i := n - 1; i >= 0; i-- {
			for j := m - 1; j >= 0; j-- {
				if ma[i] == mb[j] {
					lcs[i*w+j] = lcs[(i+1)*w+j+1] + 1
				} else {
					lcs[i*w+j] = max(lcs[(i+1)*w+j], lcs[i*w+j+1])
				}
			}
		}
		i, j := 0, 0
		for i < n && j < m {
			switch {
			case ma[i] == mb[j]:
				out = append(out, DiffSpan{DiffEqual, ma[i]})
				i++
				j++
			case lcs[(i+1)*w+j] >= lcs[i*w+j+1]:
				out = append(out, DiffSpan{DiffDelete, ma[i]})
				i++
			default:
				out = append(out, DiffSpan{DiffInsert, mb[j]})
				j++
			}
		}
		for ; i < n; i++ {
			out = append(out, DiffSpan{DiffDelete, ma[i]})
		}
		for ; j < m; j++ {
			out = append(out, DiffSpan{DiffInsert, mb[j]})
		}
	}
	for _, s := range a[len(a)-suf:] {
		out = append(out, DiffSpan{DiffEqual, s})
	}
	return out
}

var wordRe = regexp.MustCompile(`\s+|[^\s]+`)

// DiffWords is a word-level diff of a and b, with adjacent spans of the same
// operation merged. Whitespace is kept, so the spans join back to the text.
func DiffWords(a, b string) []DiffSpan {
	ops := diffSeq(wordRe.FindAllString(a, -1), wordRe.FindAllString(b, -1))
	var out []DiffSpan
	for _, s := range ops {
		if n := len(out); n > 0 && out[n-1].Op == s.Op {
			out[n-1].Text += s.Text
			continue
		}
		out = append(out, s)
	}
	return out
}

// DiffLines compares two texts line by line. Runs of removed lines followed
// by added lines are paired into changed lines with a word diff.
func DiffLines(a, b []string) []DiffLine {
	ops := diffSeq(a, b)
	var out []DiffLine
	for i := 0; i < len(ops); {
		if ops[i].Op == DiffEqual {
			out = append(out, DiffLine{Op: DiffEqual, Spans: []DiffSpan{ops[i]}})
			i++
			continue
		}
		var dels, ins []string
		for ; i < len(ops) && ops[i].Op == DiffDelete; i++ {
			dels = append(dels, ops[i].Text)
		}
		for ; i < len(ops) && ops[i].Op == DiffInsert; i++ {
			ins = append(ins, ops[i].Text)
		}
		k := 0
		for ; k < len(dels) && k < len(ins); k++ {
			out = append(out, DiffLine{Op: DiffChange, Spans: DiffWords(dels[k], ins[k])})
		}
		for _, d := range dels[k:] {
			out = append(out, DiffLine{Op: DiffDelete, Spans: []DiffSpan{{DiffDelete, d}}})
		}
		for _, n := range ins[k:] {
			out = append(out, DiffLine{Op: DiffInsert, Spans: []DiffSpan{{DiffInsert, n}}})
		}
	}
	return out
}

// HTMLTextLines reduces (sanitized) HTML to text with one line per block
// element; images become "[image: alt]". Script and style content is dropped,
// whitespace inside a line is collapsed except in <pre>.
func HTMLTextLines(src string) []string {
	var lines []string
	var cur strings.Builder
	pre, skip := 0, 0
	flush := func() {
		line := cur.String()
		if pre == 0 {
			line = strings.Join(strings.Fields(line), " ")
		}
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
		cur.Reset()
	}
	z := html.NewTokenizer(strings.NewReader(src))
	for {
		switch z.Next() {
		case html.ErrorToken:
			flush()
			return lines
		case html.TextToken:
			if skip == 0 {
				cur.Write(z.Text())
			}
		case html.StartTagToken, html.SelfClosingTagToken, html.EndTagToken:
			tt := z.Token()
			end := tt.Type == html.EndTagToken
			switch tt.DataAtom {
			case atom.Script, atom.Style, atom.Template:
				if tt.Type == html.StartTagToken {
					skip++
				} else if end && skip > 0 {
					skip--
				}
				continue
			case atom.Pre:
				flush()
				if end {
					pre = max(0, pre-1)
				} else {
					pre++
				}
				continue
			case atom.Br:
				flush()
				continue
			case atom.Img:
				if skip == 0 {
					label := ""
					for _, a := range tt.Attr {
						if a.Key == "alt" {
							label = strings.TrimSpace(a.Val)
						}
					}
					if label == "" {
						for _, a := range tt.Attr {
							if a.Key == "src" {
								label = a.Val
							}
						}
					}
					cur.WriteString(" [image: " + label + "] ")
				}
				continue
			case atom.Td, atom.Th:
				cur.WriteString(" ")
				continue
			}
			if blockAtoms[tt.DataAtom] {
				flush()
			}
		}
	}
}

var blockAtoms = map[atom.Atom]bool{
	atom.P: true, atom.Div: true, atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true, atom.H6: true,
	atom.Li: true, atom.Ul: true, atom.Ol: true, atom.Blockquote: true, atom.Table: true, atom.Tr: true, atom.Figure: true,
	atom.Figcaption: true, atom.Section: true, atom.Article: true, atom.Header: true, atom.Footer: true, atom.Aside: true,
	atom.Hr: true, atom.Dl: true, atom.Dt: true, atom.Dd: true, atom.Details: true, atom.Summary: true, atom.Nav: true,
}
