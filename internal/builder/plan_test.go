package builder

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

// plan_page_layout is a dry run: it validates, lists what the document
// would change in the draft, says whether it could be published and hands
// out the save token, and writes nothing. The sensitive publish and
// restore tools describe what they would change for the approvals page.
func TestPlanAndPreviews(t *testing.T) {
	f := newLayoutFixture(t)
	admin := f.user(t, "admin")
	page := f.page(t, admin, "Planned")
	ctx := auth.WithUser(context.Background(), admin)
	tools := map[string]kernel.MCPTool{}
	for _, tool := range f.app.Deps.MCP.Tools() {
		tools[tool.Name] = tool
	}
	plan, save, publish, restore := tools["plan_page_layout"], tools["save_page_layout"], tools["publish_page_layout"], tools["restore_page_layout_revision"]
	if plan.Handler == nil || !plan.ReadOnly || plan.Sensitive || publish.Preview == nil || restore.Preview == nil || save.Preview != nil {
		t.Fatalf("registration: plan=%+v publish.Preview=%v restore.Preview=%v", plan, publish.Preview != nil, restore.Preview != nil)
	}
	call := func(tool kernel.MCPTool, in any) (any, error) {
		raw, _ := json.Marshal(in)
		return tool.Handler(ctx, raw)
	}
	hero := DefaultSection("hero", "en")
	hero.Title = "Planned hero"
	doc := Document{Version: 1, Sections: []Section{hero}}

	value, err := call(plan, layoutPlanInput{ID: page.ID, Document: doc})
	if err != nil {
		t.Fatal(err)
	}
	p := value.(*LayoutPlan)
	if !p.Valid || !p.Publishable || p.BaseUpdatedAt == "" || p.DraftInSync || !strings.Contains(p.Plan, "+ hero "+hero.ID+": Planned hero") || p.Diff.Summary != "1 added" {
		t.Fatalf("plan: %+v", p)
	}
	if d, _ := f.svc.Load(ctx, page.ID); len(d.Document.Sections) != 0 {
		t.Fatal("plan wrote the draft")
	}
	bad := doc
	bad.Sections = []Section{{ID: "x", Kind: "nope"}}
	value, _ = call(plan, layoutPlanInput{ID: page.ID, Document: bad})
	if p := value.(*LayoutPlan); p.Valid || p.Errors["document"] == "" {
		t.Fatalf("invalid document planned as valid: %+v", p)
	}

	// Save with the planned token, then the publish preview shows the
	// first publication; after publishing, a further edit previews as a
	// change, and a stale token is called out.
	value, err = call(save, layoutSaveInput{ID: page.ID, Document: doc, BaseUpdatedAt: p.BaseUpdatedAt})
	if err != nil {
		t.Fatal(err)
	}
	saved := value.(*Draft)
	args, _ := json.Marshal(layoutPublishInput{ID: page.ID, UpdatedAt: saved.UpdatedAt})
	text, err := publish.Preview(ctx, args)
	if err != nil || !strings.HasPrefix(text, "first publication from the builder: 1 added") || strings.Contains(text, "!") {
		t.Fatalf("publish preview: %q %v", text, err)
	}
	if _, err := call(publish, layoutPublishInput{ID: page.ID, UpdatedAt: saved.UpdatedAt}); err != nil {
		t.Fatal(err)
	}
	draft, _ := f.svc.Load(ctx, page.ID)
	edited := draft.Document
	edited.Sections[0].Title = "Edited hero"
	value, _ = call(plan, layoutPlanInput{ID: page.ID, Document: edited})
	if p := value.(*LayoutPlan); !p.DraftInSync || !strings.Contains(p.Plan, "title: Planned hero → Edited hero") {
		t.Fatalf("plan after publish: %+v", p)
	}
	value, err = call(save, layoutSaveInput{ID: page.ID, Document: edited, BaseUpdatedAt: draft.UpdatedAt})
	saved = mustDraft(t, value, err)
	args, _ = json.Marshal(layoutPublishInput{ID: page.ID, UpdatedAt: "stale"})
	text, err = publish.Preview(ctx, args)
	if err != nil || !strings.Contains(text, "title: Planned hero → Edited hero") || !strings.Contains(text, "! the draft changed") {
		t.Fatalf("stale publish preview: %q %v", text, err)
	}
	revs, _ := f.svc.ListRevisions(ctx, page.ID)
	args, _ = json.Marshal(layoutRestoreInput{ID: page.ID, RevisionID: revs[0].ID, UpdatedAt: saved.UpdatedAt})
	text, err = restore.Preview(ctx, args)
	if err != nil || !strings.Contains(text, "title: Edited hero → Planned hero") || strings.Contains(text, "!") {
		t.Fatalf("restore preview: %q %v", text, err)
	}
	// The preview runs as the viewer: someone who may not read the page gets an error, not a diff.
	if _, err := publish.Preview(auth.WithUser(context.Background(), f.user(t, "contributor")), args); err == nil {
		t.Fatal("preview ignored permissions")
	}

	// REST twin.
	token := f.token(t, admin, []string{"content.edit"})
	status, raw := f.request(t, &http.Client{}, http.MethodPost, "/api/v1/builder/"+page.ID+"/plan", token, "", layoutPlanRequest{Document: doc})
	if status != 200 || !strings.Contains(string(raw), `"plan":`) || !strings.Contains(string(raw), "Edited hero → Planned hero") {
		t.Fatalf("POST plan: %d %s", status, raw)
	}
	if status, _ := f.request(t, &http.Client{}, http.MethodPost, "/api/v1/builder/"+page.ID+"/plan", token, "", `{"document":{"version":1,"sections":[]},"extra":1}`); status != 400 {
		t.Fatalf("plan with unknown field: %d", status)
	}
}

func mustDraft(t *testing.T, v any, err error) *Draft {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return v.(*Draft)
}
