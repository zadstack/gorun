package workagents

import (
	"strings"

	"golang.org/x/net/html"
)

func reviewVisibleText(source string) string {
	tokenizer := html.NewTokenizer(strings.NewReader(source))
	var visible strings.Builder
	var hidden []string
	for {
		token := tokenizer.Next()
		if token == html.ErrorToken {
			return visible.String()
		}
		switch token {
		case html.StartTagToken:
			tag, _ := tokenizer.TagName()
			name := strings.ToLower(string(tag))
			sensitive := name == "script" || name == "style" || name == "template" || name == "form" || name == "textarea" || name == "select" || name == "button" || name == "iframe" || name == "noscript"
			for {
				key, val, more := tokenizer.TagAttr()
				if string(key) == "hidden" || (string(key) == "aria-hidden" && string(val) == "true") {
					sensitive = true
				}
				if !more {
					break
				}
			}
			void := name == "input" || name == "img" || name == "br" || name == "hr" || name == "meta" || name == "link" || name == "source" || name == "area" || name == "base" || name == "embed" || name == "param" || name == "track" || name == "wbr"
			if !void && (len(hidden) > 0 || sensitive) {
				hidden = append(hidden, name)
			}
			visible.WriteByte(' ')
		case html.EndTagToken:
			tag, _ := tokenizer.TagName()
			if len(hidden) > 0 && hidden[len(hidden)-1] == string(tag) {
				hidden = hidden[:len(hidden)-1]
			}
			visible.WriteByte(' ')
		case html.TextToken:
			if len(hidden) == 0 {
				visible.Write(tokenizer.Text())
			}
		}
	}
}
