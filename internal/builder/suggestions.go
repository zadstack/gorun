package builder

import (
	"encoding/json"
	"strings"
)

const maxLayoutSuggestions = 5

type layoutSuggestion struct {
	ID        string  `json:"id"`
	LabelKey  string  `json:"label_key"`
	ReasonKey string  `json:"reason_key"`
	Section   Section `json:"section"`
}

func suggestLayouts(section Section) []layoutSuggestion {
	suggestions := make([]layoutSuggestion, 0, maxLayoutSuggestions)
	offer := func(kind, variant, reason string) {
		candidate := section
		candidate.Kind, candidate.Variant, candidate.Library = kind, variant, SectionLibrary
		if candidate.Kind == section.Kind && normalizedLayout(kind, candidate.Variant) == normalizedLayout(section.Kind, section.Variant) && candidate.Library == section.Library {
			return
		}
		if Validate(Document{Version: 1, Sections: []Section{candidate}}) != nil {
			return
		}
		if len(suggestions) < maxLayoutSuggestions {
			suggestions = append(suggestions, layoutSuggestion{ID: kind + ":" + variant, LabelKey: "layout_" + variant, ReasonKey: reason, Section: candidate})
		}
	}
	switch section.Kind {
	case "features", "services", "projects":
		offer("features", "compact_rows", "suggest_reason_scan")
		if hasEntryImages(section.Items) {
			offer("features", "image_led", "suggest_reason_images")
		}
		if len(section.Items) > 1 {
			offer("features", "featured_secondary", "suggest_reason_priority")
		}
	case "media_text":
		offer("media_text", "image_start", "suggest_reason_image_start")
		offer("media_text", "image_end", "suggest_reason_image_end")
	case "hero":
		offer("hero", "split", "suggest_reason_split")
		offer("hero", "centered", "suggest_reason_centered")
	case "faq":
		offer("faq", "split", "suggest_reason_faq")
		offer("faq", "default", "suggest_reason_scan")
	case "directory":
		offer("directory", "compact", "suggest_reason_scan")
		offer("directory", "default", "suggest_reason_images")
	case "task_entry":
		if section.Variant != "search_first" {
			offer("task_entry", "choice_first", "suggest_reason_choice")
			offer("task_entry", "service_first", "suggest_reason_service")
		}
	}
	return suggestions
}
func normalizedLayout(kind, variant string) string {
	if variant == "" || variant == "default" {
		switch kind {
		case "hero":
			return "centered"
		case "task_entry":
			return "choice_first"
		default:
			return "default"
		}
	}
	return variant
}

func hasEntryImages(entries []Entry) bool {
	for _, entry := range entries {
		if entry.Image != "" {
			return true
		}
	}
	return false
}

type suggestionShape struct {
	Kind       string   `json:"kind"`
	Library    string   `json:"library"`
	Variant    string   `json:"variant"`
	Entries    int      `json:"entries"`
	WithImages int      `json:"with_images"`
	WithLinks  int      `json:"with_links"`
	Candidates []string `json:"candidate_ids"`
}

func suggestionMetadata(section Section, candidates []layoutSuggestion) (string, error) {
	shape := suggestionShape{Kind: section.Kind, Library: section.Library, Variant: section.Variant, Entries: len(section.Items), Candidates: []string{}}
	for _, entry := range section.Items {
		if entry.Image != "" {
			shape.WithImages++
		}
		if entry.Link != "" {
			shape.WithLinks++
		}
	}
	for _, candidate := range candidates {
		shape.Candidates = append(shape.Candidates, candidate.ID)
	}
	raw, err := json.Marshal(shape)
	return string(raw), err
}
func chosenSuggestion(output string, candidates []layoutSuggestion) (layoutSuggestion, bool) {
	id := strings.TrimSpace(output)
	if len(id) > 80 {
		return layoutSuggestion{}, false
	}
	for _, candidate := range candidates {
		if candidate.ID == id {
			return candidate, true
		}
	}
	return layoutSuggestion{}, false
}
