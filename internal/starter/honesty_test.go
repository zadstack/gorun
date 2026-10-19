package starter

import (
	"strings"
	"testing"
)

func TestSampleContentHasNoLoremEmDashOrHomeNoindex(t *testing.T) {
	s := string(WXR)
	if s == "" {
		t.Fatal("embedded sample-content.xml is empty")
	}
	lower := strings.ToLower(s)
	if strings.Contains(lower, "lorem") || strings.Contains(lower, "ipsum") {
		t.Fatal("sample-content.xml must not contain lorem ipsum")
	}
	if strings.ContainsRune(s, '\u2014') {
		t.Fatal("sample-content.xml must not contain em dashes")
	}
	if strings.Contains(lower, "noindex") {
		t.Fatal("sample-content.xml must not seed noindex")
	}
}
