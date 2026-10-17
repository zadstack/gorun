package mcp

import "testing"

// The translation tools mirror the REST operations: create a linked draft, list, unlink, link.
func TestTranslationTools(t *testing.T) {
	e := newEnv(t, &fakeModule{})
	a := e.agent("*")
	ar := str(a.ok("create_content", map[string]any{"type": "post", "title": "عربي", "locale": "ar"}), "id")
	en := a.ok("create_translation", map[string]any{"id": ar, "locale": "en"})
	if str(en, "locale") != "en" || str(en, "status") != "draft" || str(en, "translation_group") == "" {
		t.Fatalf("create_translation: %v", en)
	}
	if errObj := a.fails("create_translation", map[string]any{"id": ar, "locale": "en"}); str(errObj, "code") != "validation_failed" {
		t.Fatalf("taken locale: %v", errObj)
	}
	env, _ := a.call("list_translations", map[string]any{"id": ar})
	if items, _ := env["data"].([]any); len(items) != 2 {
		t.Fatalf("list_translations: %v", env)
	}
	a.ok("unlink_translation", map[string]any{"id": str(en, "id")})
	env, _ = a.call("link_translation", map[string]any{"id": ar, "translation_id": str(en, "id")})
	if items, _ := env["data"].([]any); len(items) != 2 {
		t.Fatalf("link_translation: %v", env)
	}
	reader := e.agent("content.view").toolNames()
	if !contains(reader, "list_translations") || contains(reader, "create_translation") || contains(reader, "link_translation") {
		t.Fatalf("scopes: %v", reader)
	}
}

// Linking or unlinking translations of a live item changes public pages (hreflang, the language
// link), so it waits for approval like update_content on a live item; drafts do not.
func TestLiveTranslationLinksNeedApproval(t *testing.T) {
	e := newEnv(t, nil)
	self := e.agent("content.view", "content.create", "content.edit", "content.publish", "mcp.approve_self")
	live := str(self.ok("create_content", map[string]any{"type": "post", "title": "Live", "locale": "ar"}), "id")
	self.ok("publish_content", map[string]any{"id": live})
	draft := str(self.ok("create_content", map[string]any{"type": "post", "title": "Draft", "locale": "en"}), "id")
	other := str(self.ok("create_content", map[string]any{"type": "post", "title": "Other", "locale": "en"}), "id")
	a := e.agent("content.view", "content.create", "content.edit")

	if p := a.ok("link_translation", map[string]any{"id": live, "translation_id": draft}); str(p, "status") != "pending_approval" {
		t.Fatalf("link to a live item: %v", p)
	}
	if list, _ := e.agent("*").call("list_translations", map[string]any{"id": live}); len(list["data"].([]any)) != 1 {
		t.Fatalf("linked before approval: %v", list)
	}
	self.ok("link_translation", map[string]any{"id": live, "translation_id": draft}) // approve_self runs it
	if p := a.ok("unlink_translation", map[string]any{"id": draft}); str(p, "status") != "pending_approval" {
		t.Fatalf("unlink from a live group: %v", p)
	}
	// two drafts: no approval
	solo := str(a.ok("create_content", map[string]any{"type": "post", "title": "Solo", "locale": "ar"}), "id")
	if p := a.ok("link_translation", map[string]any{"id": solo, "translation_id": other}); str(p, "status") == "pending_approval" {
		t.Fatalf("drafts need no approval: %v", p)
	}
}
