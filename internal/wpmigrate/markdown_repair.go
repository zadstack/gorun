package wpmigrate

import (
	"regexp"
	"strings"
)

// Sites that rendered Markdown at display time (e.g. Jetpack Markdown)
// export HTML with raw Markdown left inside paragraphs. repairMarkdown
// converts what that leaves behind, outside <pre> blocks only:
//   - paragraph lines starting with "- " become a <ul> (text before and
//     after stays in its own <p>);
//   - "| a | b |" lines followed by a |---| separator become a <table>;
//   - `x` in text (not in tags or existing <code>) becomes <code>x</code>.
//
// Everything else, and every <pre> block, is kept byte for byte. It
// reports whether anything changed.
func repairMarkdown(body string) (string, bool) {
	var b strings.Builder
	last := 0
	for _, loc := range mdPre.FindAllStringIndex(body, -1) {
		b.WriteString(repairMarkdownText(body[last:loc[0]]))
		b.WriteString(body[loc[0]:loc[1]])
		last = loc[1]
	}
	b.WriteString(repairMarkdownText(body[last:]))
	out := b.String()
	return out, out != body
}

var (
	mdPre       = regexp.MustCompile(`(?is)<pre\b.*?</pre>`)
	mdPara      = regexp.MustCompile(`(?is)(<p(?:\s[^>]*)?>)(.*?)</p>`)
	mdSeparator = regexp.MustCompile(`^\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)*\|?$`)
	mdTrailBr   = regexp.MustCompile(`(?i)\s*<br\s*/?>\s*$`)
	mdTag       = regexp.MustCompile(`<[^>]*>`)
	mdTick      = regexp.MustCompile("`([^`\n<>]+)`")
	mdCodeOpen  = regexp.MustCompile(`(?i)^<code\b`)
	mdCodeClose = regexp.MustCompile(`(?i)^</code\s*>`)
)

func repairMarkdownText(s string) string {
	s = mdPara.ReplaceAllStringFunc(s, func(p string) string {
		m := mdPara.FindStringSubmatch(p)
		if out, ok := repairParagraph(m[1], m[2]); ok {
			return out
		}
		return p
	})
	return repairTicks(s)
}

// mdLine is a paragraph line without a trailing <br> (wpautop output).
func mdLine(l string) string { return mdTrailBr.ReplaceAllString(l, "") }

func isTableStart(lines []string, i int) bool {
	return strings.HasPrefix(mdLine(lines[i]), "|") && i+1 < len(lines) &&
		mdSeparator.MatchString(strings.TrimSpace(mdLine(lines[i+1])))
}

// repairParagraph splits one paragraph into text, lists and tables; ok is
// false (keep the original) when no list or table was found.
func repairParagraph(open, inner string) (string, bool) {
	lines := strings.Split(inner, "\n")
	var out, text []string
	found := false
	flush := func() {
		if t := strings.TrimSpace(mdLine(strings.Join(text, "\n"))); t != "" {
			out = append(out, open+t+"</p>")
		}
		text = nil
	}
	for i := 0; i < len(lines); {
		switch {
		case strings.HasPrefix(mdLine(lines[i]), "- "):
			flush()
			var items strings.Builder
			for ; i < len(lines) && strings.HasPrefix(mdLine(lines[i]), "- "); i++ {
				items.WriteString("<li>" + strings.TrimSpace(mdLine(lines[i])[2:]) + "</li>")
			}
			out, found = append(out, "<ul>"+items.String()+"</ul>"), true
		case isTableStart(lines, i):
			flush()
			var rows []string
			for ; i < len(lines) && strings.HasPrefix(mdLine(lines[i]), "|"); i++ {
				rows = append(rows, mdLine(lines[i]))
			}
			out, found = append(out, mdTable(rows)), true
		default:
			text = append(text, lines[i])
			i++
		}
	}
	if !found {
		return "", false
	}
	flush()
	return strings.Join(out, ""), true
}

func mdCells(line, tag string) string {
	var b strings.Builder
	for _, c := range strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|") {
		b.WriteString("<" + tag + ">" + strings.TrimSpace(c) + "</" + tag + ">")
	}
	return b.String()
}

// mdTable renders header, separator and body rows.
func mdTable(rows []string) string {
	var b strings.Builder
	b.WriteString("<table><thead><tr>" + mdCells(rows[0], "th") + "</tr></thead><tbody>")
	for _, r := range rows[2:] {
		b.WriteString("<tr>" + mdCells(r, "td") + "</tr>")
	}
	b.WriteString("</tbody></table>")
	return b.String()
}

// repairTicks converts `x` in text nodes, skipping tags and <code> content.
func repairTicks(s string) string {
	if !strings.Contains(s, "`") {
		return s
	}
	var b strings.Builder
	inCode, last := 0, 0
	text := func(t string) {
		if inCode > 0 {
			b.WriteString(t)
			return
		}
		b.WriteString(mdTick.ReplaceAllString(t, "<code>$1</code>"))
	}
	for _, loc := range mdTag.FindAllStringIndex(s, -1) {
		text(s[last:loc[0]])
		tag := s[loc[0]:loc[1]]
		switch {
		case mdCodeOpen.MatchString(tag):
			inCode++
		case mdCodeClose.MatchString(tag) && inCode > 0:
			inCode--
		}
		b.WriteString(tag)
		last = loc[1]
	}
	text(s[last:])
	return b.String()
}
