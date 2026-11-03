package builder

import (
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

const (
	SectionLibrary       = "sections"
	maxNativeFacts       = 8
	maxComparisonColumns = 6
	maxNativeLabelBytes  = 300
	maxNativeTextBytes   = 12000
)

// NativeSearchOptions describes a local GET destination; rendering preserves a
// safe link fallback until the theme expands the sanitized search marker.
type NativeSearchOptions struct {
	Action      string `json:"action"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
	Param       string `json:"param,omitempty"`
}

// NativeOptions accepts typed content only, never arbitrary classes, CSS or HTML.
type NativeOptions struct {
	Search         *NativeSearchOptions `json:"search,omitempty"`
	EmptyText      string               `json:"empty_text,omitempty"`
	Eyebrow        string               `json:"eyebrow,omitempty"`
	Background     string               `json:"background,omitempty" enum:",inherit,none,dots,grid,lines,circles,waves,paper,solid"`
	SecondaryLink  string               `json:"secondary_link,omitempty"`
	SecondaryLabel string               `json:"secondary_label,omitempty"`
	Facts          []SectionFact        `json:"facts,omitempty"`
	Columns        []string             `json:"columns,omitempty"`
	Rows           []ComparisonRow      `json:"rows,omitempty"`
	Caption        string               `json:"caption,omitempty"`
	Illustration   bool                 `json:"illustration,omitempty"`
	// ListType is the content type a directory section lists and searches
	// (a type slug; a type that is not public renders nothing).
	ListType string `json:"list_type,omitempty"`
}
type SectionFact struct {
	Label string `json:"label"`
	Value string `json:"value"`
}
type ComparisonRow struct {
	Label string           `json:"label"`
	Cells []ComparisonCell `json:"cells"`
}
type ComparisonCell struct {
	Text   string `json:"text"`
	Status string `json:"status,omitempty" enum:",yes,no"`
}

var nativeKinds = []string{"hero", "features", "steps", "comparison", "faq", "product_preview", "media_text", "switcher", "directory", "task_entry"}
var nativeOnlyKinds = []string{"steps", "comparison", "product_preview", "media_text", "switcher", "directory", "task_entry"}

var listTypePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,39}$`)

// nativeEntryImageKinds are the native kinds whose entries may carry an image.
var nativeEntryImageKinds = []string{"features", "steps", "media_text", "switcher"}
var sectionIcons = []string{"", "bolt", "shield", "chart", "globe", "clock", "check", "layers"}
var sectionBackgrounds = []string{"", "inherit", "none", "dots", "grid", "lines", "circles", "waves", "paper", "solid"}

// Native layouts are a closed set per section kind; no raw classes are accepted.
func validNativeVariant(kind, variant string) bool {
	if variant == "" || variant == "default" {
		return true
	}
	switch kind {
	case "hero":
		return variant == "split" || variant == "centered"
	case "faq":
		return variant == "split"
	case "directory":
		return variant == "compact"
	case "task_entry":
		return variant == "search_first" || variant == "choice_first" || variant == "service_first"
	case "features":
		return variant == "compact_rows" || variant == "image_led" || variant == "featured_secondary"
	case "media_text":
		return variant == "image_start" || variant == "image_end"
	}
	return false
}

func validateNative(s Section) error {
	fail := func() error { return errors.New("builder: unsupported native section option") }
	if s.Library == "" {
		if s.Native != nil || slices.Contains(nativeOnlyKinds, s.Kind) {
			return fail()
		}
		for _, e := range s.Items {
			if e.Icon != "" || e.Featured {
				return fail()
			}
		}
		return nil
	}
	if s.Library != SectionLibrary || !slices.Contains(nativeKinds, s.Kind) || s.HeroScene != "" || s.Feed != "" {
		return fail()
	}
	if !validNativeVariant(s.Kind, s.Variant) {
		return fail()
	}
	if s.Link != "" && strings.TrimSpace(s.LinkLabel) == "" {
		return errors.New("builder: native button needs a label")
	}
	if s.Kind == "comparison" && strings.TrimSpace(s.Title) == "" {
		return errors.New("builder: comparison needs an accessible title")
	}
	if s.Kind != "hero" && s.Kind != "product_preview" && (s.Image != "" || s.ImageAlt != "") {
		return fail()
	}
	if (s.Kind == "hero" || s.Kind == "comparison" || s.Kind == "product_preview" || s.Kind == "directory") && len(s.Items) > 0 {
		return fail()
	}
	for _, e := range s.Items {
		if s.Kind == "faq" && strings.TrimSpace(e.Title) == "" {
			return errors.New("builder: native question needs a summary")
		}
		if s.Kind == "switcher" && strings.TrimSpace(e.Title) == "" {
			return errors.New("builder: a switcher option needs a label")
		}
		if e.Link != "" && strings.TrimSpace(e.Title) == "" {
			return errors.New("builder: native feature link needs a title")
		}
		if e.Price != "" || !slices.Contains(sectionIcons, e.Icon) {
			return fail()
		}
		// Entry images: feature cards, steps, text-and-media rows and switcher
		// panels. Task choices may link and carry icons; featuring belongs to results.
		if s.Kind != "features" && s.Kind != "task_entry" && (e.Link != "" || e.Icon != "" || e.Featured) {
			return fail()
		}
		if !slices.Contains(nativeEntryImageKinds, s.Kind) && (e.Image != "" || e.ImageAlt != "") {
			return fail()
		}
	}
	if s.Native == nil {
		if s.Kind == "comparison" || s.Kind == "directory" || s.Kind == "task_entry" {
			return fail()
		}
		return nil
	}
	n := s.Native
	if err := validateTaskAndResultOptions(s); err != nil {
		return err
	}
	if !slices.Contains(sectionBackgrounds, n.Background) {
		return fail()
	}
	for _, v := range []string{n.Eyebrow, n.SecondaryLabel, n.Caption} {
		if len(v) > maxNativeTextBytes {
			return errors.New("builder: native text too long")
		}
	}
	if err := safeURL(n.SecondaryLink, false); err != nil {
		return err
	}
	if n.SecondaryLink != "" && strings.TrimSpace(n.SecondaryLabel) == "" {
		return fail()
	}
	if s.Kind != "hero" && (n.Eyebrow != "" || n.SecondaryLink != "" || n.SecondaryLabel != "" || len(n.Facts) > 0) {
		return fail()
	}
	if s.Kind != "hero" && s.Kind != "product_preview" && (n.Caption != "" || n.Illustration) {
		return fail()
	}
	if n.Illustration && (s.Image != "" || strings.TrimSpace(n.Caption) == "") {
		return fail()
	}
	if len(n.Facts) > maxNativeFacts || len(n.Columns) > maxComparisonColumns || len(n.Rows) > maxRepeatedEntries {
		return errors.New("builder: too many native entries")
	}
	for _, f := range n.Facts {
		if strings.TrimSpace(f.Label) == "" || strings.TrimSpace(f.Value) == "" || len(f.Label) > maxNativeLabelBytes || len(f.Value) > maxNativeLabelBytes {
			return fail()
		}
	}
	if s.Kind != "comparison" && (len(n.Columns) > 0 || len(n.Rows) > 0) {
		return fail()
	}
	if (s.Kind == "directory") != (n.ListType != "") || (n.ListType != "" && !listTypePattern.MatchString(n.ListType)) {
		return errors.New("builder: a directory needs a content type to list")
	}
	if s.Kind == "comparison" {
		if len(n.Columns) < 2 {
			return errors.New("builder: comparison needs two to six columns")
		}
		for _, v := range n.Columns {
			if strings.TrimSpace(v) == "" || len(v) > maxNativeLabelBytes {
				return fail()
			}
		}
		for _, r := range n.Rows {
			if strings.TrimSpace(r.Label) == "" || len(r.Label) > maxNativeTextBytes || len(r.Cells) != len(n.Columns)-1 {
				return errors.New("builder: comparison row does not match columns")
			}
			for _, c := range r.Cells {
				if len(c.Text) > maxNativeTextBytes || !slices.Contains([]string{"", "yes", "no"}, c.Status) {
					return fail()
				}
			}
		}
	}
	return nil
}

func validateTaskAndResultOptions(s Section) error {
	n := s.Native
	fail := func() error { return errors.New("builder: unsupported task or result option") }
	if n.Search != nil {
		search := n.Search
		if s.Kind != "task_entry" || s.Variant != "search_first" || strings.TrimSpace(search.Label) == "" || len(search.Label) > maxNativeLabelBytes || len(search.Placeholder) > maxNativeLabelBytes || strings.ContainsFunc(search.Label+search.Placeholder, unicode.IsControl) {
			return fail()
		}
		if err := safeURL(search.Action, false); err != nil {
			return err
		}
		u, err := url.Parse(search.Action)
		if err != nil || !strings.HasPrefix(search.Action, "/") || u.Scheme != "" || u.Host != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(search.Action, "?#") || strings.HasPrefix(u.Path, "//") || strings.Contains(u.Path, "\\") || strings.ContainsFunc(u.Path, unicode.IsControl) {
			return fail()
		}
		if search.Param != "" && !listTypePattern.MatchString(search.Param) {
			return fail()
		}
	}
	if s.Kind == "task_entry" {
		if s.Variant == "search_first" && n.Search == nil {
			return fail()
		}
		if s.Variant != "search_first" && len(s.Items) == 0 {
			return errors.New("builder: task choices need a destination")
		}
		for _, entry := range s.Items {
			if strings.TrimSpace(entry.Title) == "" || entry.Link == "" || entry.Featured {
				return fail()
			}
		}
	}
	if len(n.EmptyText) > maxNativeTextBytes || (n.EmptyText != "" && (s.Kind != "features" || s.Variant == "" || s.Variant == "default")) {
		return fail()
	}
	return nil
}

func defaultNative(s *Section, locale string) {
	s.Library = SectionLibrary
	s.Native = &NativeOptions{}
	switch s.Kind {
	case "task_entry":
		s.Variant = "choice_first"
		s.Items = []Entry{{Title: "Explore", Text: "Choose a published destination", Link: "/"}}
		if locale == "ar" {
			s.Items = []Entry{{Title: "استكشف", Text: "اختر وجهة منشورة", Link: "/"}}
		}
	case "steps":
		for i := 0; i < 3; i++ {
			s.Items = append(s.Items, Entry{Title: []string{"Write", "Review", "Publish"}[i], Text: "Describe this step"})
		}
		if locale == "ar" {
			for i := range s.Items {
				s.Items[i] = Entry{Title: []string{"اكتب", "راجع", "انشر"}[i], Text: "اكتب وصف الخطوة"}
			}
		}
	case "comparison":
		s.Native.Columns = []string{"Capability", "Option A", "Option B"}
		s.Native.Rows = []ComparisonRow{{Label: "Feature", Cells: []ComparisonCell{{Text: "Included", Status: "yes"}, {Text: "Not included", Status: "no"}}}}
		if locale == "ar" {
			s.Native.Columns = []string{"الإمكانية", "الخيار أ", "الخيار ب"}
			s.Native.Rows = []ComparisonRow{{Label: "ميزة", Cells: []ComparisonCell{{Text: "متاحة", Status: "yes"}, {Text: "غير متاحة", Status: "no"}}}}
		}
	case "directory":
		s.Native.ListType = "post"
	case "product_preview":
		s.Native.Illustration = true
		s.Native.Caption = "Illustrative interface drawn in CSS. Replace it with your product screenshot."
		if locale == "ar" {
			s.Native.Caption = "واجهة توضيحية مرسومة بـ CSS. استبدلها بلقطة حقيقية لمنتجك."
		}
	}
}
