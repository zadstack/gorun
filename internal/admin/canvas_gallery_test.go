package admin_test

import (
	"strings"
	"testing"
)

func TestCanvasGalleryPreviewIsPrivateAndEscaped(t *testing.T) {
	s := boot(t)
	owner := s.login("gallery-owner@example.test", "admin", "en")
	path := newCanvas(t, s)
	card := map[string]any{"id": "12345678-1234-4123-8123-123456789abc", "kind": "note", "title": "Preview note", "body": "<script>privateBody</script>", "x": 24, "y": 48}
	if code, _ := studioJSON(t, s, path+"/document", canvasInput(card), true); code != 200 {
		t.Fatal(code)
	}
	_, page := s.do("GET", "/admin", nil)
	mustContain(t, page, `data-widget="canvas_gallery"`, `class="canvas-gallery-grid"`, "Preview note", "&lt;script&gt;privateBody&lt;/script&gt;")
	mustNotContain(t, page, "<script>privateBody</script>", "ZgotmplZ")
	s.login("gallery-other@example.test", "admin", "en")
	_, page = s.do("GET", "/admin", nil)
	if strings.Contains(page, "Preview note") || strings.Contains(page, "privateBody") || strings.Contains(page, path) {
		t.Fatal("foreign canvas preview exposed")
	}
	s.signIn(owner.Email)
	_, page = s.do("GET", "/admin", nil)
	mustContain(t, page, "Preview note")
}
