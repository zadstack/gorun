package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestUnapprovedUpdateRejectsConcurrentPublication(t *testing.T) {
	for _, when := range []string{"after_gate", "before_transaction"} {
		t.Run(when, func(t *testing.T) {
			e := newEnv(t, nil)
			agent := e.agent("content.view", "content.create", "content.edit")
			id := str(agent.ok("create_content", map[string]any{"type": "post", "title": "Original draft"}), "id")
			s, _ := kernel.Get[*server](e.app.Deps, serviceName)
			svc, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
			publish := func() {
				if err := svc.Publish(context.Background(), id); err != nil {
					t.Fatal(err)
				}
			}
			if when == "after_gate" {
				rule := s.dynamic["update_content"]
				s.dynamic["update_content"] = func(ctx context.Context, args json.RawMessage) bool {
					value := rule(ctx, args)
					publish()
					return value
				}
			} else {
				e.app.Deps.Hooks.Filter("content.before_save", 0, func(_ context.Context, v any) (any, error) { publish(); return v, nil })
			}
			got := agent.fails("update_content", map[string]any{"id": id, "title": "Unapproved live edit"})
			if str(got, "code") != "conflict" {
				t.Fatalf("want conflict, got %v", got)
			}
			item, _ := svc.Get(context.Background(), id)
			if item.Title != "Original draft" || item.Status != "published" {
				t.Fatalf("concurrent publication overwritten: %+v", item)
			}
			if e.count("SELECT count(*) FROM mcp_approvals") != 0 {
				t.Fatal("race should ask caller to retry, not silently execute")
			}
		})
	}
}

func TestUnapprovedTranslationRejectsConcurrentPublication(t *testing.T) {
	for _, tool := range []string{"link_translation", "unlink_translation"} {
		t.Run(tool, func(t *testing.T) {
			e := newEnv(t, nil)
			a := e.agent("content.view", "content.create", "content.edit")
			left := str(a.ok("create_content", map[string]any{"type": "post", "title": "Left", "locale": "ar"}), "id")
			right := str(a.ok("create_content", map[string]any{"type": "post", "title": "Right", "locale": "en"}), "id")
			if tool == "unlink_translation" {
				a.ok("link_translation", map[string]any{"id": left, "translation_id": right})
			}
			s, _ := kernel.Get[*server](e.app.Deps, serviceName)
			svc, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
			rule := s.dynamic[tool]
			s.dynamic[tool] = func(ctx context.Context, args json.RawMessage) bool {
				value := rule(ctx, args)
				if err := svc.Publish(context.Background(), right); err != nil {
					t.Fatal(err)
				}
				return value
			}
			args := map[string]any{"id": left}
			if tool == "link_translation" {
				args["translation_id"] = right
			}
			got := a.fails(tool, args)
			if str(got, "code") != "conflict" {
				t.Fatalf("want conflict: %v", got)
			}
			l, _ := svc.Get(context.Background(), left)
			r, _ := svc.Get(context.Background(), right)
			linked := l.TranslationGroup != "" && l.TranslationGroup == r.TranslationGroup
			if linked != (tool == "unlink_translation") {
				t.Fatal("translation membership changed before approval")
			}
		})
	}
}

func TestExplicitSelfApprovalAllowsConcurrentLiveUpdate(t *testing.T) {
	e := newEnv(t, nil)
	agent := e.agent("content.view", "content.create", "content.edit", "mcp.approve_self")
	id := str(agent.ok("create_content", map[string]any{"type": "post", "title": "Original"}), "id")
	s, _ := kernel.Get[*server](e.app.Deps, serviceName)
	svc, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	rule := s.dynamic["update_content"]
	s.dynamic["update_content"] = func(ctx context.Context, args json.RawMessage) bool {
		value := rule(ctx, args)
		if err := svc.Publish(context.Background(), id); err != nil {
			t.Fatal(err)
		}
		return value
	}
	got := agent.ok("update_content", map[string]any{"id": id, "title": "Explicitly self-approved"})
	if str(got, "title") != "Explicitly self-approved" || str(got, "status") != "published" {
		t.Fatalf("self-approved update failed: %v", got)
	}
}
