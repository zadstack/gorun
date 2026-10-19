package admin

import (
	"strings"
	"testing"
)

func TestStructuralTemplateStripsAllBindingsAndSensitiveText(t *testing.T) {
	b := &canvasBoard{Cards: []canvasCard{
		{ID: "11111111-1111-4111-8111-111111111111", Kind: "content", Title: "secret title", Body: "secret body", ContentID: "secret-content", X: 42, Y: 80},
		{ID: "22222222-2222-4222-8222-222222222222", Kind: "provider", Title: "secret title", Body: "secret body", GenerationKind: "image", ProviderID: "secret-provider", PresetID: "secret-skill"},
		{ID: "33333333-3333-4333-8333-333333333333", Kind: "result", RunID: "secret-run", Body: "secret output"},
		{ID: "44444444-4444-4444-8444-444444444444", Kind: "source", SourceType: "secret-type", SourceIDs: []string{"secret-item"}, SourceLimit: 5},
		{ID: "55555555-5555-4555-8555-555555555555", Kind: "link", URL: "https://user:secret@example.com"},
		{ID: "66666666-6666-4666-8666-666666666666", Kind: "checklist", Items: []canvasCheck{{Text: "secret item", Checked: true}}},
	}, Edges: []canvasEdge{{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"}}}
	cards, edges, e := canvasTemplateDocument(b)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(canvasEncode(cards), "secret") || cards[2].Kind != "note" || cards[4].Kind != "note" || cards[0].X != 42 || cards[1].GenerationKind != "image" {
		t.Fatal("unsafe structural template", canvasEncode(cards))
	}
	if len(edges) != 1 || edges[0].From != cards[0].ID || edges[0].To != cards[1].ID {
		t.Fatal("edge remap", edges)
	}
	for i, c := range cards {
		if !canvasCardID.MatchString(c.ID) || c.ID == b.Cards[i].ID {
			t.Fatal("ID not regenerated", c)
		}
	}
}
