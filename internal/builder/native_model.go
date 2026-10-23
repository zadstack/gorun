package builder

import (
	"errors"
	"slices"
	"strings"
)

const (
	SectionLibrary       = "sections"
	maxNativeFacts       = 8
	maxComparisonColumns = 6
	maxNativeLabelBytes  = 300
	maxNativeTextBytes   = 12000
)

// NativeOptions describes only editable content/options supported by the
// existing gp-s section stylesheet. It accepts no arbitrary class, CSS or HTML.
type NativeOptions struct {
	Eyebrow        string          `json:"eyebrow,omitempty"`
	Background     string          `json:"background,omitempty" enum:",inherit,none,dots,grid,lines,circles,waves,paper,solid"`
	SecondaryLink  string          `json:"secondary_link,omitempty"`
	SecondaryLabel string          `json:"secondary_label,omitempty"`
	Facts          []SectionFact   `json:"facts,omitempty"`
	Columns        []string        `json:"columns,omitempty"`
	Rows           []ComparisonRow `json:"rows,omitempty"`
	Caption        string          `json:"caption,omitempty"`
	Illustration   bool            `json:"illustration,omitempty"`
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

var nativeKinds = []string{"hero", "features", "steps", "comparison", "faq", "product_preview"}
var nativeOnlyKinds = []string{"steps", "comparison", "product_preview"}
var sectionIcons = []string{"", "bolt", "shield", "chart", "globe", "clock", "check", "layers"}
var sectionBackgrounds = []string{"", "inherit", "none", "dots", "grid", "lines", "circles", "waves", "paper", "solid"}

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
	if s.Kind != "hero" && s.Kind != "faq" && s.Variant != "" && s.Variant != "default" {
		return fail()
	}
	if s.Link != "" && strings.TrimSpace(s.LinkLabel) == "" {
		return errors.New("builder: native button needs a label")
	}
	if s.Kind == "comparison" && strings.TrimSpace(s.Title) == "" {
		return errors.New("builder: comparison needs an accessible title")
	}
	if s.Kind == "faq" && s.Variant == "centered" {
		return fail()
	}
	if s.Kind != "hero" && s.Kind != "product_preview" && (s.Image != "" || s.ImageAlt != "") {
		return fail()
	}
	if (s.Kind == "hero" || s.Kind == "comparison" || s.Kind == "product_preview") && len(s.Items) > 0 {
		return fail()
	}
	for _, e := range s.Items {
		if s.Kind == "faq" && strings.TrimSpace(e.Title) == "" {
			return errors.New("builder: native question needs a summary")
		}
		if e.Link != "" && strings.TrimSpace(e.Title) == "" {
			return errors.New("builder: native feature link needs a title")
		}
		if e.Price != "" || !slices.Contains(sectionIcons, e.Icon) {
			return fail()
		}
		if s.Kind != "features" && (e.Image != "" || e.ImageAlt != "" || e.Link != "" || e.Icon != "" || e.Featured) {
			return fail()
		}
	}
	if s.Native == nil {
		if s.Kind == "comparison" {
			return fail()
		}
		return nil
	}
	n := s.Native
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

func defaultNative(s *Section, locale string) {
	s.Library = SectionLibrary
	s.Native = &NativeOptions{}
	switch s.Kind {
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
	case "product_preview":
		s.Native.Illustration = true
		s.Native.Caption = "Illustrative interface drawn in CSS. Replace it with your product screenshot."
		if locale == "ar" {
			s.Native.Caption = "واجهة توضيحية مرسومة بـ CSS. استبدلها بلقطة حقيقية لمنتجك."
		}
	}
}
