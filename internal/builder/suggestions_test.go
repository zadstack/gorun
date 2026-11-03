package builder

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
)

func TestSuggestionsPreserveFieldsAndRejectLossyConversion(t *testing.T) {
	for _, kind := range []string{"features", "services", "projects", "hero", "media_text", "task_entry"} {
		section := DefaultSection(kind, "ar")
		original, _ := json.Marshal(section)
		if kind == "features" || kind == "services" || kind == "projects" {
			section.Items[0].Image = "/media/KEEP.webp"
			section.Items[0].Link = "/ar/KEEP"
			original, _ = json.Marshal(section)
		}
		candidates := suggestLayouts(section)
		if len(candidates) == 0 {
			t.Fatalf("no useful alternatives for %s", kind)
		}
		for _, candidate := range candidates {
			got := candidate.Section
			got.Kind = section.Kind
			got.Library = section.Library
			got.Variant = section.Variant

			if !reflect.DeepEqual(got, section) {
				t.Fatalf("suggestion lost content: %s", candidate.ID)
			}
			if err := Validate(Document{Version: 1, Sections: []Section{candidate.Section}}); err != nil {
				t.Fatal(err)
			}
		}
		after, _ := json.Marshal(section)
		if string(after) != string(original) {
			t.Fatal("source mutated")
		}
	}
	priced := DefaultSection("services", "en")
	priced.Items[0].Price = "$49"
	if candidates := suggestLayouts(priced); candidates == nil || len(candidates) != 0 {
		t.Fatal("price would disappear on conversion")
	}
}

// This boundary stands for the configured external AI adapter. The request
// metadata must never contain selected-page text or URLs.
type suggestionAI struct {
	input string
	reply string
	err   error
}

func (a *suggestionAI) DescribeLayout(context.Context) (string, string, error) {
	return "model.example.test", "approved-binding", nil
}
func (a *suggestionAI) SuggestLayout(_ context.Context, binding, input string) (string, error) {
	if binding != "approved-binding" {
		return "", errors.New("wrong binding")
	}
	a.input = input
	return a.reply, a.err
}

func TestSuggestionsEnforcePageScopeCSRFAndBodyLimitWithoutSaving(t *testing.T) {
	f := newLayoutFixture(t)
	author := f.user(t, "author")
	admin := f.user(t, "admin")
	own := f.page(t, author, "Own")
	other := f.page(t, admin, "Private")
	client, csrf := f.session(t, author, own.ID)
	section := DefaultSection("features", "ar")
	section.Title = "PRIVATE TITLE"
	section.Items[0].Text = "PRIVATE TEXT"
	section.Items[0].Link = "https://example.test/PRIVATE_LINK"
	section.Items[0].Image = "/media/PRIVATE_IMAGE.webp"
	payload := suggestionRequest{Section: section, Mode: "local"}
	route := "/admin/builder/" + own.ID + "/suggest"
	for _, test := range []struct {
		route, csrf string
		body        any
		status      int
	}{{route, "", payload, 403}, {"/admin/builder/" + other.ID + "/suggest", csrf, payload, 403}, {route, csrf, strings.Repeat(" ", suggestionBodyLimit+1) + "{}", 413}, {route, csrf, `{"mode":"local","unknown":true}`, 400}} {
		status, body := f.request(t, client, "POST", test.route, "", test.csrf, test.body)
		if status != test.status {
			t.Fatalf("expected %d got %d: %s", test.status, status, body)
		}
	}
	initial, err := f.svc.Load(auth.WithUser(context.Background(), author), own.ID)
	if err != nil {
		t.Fatal(err)
	}
	status, raw := f.request(t, client, "POST", route, "", csrf, payload)
	if status != 200 {
		t.Fatalf("local suggestions: %d %s", status, raw)
	}
	var local suggestionResponse
	if err := json.Unmarshal(raw, &local); err != nil {
		t.Fatal(err)
	}
	if local.Source != "local" || len(local.Candidates) == 0 || local.AIDestination != "" {
		t.Fatal("local/offline advice mislabeled")
	}
	payload.Mode = "ai"
	payload.AIBinding = "approved-binding"
	status, _ = f.request(t, client, "POST", route, "", csrf, payload)
	if status != 503 {
		t.Fatal("missing provider must remain unavailable")
	}
	adapter := &suggestionAI{reply: local.Candidates[0].ID}
	f.app.Deps.Services.Set(layoutAdvisorService, adapter)
	status, raw = f.request(t, client, "POST", route, "", csrf, payload)
	if status != 200 {
		t.Fatalf("AI selection: %d %s", status, raw)
	}
	for _, private := range []string{"PRIVATE TITLE", "PRIVATE TEXT", "PRIVATE_LINK", "PRIVATE_IMAGE"} {
		if strings.Contains(adapter.input, private) {
			t.Fatal("private content sent to provider")
		}
	}
	var selected suggestionResponse
	if err := json.Unmarshal(raw, &selected); err != nil {
		t.Fatal(err)
	}
	if selected.Source != "ai" || len(selected.Candidates) != 1 {
		t.Fatal("AI choice not identified")
	}
	for _, reply := range []string{`hero:split`, `{"kind":"features","variant":"compact_rows"}`, `features:compact_rows<script>`} {
		adapter.reply = reply
		status, _ = f.request(t, client, "POST", route, "", csrf, payload)
		if status != 502 {
			t.Fatal("arbitrary AI candidate accepted")
		}
	}
	adapter.err = errors.New("PRIVATE_PROVIDER_FAULT")
	if status, raw := f.request(t, client, "POST", route, "", csrf, payload); status != 503 || strings.Contains(string(raw), "PRIVATE_PROVIDER_FAULT") {
		t.Fatal("provider failure leaked or was mislabeled")
	}
	after, err := f.svc.Load(auth.WithUser(context.Background(), author), own.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(initial, after) {
		t.Fatal("suggestions saved a draft")
	}
	viewer := "customer"
	if _, err := f.users.Update(context.Background(), author.ID, auth.UserPatch{Role: &viewer}); err != nil {
		t.Fatal(err)
	}
	if status, _ := f.request(t, client, "POST", route, "", csrf, payload); status != 403 {
		t.Fatalf("edit permission revocation: %d", status)
	}
	item, err := f.cs.Get(auth.WithUser(context.Background(), author), own.ID)
	if err != nil || item.Body != "<p>Original public body</p>" {
		t.Fatal("suggestions changed public content")
	}
}

func TestSuggestionsExcludeEquivalentLayouts(t *testing.T) {
	for _, kind := range []string{"hero", "task_entry"} {
		section := DefaultSection(kind, "en")
		section.Library = SectionLibrary
		section.Variant = ""
		for _, candidate := range suggestLayouts(section) {
			if candidate.Section.Kind == kind && normalizedLayout(kind, candidate.Section.Variant) == normalizedLayout(kind, section.Variant) {
				t.Fatal("equivalent layout offered as an improvement")
			}
		}
	}
}
