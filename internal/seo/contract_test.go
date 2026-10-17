package seo

import (
	"reflect"
	"testing"
)

func TestFromMapToMapRoundTrip(t *testing.T) {
	in := map[string]any{"title": "T", "description": "D", "og_image": "/media/x.png", "robots": "noindex",
		"schema": map[string]any{"@type": "Article"}, "ignored": 1}
	m := FromMap(in)
	if m.Title != "T" || m.OGImage != "/media/x.png" || string(m.Schema) != `{"@type":"Article"}` {
		t.Fatalf("%+v", m)
	}
	delete(in, "ignored")
	if out := m.ToMap(); !reflect.DeepEqual(out, in) {
		t.Fatalf("round trip: %v", out)
	}
	if len(Meta{}.ToMap()) != 0 || FromMap(nil).Title != "" {
		t.Fatal("empty values")
	}
}
