package admin

import (
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Run context limits, in characters (runes).
const (
	canvasContextLimit = 64000 // the whole context: headers, text, markers and separators
	canvasItemBudget   = 8000  // the most text one source keeps inline
	canvasItemMinimum  = 500   // the least text a shortened source keeps inline
	canvasSourceMax    = 200000
	canvasSeparator    = "\n\n"
)

// canvasSpillMarker replaces the part of a source that did not fit. It is read
// by models, so it is always English whatever the admin locale is.
const canvasSpillMarker = "[… %s more characters. Full text: ref:%s]"

// canvasSourceItem is one source in a run context. Ref is "content/<id>" or
// "note/<card id>"; Body is the full sanitized text.
type canvasSourceItem struct {
	Ref, Title, Body string
}

// canvasSpill records a source that was shortened: Kept of its Total characters
// are inline, the rest is available through get_canvas_source.
type canvasSpill struct {
	Ref   string `json:"ref"`
	Kept  int    `json:"kept"`
	Total int    `json:"total"`
}

func canvasHeader(it canvasSourceItem) string {
	title := strings.Join(strings.Fields(it.Title), " ")
	return "## [ref:" + it.Ref + "] " + title
}

func canvasMarker(remaining int, ref string) string {
	return strings.Replace(strings.Replace(canvasSpillMarker, "%s", canvasGroup(remaining), 1), "%s", ref, 1)
}

// canvasGroup writes n with Latin digits and comma thousands separators.
func canvasGroup(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// canvasInline joins distinct sources in their given order, each starting with its
// reference header, into a context of at most canvasContextLimit characters.
//
// Budget (fair share, "water filling"): every source first receives its
// minimum, which is its whole text when that is shorter than
// canvasItemMinimum plus a marker, otherwise canvasItemMinimum characters plus
// the marker. If the headers, separators and minimums alone exceed the limit,
// the context is invalid. What remains of the limit is then shared out: each
// source wants its whole text, capped at canvasItemBudget characters (plus the
// marker when capped). Sources are visited from the smallest want above their
// minimum to the largest; each receives the smaller of its want and an equal share of what is
// still unallocated, so small sources stay whole and the large ones split the
// rest evenly. A source whose allocation is below its full length keeps
// allocation minus marker characters, cut on a line or paragraph boundary
// near the limit when one is close, and the marker gives the exact number of
// characters left out.
func canvasInline(items []canvasSourceItem) (string, []canvasSpill, error) {
	// A source reached twice (a content card and a source query) is inlined
	// once, at its first occurrence.
	seen := map[string]bool{}
	unique := []canvasSourceItem{}
	for _, it := range items {
		if !seen[it.Ref] {
			seen[it.Ref] = true
			unique = append(unique, it)
		}
	}
	items = unique
	n := len(items)
	if n == 0 {
		return "", nil, nil
	}
	total := make([]int, n)
	reserve := make([]int, n)
	minimum := make([]int, n)
	want := make([]int, n)
	pool := canvasContextLimit - len(canvasSeparator)*(n-1)
	for i, it := range items {
		total[i] = utf8.RuneCountInString(it.Body)
		// Newline plus the longest marker this source could need.
		reserve[i] = 1 + utf8.RuneCountInString(canvasMarker(total[i], it.Ref))
		pool -= utf8.RuneCountInString(canvasHeader(it)) + 1
		minimum[i] = min(total[i], canvasItemMinimum+reserve[i])
		want[i] = total[i]
		if total[i] > canvasItemBudget {
			want[i] = canvasItemBudget + reserve[i]
		}
		pool -= minimum[i]
	}
	if pool < 0 {
		return "", nil, errCanvasInvalid
	}
	alloc := slices.Clone(minimum)
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return want[order[a]]-minimum[order[a]] < want[order[b]]-minimum[order[b]] })
	for k, i := range order {
		extra := min(want[i]-minimum[i], pool/(n-k))
		alloc[i] += extra
		pool -= extra
	}
	parts := make([]string, n)
	spilled := []canvasSpill{}
	for i, it := range items {
		body := it.Body
		if alloc[i] < total[i] {
			kept := canvasCut(body, alloc[i]-reserve[i])
			keptRunes := utf8.RuneCountInString(kept)
			spilled = append(spilled, canvasSpill{Ref: it.Ref, Kept: keptRunes, Total: total[i]})
			body = kept + "\n" + canvasMarker(total[i]-keptRunes, it.Ref)
		}
		parts[i] = canvasHeader(it) + "\n" + body
	}
	if len(spilled) == 0 {
		spilled = nil
	}
	return strings.Join(parts, canvasSeparator), spilled, nil
}

// canvasCut returns at most limit runes of s, ending on a paragraph or line
// break in the last fifth of the limit when there is one, but never keeping
// fewer than canvasItemMinimum runes because of that preference.
func canvasCut(s string, limit int) string {
	floorRunes := max(limit-limit/5, min(canvasItemMinimum, limit))
	end, floor, count := len(s), 0, 0
	for i := range s {
		if count == floorRunes {
			floor = i
		}
		if count == limit {
			end = i
			break
		}
		count++
	}
	head := s[:end]
	for _, sep := range []string{"\n\n", "\n"} {
		if at := strings.LastIndex(head, sep); at >= floor && at > 0 {
			return strings.TrimRight(head[:at], " \t\r\n")
		}
	}
	return head
}

// canvasSpillSummary is the review dialog's shortening line: how many sources
// were shortened, how many characters were left out, and whether the
// destination can fetch them (only an external MCP agent can call
// get_canvas_source; providers and managed agents receive the shortened text).
func canvasSpillSummary(p canvasPrepared) map[string]any {
	chars := 0
	for _, s := range p.Spilled {
		chars += s.Total - s.Kept
	}
	return map[string]any{"sources": len(p.Spilled), "characters": chars, "on_request": p.KeyID != "" && !p.managedAgent()}
}
