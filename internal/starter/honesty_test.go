package starter

import (
	"encoding/xml"
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

func TestEveryStarterItemIsAnExplicitExample(t *testing.T) {
	var doc struct {
		Items []wxrItem `xml:"channel>item"`
	}
	if err := xml.Unmarshal(WXR, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Items) != 9 {
		t.Fatalf("starter shape changed: %d items", len(doc.Items))
	}
	for _, item := range doc.Items {
		if !strings.Contains(item.Body, "محتوى توضيحي") {
			t.Errorf("%s is not explicitly an editable example", item.Title)
		}
		if len(item.Body) < 300 {
			t.Errorf("%s has no useful authored example", item.Title)
		}
	}
	for _, claim := range []string{"Stripe", "خوادم MCP", "خمس دقائق", "الخدمة الأولى"} {
		if strings.Contains(string(WXR), claim) {
			t.Errorf("starter must not imply available services or use unfinished placeholder: %s", claim)
		}
	}
}
