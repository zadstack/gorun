package builder

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/zadstack/gorun/internal/db"
)

const (
	maxSections        = 60
	maxRepeatedEntries = 24
)

type Document struct {
	Version  int       `json:"version"`
	Sections []Section `json:"sections"`
}
type Section struct {
	Library   string         `json:"library,omitempty" enum:",sections" doc:"Empty keeps legacy builder HTML; sections opts into the theme section library."`
	Native    *NativeOptions `json:"native,omitempty"`
	HeroScene string         `json:"hero_scene,omitempty"`
	Feed      string         `json:"feed,omitempty"`
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Variant   string         `json:"variant"`
	Title     string         `json:"title"`
	Text      string         `json:"text"`
	Image     string         `json:"image"`
	ImageAlt  string         `json:"image_alt"`
	Link      string         `json:"link"`
	LinkLabel string         `json:"link_label"`
	Items     []Entry        `json:"items"`
	Hidden    bool           `json:"hidden"`
}
type Entry struct {
	Icon     string `json:"icon,omitempty" enum:",bolt,shield,chart,globe,clock,check,layers"`
	Featured bool   `json:"featured,omitempty"`
	Title    string `json:"title"`
	Text     string `json:"text"`
	Image    string `json:"image"`
	ImageAlt string `json:"image_alt"`
	Link     string `json:"link"`
	Price    string `json:"price"`
}
type BlockSpec struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

var kinds = []string{"hero", "text", "features", "services", "projects", "testimonials", "pricing", "faq", "stats", "logos", "contact", "cta", "steps", "comparison", "product_preview", "media_text", "switcher"}
var enLabels = []string{"Hero", "Text", "Features", "Services", "Projects", "Testimonials", "Pricing", "Questions", "Statistics", "Client logos", "Contact", "Call to action", "Steps", "Comparison", "Product preview", "Text and media rows", "Feature switcher"}
var arLabels = []string{"واجهة رئيسية", "نص", "مميزات", "خدمات", "أعمال", "آراء العملاء", "أسعار", "أسئلة شائعة", "إحصائيات", "شعارات العملاء", "تواصل", "دعوة لاتخاذ إجراء", "خطوات", "مقارنة", "معاينة المنتج", "صفوف نص ووسائط", "مبدّل المزايا"}

func Catalog(locale string) []BlockSpec {
	labels := enLabels
	if locale == "ar" {
		labels = arLabels
	}
	out := make([]BlockSpec, len(kinds))
	for i, k := range kinds {
		out[i] = BlockSpec{k, labels[i]}
	}
	return out
}
func DefaultSection(kind, locale string) Section {
	s := Section{ID: db.NewID(), Kind: kind, Variant: "default", Items: []Entry{}}
	for _, spec := range Catalog(locale) {
		if spec.Kind == kind {
			s.Title = spec.Label
		}
	}
	if locale == "ar" {
		s.Text = "اكتب محتوى القسم هنا"
		s.LinkLabel = "اعرف المزيد"
	} else {
		s.Text = "Add your section content here"
		s.LinkLabel = "Learn more"
	}
	switch kind {
	case "features", "services", "projects", "testimonials", "pricing", "faq", "stats", "logos":
		for i := 0; i < 3; i++ {
			label := "Item"
			text := "Add a short description"
			if locale == "ar" {
				label = "عنصر"
				text = "أضف وصفًا مختصرًا"
			}
			s.Items = append(s.Items, Entry{Title: fmt.Sprintf("%s %d", label, i+1), Text: text})
		}
	}
	if kind == "steps" || kind == "comparison" || kind == "product_preview" || kind == "media_text" || kind == "switcher" {
		defaultNative(&s, locale)
	}
	return s
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

func Validate(doc Document) error {
	if doc.Version != 1 {
		return errors.New("builder: unsupported document version")
	}
	if len(doc.Sections) > maxSections {
		return errors.New("builder: too many sections")
	}
	seen := map[string]bool{}
	for _, s := range doc.Sections {
		if !idPattern.MatchString(s.ID) || seen[s.ID] {
			return errors.New("builder: invalid or repeated section ID")
		}
		seen[s.ID] = true
		found := false
		for _, k := range kinds {
			if s.Kind == k {
				found = true
			}
		}
		if !found {
			return errors.New("builder: unknown section kind")
		}
		if s.Variant != "" && s.Variant != "default" && s.Variant != "split" && s.Variant != "centered" {
			return errors.New("builder: unknown layout")
		}
		if s.HeroScene != "" && (s.Kind != "hero" || (s.HeroScene != "orbit" && s.HeroScene != "ribbons" && s.HeroScene != "blocks")) {
			return errors.New("builder: unsupported hero scene")
		}
		if s.Feed != "" && (s.Feed != "posts" || (s.Kind != "text" && s.Kind != "projects")) {
			return errors.New("builder: unsupported feed")
		}
		if err := validateNative(s); err != nil {
			return err
		}
		if len(s.Items) > maxRepeatedEntries {
			return errors.New("builder: too many section entries")
		}
		for _, v := range []string{s.Title, s.Text, s.ImageAlt, s.LinkLabel} {
			if len(v) > 12000 {
				return errors.New("builder: text too long")
			}
		}
		if err := safeURL(s.Link, false); err != nil {
			return err
		}
		if err := safeURL(s.Image, true); err != nil {
			return err
		}
		for _, e := range s.Items {
			for _, v := range []string{e.Title, e.Text, e.ImageAlt, e.Price} {
				if len(v) > 12000 {
					return errors.New("builder: text too long")
				}
			}
			if err := safeURL(e.Link, false); err != nil {
				return err
			}
			if err := safeURL(e.Image, true); err != nil {
				return err
			}
		}
	}
	return nil
}
func safeURL(raw string, image bool) error {
	if raw == "" {
		return nil
	}
	if len(raw) > 2048 || strings.Contains(raw, "\\") || strings.ContainsFunc(raw, unicode.IsControl) || strings.TrimSpace(raw) != raw {
		return errors.New("builder: invalid URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || strings.HasPrefix(raw, "//") {
		return errors.New("builder: invalid URL")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		if u.Host == "" {
			return errors.New("builder: URL requires host")
		}
	case "":
		if !strings.HasPrefix(raw, "/") && !(strings.HasPrefix(raw, "#") && !image) {
			return errors.New("builder: use absolute or local URL")
		}
	case "mailto", "tel":
		if image || u.Opaque == "" {
			return errors.New("builder: invalid contact URL")
		}
	default:
		return errors.New("builder: unsupported URL scheme")
	}
	return nil
}

// ValidatePublication allows incomplete drafts but requires a visible page heading for publishing.
func ValidatePublication(doc Document) error {
	if err := Validate(doc); err != nil {
		return err
	}
	for _, section := range doc.Sections {
		if !section.Hidden && strings.TrimSpace(section.Title) != "" {
			return nil
		}
	}
	return errors.New("builder: add a visible section heading before publishing")
}
