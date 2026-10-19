package kernel

import (
	"context"
	"html/template"
	"net/http"
	"testing"
)

func TestAdminNavSortedAndPermissionFiltered(t *testing.T) {
	a := NewAdminRegistry()
	a.AddNav(NavItem{ID: "media", Position: 20, Permission: "media.upload"})
	a.AddNav(NavItem{ID: "content", Position: 10})
	a.AddNav(NavItem{ID: "aaa", Position: 20})
	got := a.Nav(context.Background())
	if len(got) != 3 || got[0].ID != "content" || got[1].ID != "aaa" || got[2].ID != "media" {
		t.Fatalf("%+v", got)
	}
	a.SetPermissionCheck(func(_ context.Context, perm string) bool { return perm != "media.upload" })
	if got := a.Nav(context.Background()); len(got) != 2 {
		t.Fatalf("want media filtered out: %+v", got)
	}
	a.SetNavFilter(func(_ context.Context, items []NavItem) []NavItem {
		out := make([]NavItem, 0, len(items))
		for _, n := range items {
			if n.ID != "aaa" {
				out = append(out, n)
			}
		}
		return out
	})
	if got := a.Nav(context.Background()); len(got) != 1 || got[0].ID != "content" {
		t.Fatalf("want nav filter applied: %+v", got)
	}
	a.AddWidget("stats", func(context.Context, *http.Request) (template.HTML, error) { return "<b>1</b>", nil })
	if w := a.Widgets(); len(w) != 1 || w[0].ID != "stats" {
		t.Fatalf("%+v", w)
	}
}

// Nav providers run on every Nav call, are merged, filtered and sorted,
// and never override a static item with the same ID.
func TestAdminNavProviderEvaluatedPerCall(t *testing.T) {
	a := NewAdminRegistry()
	a.AddNav(NavItem{ID: "static", Position: 50, LabelKey: "static"})
	n := 0
	a.AddNavProvider(func(ctx context.Context) []NavItem {
		n++
		items := []NavItem{{ID: "static", LabelKey: "dup"}, {ID: "secret", Permission: "x.secret", Position: 1}}
		for i := 0; i < n; i++ {
			items = append(items, NavItem{ID: "dyn" + string(rune('a'+i)), Position: 10 + i})
		}
		return items
	})
	a.SetPermissionCheck(func(_ context.Context, perm string) bool { return perm != "x.secret" })
	if got := a.Nav(context.Background()); len(got) != 2 || got[0].ID != "dyna" || got[1].ID != "static" || got[1].LabelKey != "static" {
		t.Fatalf("first call: %+v", got)
	}
	if got := a.Nav(context.Background()); len(got) != 3 || got[1].ID != "dynb" {
		t.Fatalf("second call must see the new item: %+v", got)
	}
}

func TestDuplicateRegistrationPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("want panic on duplicate nav id")
		}
	}()
	a := NewAdminRegistry()
	a.AddNav(NavItem{ID: "x"})
	a.AddNav(NavItem{ID: "x"})
}

func TestThemeRegistry(t *testing.T) {
	r := NewThemeRegistry()
	r.AddFunc("upper", func(s string) string { return s })
	r.AddKind("single", func(req KindRequest) []string { return []string{"single-" + req.Slug, "single"} })
	if _, ok := r.Funcs()["upper"]; !ok {
		t.Fatal("func missing")
	}
	if c := r.Candidates(KindRequest{Kind: "single", Slug: "hello"}); len(c) != 2 || c[0] != "single-hello" {
		t.Fatalf("%v", c)
	}
	if c := r.Candidates(KindRequest{Kind: "unknown"}); c != nil {
		t.Fatalf("%v", c)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("want panic for non-func")
		}
	}()
	r.AddFunc("bad", 42)
}

func TestPermissionsAPIAndMCPRegistries(t *testing.T) {
	p := NewPermissionRegistry()
	p.Declare("content.publish", map[string]string{"en": "Publish"})
	p.Declare("audit.view", map[string]string{"en": "View audit"})
	if all := p.All(); len(all) != 2 || all[0].Name != "audit.view" || all[1].Label["en"] != "Publish" {
		t.Fatalf("%+v", all)
	}
	api := NewAPIRegistry()
	api.Add(APIOperation{Method: "GET", Path: "/v1/content", Handler: func(http.ResponseWriter, *http.Request) {}})
	if ops := api.Operations(); len(ops) != 1 || ops[0].Path != "/v1/content" {
		t.Fatalf("%+v", ops)
	}
	m := NewMCPRegistry()
	m.AddTool(MCPTool{Name: "content_list"})
	if tools := m.Tools(); len(tools) != 1 || tools[0].Name != "content_list" {
		t.Fatalf("%+v", tools)
	}
}

type fakeService struct{ name string }

func TestServiceRegistry(t *testing.T) {
	d := &Deps{Services: NewServiceRegistry()}
	d.Services.Set("content", &fakeService{name: "c"})
	s, ok := Get[*fakeService](d, "content")
	if !ok || s.name != "c" {
		t.Fatal("get failed")
	}
	if _, ok := Get[string](d, "content"); ok {
		t.Fatal("wrong type must not match")
	}
	if _, ok := Get[*fakeService](d, "missing"); ok {
		t.Fatal("missing must not match")
	}
}
