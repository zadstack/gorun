package theme

import (
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
)

func TestReadingTime(t *testing.T) {
	long := "<p>" + strings.Repeat("كلمة word ", 300) + "</p>" // 600 words
	cases := []struct {
		name string
		in   any
		want int
	}{
		{"empty body still reads in a minute", &content.Item{}, 1},
		{"short body", &content.Item{Body: "<p>three short words</p>"}, 1},
		{"markup is not counted", &content.Item{Body: `<img src="x.png" alt="a b c d"><p>one</p>`}, 1},
		{"600 words at 200 per minute", &content.Item{Body: long}, 3},
		{"rounds up a partial minute", &content.Item{Body: "<p>" + strings.Repeat("w ", 201) + "</p>"}, 2},
		{"plain HTML string", long, 3},
		{"unknown value", 42, 1},
	}
	for _, c := range cases {
		if got := readingTime(c.in); got != c.want {
			t.Errorf("%s: readingTime = %d, want %d", c.name, got, c.want)
		}
	}
}
