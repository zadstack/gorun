package admin

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestMaterializeCanvasAISuggestions(t *testing.T) {
	raw := `{
  "suggestions": [
    {
      "title": "Morning brief",
      "summary": "Summarize ops items each morning.",
      "cards": [
        {"kind":"source","title":"Recent posts","body":"","x":24,"y":24,"source_limit":3},
        {"kind":"prompt","title":"Instructions","body":"Write a concise morning brief. Do not publish.","x":420,"y":24},
        {"kind":"agent","title":"Agent","body":"","x":816,"y":24},
        {"kind":"note","title":"Review","body":"Check before sharing.","x":816,"y":240}
      ],
      "edges": [{"from":0,"to":2},{"from":1,"to":2}]
    }
  ]
}`
	h := &handlers{d: &kernel.Deps{}}
	ctx := auth.WithUser(context.Background(), &auth.User{ID: "01JTESTUSER0000000000000000", Role: "admin"})
	items, err := materializeCanvasAISuggestions(ctx, h, raw, "agent")
	if err != nil || len(items) != 1 {
		t.Fatalf("materialize: %v len=%d", err, len(items))
	}
	if items[0].Title != "Morning brief" || len(items[0].Cards) != 4 || len(items[0].Edges) != 2 {
		t.Fatalf("unexpected suggestion: %+v", items[0])
	}
	if items[0].Cards[2].Kind != "agent" || items[0].Cards[2].AgentID != "" {
		t.Fatalf("agent card: %+v", items[0].Cards[2])
	}
}

func TestMaterializeCanvasAISuggestionsRejectsInvalidGraph(t *testing.T) {
	raw := `{"suggestions":[{"title":"Bad","summary":"Bad flow","cards":[{"kind":"note","title":"n","body":"x"}],"edges":[]}]}`
	h := &handlers{d: &kernel.Deps{}}
	ctx := auth.WithUser(context.Background(), &auth.User{ID: "01JTESTUSER0000000000000001", Role: "admin"})
	if items, err := materializeCanvasAISuggestions(ctx, h, raw, "agent"); err == nil || len(items) != 0 {
		t.Fatalf("expected rejection, got %v %d", err, len(items))
	}
}

func TestMaterializeCanvasAISuggestionsStripsFence(t *testing.T) {
	raw := "```json\n" + strings.TrimSpace(`{
  "suggestions": [{
    "title":"Lead reply",
    "summary":"Draft a reply for humans to send.",
    "cards":[
      {"kind":"prompt","title":"Prompt","body":"Draft a short reply. Do not send email.","x":24,"y":24},
      {"kind":"provider","title":"Provider","body":"","x":420,"y":24},
      {"kind":"note","title":"Review","body":"Send manually.","x":816,"y":240}
    ],
    "edges":[{"from":0,"to":1}]
  }]
}`) + "\n```"
	h := &handlers{d: &kernel.Deps{}}
	ctx := auth.WithUser(context.Background(), &auth.User{ID: "01JTESTUSER0000000000000002", Role: "admin"})
	items, err := materializeCanvasAISuggestions(ctx, h, raw, "provider")
	if err != nil || len(items) != 1 || items[0].Cards[1].Kind != "provider" || items[0].Cards[1].GenerationKind != "text" {
		t.Fatalf("provider flow: %v %+v", err, items)
	}
}

func TestCanvasAICacheExpiryAndCapacity(t *testing.T) {
	h := &handlers{}
	c := h.canvasAICache()
	c.put("expired", canvasAIGeneration{ExpiresAt: time.Now().Add(-time.Second)})
	if _, ok := c.get("expired"); ok {
		t.Fatal("expired generation retained")
	}
	for i := range 5000 {
		c.put(fmt.Sprint(i), canvasAIGeneration{ExpiresAt: time.Now().Add(time.Hour)})
	}
	c.put("overflow", canvasAIGeneration{ExpiresAt: time.Now().Add(time.Hour)})
	if _, ok := c.get("overflow"); ok || len(c.entries) != 5000 {
		t.Fatal("cache exceeded hard bound")
	}
	c.put("0", canvasAIGeneration{RequestID: "replacement", ExpiresAt: time.Now().Add(time.Hour)})
	if item, ok := c.get("0"); !ok || item.RequestID != "replacement" {
		t.Fatal("owner update blocked at capacity")
	}
}

func TestCanvasAIPreviewExpiryOwnerAndOneShot(t *testing.T) {
	h := &handlers{}
	c := h.canvasAICache()
	c.previews["expired"] = canvasAIPrepared{Owner: "a", Expires: time.Now().Add(-time.Second)}
	if _, ok := c.take("a", "expired"); ok {
		t.Fatal("expired review accepted")
	}
	if _, exists := c.previews["expired"]; exists {
		t.Fatal("expired secret-bearing review retained")
	}
	c.previews["live"] = canvasAIPrepared{Owner: "a", Expires: time.Now().Add(time.Minute)}
	if _, ok := c.take("b", "live"); ok {
		t.Fatal("wrong owner consumed review")
	}
	if _, ok := c.take("a", "live"); !ok {
		t.Fatal("wrong-owner attempt burned review")
	}
	if _, ok := c.take("a", "live"); ok {
		t.Fatal("review replay accepted")
	}
}
func TestCanvasAISuggestionsRejectInjectedAuthorityAndMalformedShape(t *testing.T) {
	h := &handlers{d: &kernel.Deps{}}
	ctx := auth.WithUser(context.Background(), &auth.User{ID: "owner", Role: "admin"})
	for _, raw := range []string{
		`{"suggestions":[{"title":"t","summary":"s","cards":[{"kind":"agent","agent_id":"privileged-key"}],"edges":[]}]}`,
		`{"suggestions":[{"title":"t","summary":"s","cards":[{"kind":"provider","provider_id":"private-provider"}],"edges":[]}]}`,
		`{"suggestions":[{"title":"t","summary":"s","cards":[{"kind":"schedule","schedule_enabled":true}],"edges":[]}]}`,
		`{"suggestions":[{"title":"t","summary":"s","cards":[{"kind":"widget","title":"Private widget"}],"edges":[]}]}`,
		`{"suggestions":[]} {"ignored":true}`,
		`{"suggestions":[{"title":"t","summary":"s","cards":[{"kind":"agent"},{"kind":"agent"}],"edges":[]}]}`,
		`{"suggestions":[{"title":"t","summary":"s","cards":[{"kind":"agent"}],"edges":[{"from":0,"to":5}]}]}`,
		`{"suggestions":[{"title":"t","summary":"s","cards":[{"kind":"agent"}],"edges":[{"from":0,"to":0}]}]}`,
	} {
		if _, err := materializeCanvasAISuggestions(ctx, h, raw, "agent"); err == nil {
			t.Fatalf("accepted authority injection or malformed flow: %s", raw)
		}
	}
}
