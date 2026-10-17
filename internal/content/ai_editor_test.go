package content_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestEditorAIMenuFollowsSetting: the AI menu and the title / excerpt
// helpers render only while ai.provider is set.
func TestEditorAIMenuFollowsSetting(t *testing.T) {
	s := loggedIn(t, "editor")
	markers := []string{`x-data="gpAI"`, `data-action="title"`, `data-action="excerpt"`, `run('translate_ar')`}
	resp, page := s.do("GET", "/admin/content/post/new", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("editor: %d", resp.StatusCode)
	}
	for _, m := range markers {
		if strings.Contains(page, m) {
			t.Fatalf("AI helper %q shown while AI is off", m)
		}
	}
	if err := s.app.Deps.Settings.Set(context.Background(), "ai.provider", "openai"); err != nil {
		t.Fatal(err)
	}
	_, page = s.do("GET", "/admin/content/post/new", nil, nil)
	for _, m := range markers {
		if !strings.Contains(page, m) {
			t.Fatalf("AI helper %q missing while AI is on", m)
		}
	}
}
