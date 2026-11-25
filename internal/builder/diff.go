package builder

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// A layout diff: what one document changes against another, as a short
// list a person can read before approving an agent's write (the plan step
// of plan_page_layout, the preview on a pending approval) or after it (the
// editor's layout history). Sections are matched by id, so a moved section
// is a move and not a removal plus an addition; entries are matched by
// position inside their section. Values are shown shortened.

// Change is one difference.
type Change struct {
	// Op is add, remove, move or change.
	Op string `json:"op"`
	// Section is the section id; Kind its kind.
	Section string `json:"section"`
	Kind    string `json:"kind"`
	// Path names the changed value inside the section ("" for the section
	// itself): title, items[2].text, native.eyebrow, form.fields, hidden…
	Path string `json:"path,omitempty"`
	// From and To are the shortened old and new values (a move carries the
	// positions, counted from 1).
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// LayoutDiff is the whole comparison.
type LayoutDiff struct {
	Changes []Change `json:"changes"`
	// Summary counts the changes: "2 sections added, 3 values changed".
	Summary string `json:"summary"`
}

// Empty reports whether nothing changed.
func (d LayoutDiff) Empty() bool { return len(d.Changes) == 0 }

// textPaths are the values a text-only editor (content.edit_text) may
// change: the texts and images of a section and of its entries, and the
// native texts. Everything else (sections, order, kind, layout, links,
// visibility, structure, forms) is layout.
var textPaths = map[string]bool{"title": true, "text": true, "link_label": true, "image": true, "image_alt": true, "price": true,
	"native.eyebrow": true, "native.caption": true, "native.secondary_label": true, "native.empty_text": true}

// TextOnly reports whether every change edits a text or image in place.
func (d LayoutDiff) TextOnly() bool {
	for _, c := range d.Changes {
		if c.Op != "change" {
			return false
		}
		path := c.Path
		if strings.HasPrefix(path, "items[") {
			if i := strings.Index(path, "]."); i >= 0 {
				path = path[i+2:]
			}
		}
		if !textPaths[path] {
			return false
		}
	}
	return true
}

const diffValueMax = 80

// Diff compares before with after.
func Diff(before, after Document) LayoutDiff {
	var out LayoutDiff
	index := func(doc Document) map[string]int {
		m := make(map[string]int, len(doc.Sections))
		for i, s := range doc.Sections {
			m[s.ID] = i
		}
		return m
	}
	bi, ai := index(before), index(after)
	for _, s := range before.Sections {
		if _, ok := ai[s.ID]; !ok {
			out.Changes = append(out.Changes, Change{Op: "remove", Section: s.ID, Kind: s.Kind, From: short(s.Title)})
		}
	}
	for j, s := range after.Sections {
		i, ok := bi[s.ID]
		if !ok {
			out.Changes = append(out.Changes, Change{Op: "add", Section: s.ID, Kind: s.Kind, To: short(s.Title)})
			continue
		}
		// Position among the sections both documents have.
		if rank(before, ai, i) != rank(after, bi, j) {
			out.Changes = append(out.Changes, Change{Op: "move", Section: s.ID, Kind: s.Kind, From: strconv.Itoa(i + 1), To: strconv.Itoa(j + 1)})
		}
		out.Changes = append(out.Changes, sectionChanges(before.Sections[i], s)...)
	}
	out.Summary = summarize(out.Changes)
	return out
}

// rank is the position of section i counting only the sections the other
// document also has (other maps its ids), so additions and removals
// elsewhere do not read as moves.
func rank(doc Document, other map[string]int, i int) int {
	n := 0
	for k := 0; k < i; k++ {
		if _, ok := other[doc.Sections[k].ID]; ok {
			n++
		}
	}
	return n
}

func sectionChanges(a, b Section) []Change {
	var out []Change
	add := func(path, from, to string) {
		if from != to {
			out = append(out, Change{Op: "change", Section: b.ID, Kind: b.Kind, Path: path, From: short(from), To: short(to)})
		}
	}
	add("kind", a.Kind, b.Kind)
	add("library", a.Library, b.Library)
	add("variant", orDefault(a.Variant), orDefault(b.Variant))
	add("hero_scene", a.HeroScene, b.HeroScene)
	add("feed", a.Feed, b.Feed)
	add("title", a.Title, b.Title)
	add("text", a.Text, b.Text)
	add("image", a.Image, b.Image)
	add("image_alt", a.ImageAlt, b.ImageAlt)
	add("link", a.Link, b.Link)
	add("link_label", a.LinkLabel, b.LinkLabel)
	add("hidden", strconv.FormatBool(a.Hidden), strconv.FormatBool(b.Hidden))
	n := max(len(a.Items), len(b.Items))
	for i := 0; i < n; i++ {
		p := "items[" + strconv.Itoa(i+1) + "]"
		switch {
		case i >= len(a.Items):
			out = append(out, Change{Op: "add", Section: b.ID, Kind: b.Kind, Path: p, To: short(b.Items[i].Title)})
		case i >= len(b.Items):
			out = append(out, Change{Op: "remove", Section: b.ID, Kind: b.Kind, Path: p, From: short(a.Items[i].Title)})
		default:
			x, y := a.Items[i], b.Items[i]
			add(p+".title", x.Title, y.Title)
			add(p+".text", x.Text, y.Text)
			add(p+".image", x.Image, y.Image)
			add(p+".image_alt", x.ImageAlt, y.ImageAlt)
			add(p+".link", x.Link, y.Link)
			add(p+".price", x.Price, y.Price)
			add(p+".icon", x.Icon, y.Icon)
			add(p+".featured", strconv.FormatBool(x.Featured), strconv.FormatBool(y.Featured))
		}
	}
	out = append(out, objectChanges(b, "native", a.Native, b.Native)...)
	out = append(out, objectChanges(b, "form", a.Form, b.Form)...)
	return out
}

// objectChanges compares two optional option objects field by field at the
// top level (nested values are compared as JSON).
func objectChanges(s Section, name string, a, b any) []Change {
	flat := func(v any) map[string]string {
		raw, err := json.Marshal(v)
		if err != nil || string(raw) == "null" {
			return nil
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			return nil
		}
		out := make(map[string]string, len(fields))
		for k, v := range fields {
			var str string
			if json.Unmarshal(v, &str) == nil {
				out[k] = str
			} else {
				out[k] = string(v)
			}
		}
		return out
	}
	fa, fb := flat(a), flat(b)
	var out []Change
	keys := map[string]bool{}
	for k := range fa {
		keys[k] = true
	}
	for k := range fb {
		keys[k] = true
	}
	for _, k := range sortedKeys(keys) {
		if fa[k] != fb[k] {
			out = append(out, Change{Op: "change", Section: s.ID, Kind: s.Kind, Path: name + "." + k, From: short(fa[k]), To: short(fb[k])})
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func orDefault(v string) string {
	if v == "" {
		return "default"
	}
	return v
}

// short shortens a value for display: one line, at most diffValueMax runes.
func short(v string) string {
	v = strings.Join(strings.Fields(v), " ")
	if r := []rune(v); len(r) > diffValueMax {
		return string(r[:diffValueMax-1]) + "…"
	}
	return v
}

func summarize(changes []Change) string {
	var added, removed, moved, changed int
	for _, c := range changes {
		switch c.Op {
		case "add":
			added++
		case "remove":
			removed++
		case "move":
			moved++
		default:
			changed++
		}
	}
	var parts []string
	for _, p := range []struct {
		n    int
		word string
	}{{added, "added"}, {removed, "removed"}, {moved, "moved"}, {changed, "changed"}} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.word))
		}
	}
	if len(parts) == 0 {
		return "no changes"
	}
	return strings.Join(parts, ", ")
}

// Text renders the diff as lines: "+ hero <id> Title", "- …", "↕ …", and
// "~ hero <id> title: old → new".
func (d LayoutDiff) Text() string {
	if d.Empty() {
		return d.Summary
	}
	var b strings.Builder
	b.WriteString(d.Summary)
	for _, c := range d.Changes {
		b.WriteString("\n")
		switch c.Op {
		case "add":
			b.WriteString("+ ")
		case "remove":
			b.WriteString("- ")
		case "move":
			b.WriteString("↕ ")
		default:
			b.WriteString("~ ")
		}
		b.WriteString(c.Kind + " " + c.Section)
		if c.Path != "" {
			b.WriteString(" " + c.Path)
		}
		switch c.Op {
		case "add":
			if c.To != "" {
				b.WriteString(": " + c.To)
			}
		case "remove":
			if c.From != "" {
				b.WriteString(": " + c.From)
			}
		default:
			b.WriteString(": " + c.From + " → " + c.To)
		}
	}
	return b.String()
}
