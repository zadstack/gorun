package content

import (
	"slices"
	"testing"
)

func TestSEODrift(t *testing.T) {
	cases := []struct {
		name string
		it   Item
		want []string
	}{
		{"empty SEO never warns", Item{Title: "خدمات الإطلاق", Body: "<p>نص</p>", SEO: map[string]any{}}, nil},
		{"matching SEO", Item{Title: "خدمات الإطلاق للفنانين", Body: "<p>فحص الجاهزية وتحرير الكلمات للفنانين المستقلين</p>",
			SEO: map[string]any{"title": "خدمات الإطلاق — استوديو تجريبي", "description": "فحص الجاهزية وتحرير الكلمات للفنانين"}}, nil},
		{"rewritten page keeps old SEO", Item{Title: "خدمات الإطلاق للفنانين", Body: "<p>فحص الجاهزية وتحرير الكلمات والبث على إذاعة تجريبية</p>",
			SEO: map[string]any{"title": "صوت له شخصية وقصة تستحق أن تروى", "description": "استوديو مستقل للصوت والصورة يرتب الفكرة والتسليم"}},
			[]string{"title", "description"}},
		{"English title variant", Item{Title: "Decision services", Excerpt: "Buying reports for teams", SEO: map[string]any{"title": "Decision services | Example Reviews"}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := seoDrift(&c.it); !slices.Equal(got, c.want) {
				t.Fatalf("seoDrift = %v, want %v", got, c.want)
			}
		})
	}
}
