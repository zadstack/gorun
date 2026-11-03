package theme

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var taskSearchParameter = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,39}$`)

type taskSearchFields struct {
	action, parameter, label, placeholder string
}

func taskSearchText(node *html.Node) string {
	var text strings.Builder
	var visit func(*html.Node)
	visit = func(child *html.Node) {
		if child.Type == html.TextNode {
			text.WriteString(child.Data)
		}
		for nested := child.FirstChild; nested != nil; nested = nested.NextSibling {
			visit(nested)
		}
	}
	visit(node)
	return strings.TrimSpace(text.String())
}

func taskSearchAttribute(node *html.Node, key string) string {
	for _, attribute := range node.Attr {
		if attribute.Key == key {
			return attribute.Val
		}
	}
	return ""
}

func taskSearchDestination(action string) bool {
	destination, err := url.Parse(action)
	return err == nil && strings.HasPrefix(action, "/") && !destination.IsAbs() && destination.Host == "" && destination.RawQuery == "" && !destination.ForceQuery && destination.Fragment == "" && !strings.Contains(action, "#") && !strings.HasPrefix(destination.Path, "//") && !strings.ContainsAny(destination.Path, "\\\r\n\t")
}

// Stored HTML is independently revalidated: imported content may contain a
// marker without having passed the builder's typed document validator.
func taskSearchFieldsFor(node *html.Node) (taskSearchFields, bool) {
	fields := taskSearchFields{}
	for _, class := range strings.Fields(taskSearchAttribute(node, "class")) {
		if strings.HasPrefix(class, "gp-task-param-") {
			if fields.parameter != "" {
				return fields, false
			}
			fields.parameter = strings.TrimPrefix(class, "gp-task-param-")
		}
	}
	if !taskSearchParameter.MatchString(fields.parameter) {
		return fields, false
	}
	links, placeholders := 0, 0
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != html.ElementNode {
			continue
		}
		if child.Data == "a" && hasClass(child, "gp-task-search-target") {
			links++
			fields.action = taskSearchAttribute(child, "href")
			fields.label = taskSearchText(child)
		}
		if child.Data == "span" && hasClass(child, "gp-task-search-placeholder") {
			placeholders++
			fields.placeholder = taskSearchText(child)
		}
	}
	valid := links == 1 && placeholders <= 1 && fields.label != "" && len(fields.label) <= 12000 && len(fields.placeholder) <= 12000 && taskSearchDestination(fields.action)
	return fields, valid
}

func taskSearchElement(tag string, attributes ...html.Attribute) *html.Node {
	return &html.Node{Type: html.ElementNode, Data: tag, DataAtom: atom.Lookup([]byte(tag)), Attr: attributes}
}

func taskSearchForm(fields taskSearchFields, id, query, submit string) *html.Node {
	form := taskSearchElement("form", html.Attribute{Key: "class", Val: "gp-task-search-form"}, html.Attribute{Key: "method", Val: "get"}, html.Attribute{Key: "action", Val: fields.action}, html.Attribute{Key: "role", Val: "search"})
	label := taskSearchElement("label", html.Attribute{Key: "for", Val: id})
	label.AppendChild(&html.Node{Type: html.TextNode, Data: fields.label})
	input := taskSearchElement("input", html.Attribute{Key: "type", Val: "search"}, html.Attribute{Key: "id", Val: id}, html.Attribute{Key: "name", Val: fields.parameter}, html.Attribute{Key: "value", Val: query}, html.Attribute{Key: "placeholder", Val: fields.placeholder}, html.Attribute{Key: "maxlength", Val: "100"})
	button := taskSearchElement("button", html.Attribute{Key: "type", Val: "submit"})
	button.AppendChild(&html.Node{Type: html.TextNode, Data: submit})
	label.AppendChild(input)
	form.AppendChild(label)
	form.AppendChild(button)
	return form
}

func (f *funcs) expandTaskSearch(node *html.Node, index int) {
	fields, valid := taskSearchFieldsFor(node)
	if !valid {
		return
	}
	fields.action = f.localized(fields.action)
	query := ""
	if request := f.st.tc.Request; request != nil && request.URL != nil {
		query = request.URL.Query().Get(fields.parameter)
	}
	if runes := []rune(query); len(runes) > 100 {
		query = string(runes[:100])
	}
	submit := "Search"
	if f.st.tc.Locale == "ar" {
		submit = "بحث"
	}
	form := taskSearchForm(fields, fmt.Sprintf("gp-task-%d-q", index), query, submit)
	for node.FirstChild != nil {
		node.RemoveChild(node.FirstChild)
	}
	node.AppendChild(form)
}
