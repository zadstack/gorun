package admin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
)

const spillID = "0123456789abcdef0123456789abcdef"

func spillDigits(n int) string {
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		b.WriteByte(byte('0' + i%10))
	}
	return b.String()[:n]
}

func TestCanvasInlineSpillCutsLongItemWithExactMarker(t *testing.T) {
	body := spillDigits(30000)
	out, spilled, err := canvasInline([]canvasSourceItem{{Ref: "content/" + spillID, Title: "Long report", Body: body}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "## [ref:content/"+spillID+"] Long report\n") {
		t.Fatalf("missing header: %q", out[:80])
	}
	if !strings.Contains(out, body[:8000]) || strings.Contains(out, body[:8001]) {
		t.Fatal("first 8,000 characters not kept exactly")
	}
	if !strings.HasSuffix(out, "\n[… 22,000 more characters. Full text: ref:content/"+spillID+"]") {
		t.Fatalf("marker missing or wrong: %q", out[len(out)-120:])
	}
	if len(spilled) != 1 || spilled[0] != (canvasSpill{Ref: "content/" + spillID, Kept: 8000, Total: 30000}) {
		t.Fatalf("spill record %#v", spilled)
	}
}

func TestCanvasInlineSpillSharesBudgetAcrossManySources(t *testing.T) {
	items := []canvasSourceItem{}
	for i := range 10 {
		items = append(items, canvasSourceItem{Ref: "content/" + spillID[:31] + string(rune('a'+i)), Title: "Source", Body: strings.Repeat("x", 20000)})
	}
	out, spilled, err := canvasInline(items)
	if err != nil {
		t.Fatal("run failed for size", err)
	}
	if n := utf8.RuneCountInString(out); n > canvasContextLimit {
		t.Fatal("context over limit", n)
	}
	if len(spilled) != 10 || strings.Count(out, "more characters. Full text: ref:content/") != 10 {
		t.Fatal("each large source needs a marker", len(spilled))
	}
	for i, s := range spilled {
		if s.Ref != items[i].Ref || s.Total != 20000 || s.Kept < 5000 || s.Kept > 8000 {
			t.Fatalf("unfair share %#v", s)
		}
	}
}

func TestCanvasInlineSpillKeepsSmallSourceWhole(t *testing.T) {
	small := "A short, complete brief.\nSecond line."
	items := []canvasSourceItem{{Ref: "note/n1", Title: "Large", Body: strings.Repeat("y", 40000)}, {Ref: "note/n2", Title: "Small", Body: small}}
	for range 9 {
		items = append(items, canvasSourceItem{Ref: "note/n3", Title: "Large", Body: strings.Repeat("z", 40000)})
	}
	out, spilled, err := canvasInline(items)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "## [ref:note/n2] Small\n"+small+"\n\n") {
		t.Fatal("small source not kept in full")
	}
	for _, s := range spilled {
		if s.Ref == "note/n2" {
			t.Fatal("small source spilled")
		}
	}
	if strings.Index(out, "ref:note/n1]") > strings.Index(out, "ref:note/n2]") {
		t.Fatal("source order changed")
	}
}

func TestCanvasInlineSpillFailsWhenMinimumsExceedLimit(t *testing.T) {
	items := []canvasSourceItem{}
	for range 120 {
		items = append(items, canvasSourceItem{Ref: "content/" + spillID, Title: "Source", Body: strings.Repeat("q", 2000)})
	}
	if _, _, err := canvasInline(items); err != errCanvasInvalid {
		t.Fatal("minimums over limit must fail", err)
	}
}

func TestCanvasInlineSpillCutsArabicOnRuneBoundaries(t *testing.T) {
	body := strings.Repeat("مرحبا بالعالم ", 3000)
	out, spilled, err := canvasInline([]canvasSourceItem{{Ref: "content/" + spillID, Title: "تقرير", Body: body}})
	if err != nil || len(spilled) != 1 {
		t.Fatal(err, spilled)
	}
	if !utf8.ValidString(out) || strings.ContainsRune(out, utf8.RuneError) {
		t.Fatal("cut split a rune")
	}
	total := utf8.RuneCountInString(body)
	if spilled[0].Total != total || spilled[0].Kept > 8000 || spilled[0].Kept < 7000 {
		t.Fatalf("arabic spill %#v", spilled[0])
	}
	if !strings.Contains(out, "["+"… "+canvasGroup(total-spilled[0].Kept)+" more characters.") {
		t.Fatal("marker count disagrees with kept text")
	}
}

func TestCanvasInlineSpillPrefersLineBoundary(t *testing.T) {
	body := strings.Repeat("p", 7900) + "\n\n" + strings.Repeat("R", 5000)
	out, spilled, err := canvasInline([]canvasSourceItem{{Ref: "note/n", Title: "T", Body: body}})
	if err != nil || len(spilled) != 1 {
		t.Fatal(err, spilled)
	}
	if spilled[0].Kept != 7900 || strings.Contains(out, "R") || !strings.HasSuffix(out, strings.Repeat("p", 7900)+"\n[… 5,002 more characters. Full text: ref:note/n]") {
		t.Fatalf("not cut on paragraph boundary %#v", spilled[0])
	}
}

// spillBoard connects a content item to the fixture agent and grants it.
func spillBoard(t *testing.T, body string) (*handlers, context.Context, *canvasBoard, canvasPrepared, *content.Item) {
	t.Helper()
	h, ctx, b, _ := canvasOperationFixture(t, content.New())
	cs, _ := h.contentService()
	it, e := cs.Create(ctx, content.Input{Type: "post", Title: "Long article", Body: body, Status: "draft", AuthorID: auth.UserFromContext(ctx).ID})
	if e != nil {
		t.Fatal(e)
	}
	card := canvasCard{ID: "44444444-4444-4444-8444-444444444444", Kind: "content", ContentID: it.ID, Title: "Long article"}
	cards := append(b.Cards, card)
	edges := append(b.Edges, canvasEdge{card.ID, b.Cards[1].ID})
	if _, e = h.d.DB.Exec(`UPDATE admin_canvases SET document_json=?,edges_json=? WHERE id=?`, canvasEncode(cards), canvasEncode(edges), b.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = h.d.DB.Exec(`UPDATE admin_canvas_grants SET node_ids=? WHERE board_id=?`, canvasEncode([]string{b.Cards[0].ID, card.ID}), b.ID); e != nil {
		t.Fatal(e)
	}
	if b, e = h.canvasOwned(ctx, b.ID); e != nil {
		t.Fatal(e)
	}
	p, e := h.canvasPrepare(ctx, b, b.Cards[1].ID)
	if e != nil {
		t.Fatal(e)
	}
	return h, ctx, b, p, it
}

func TestCanvasPrepareSpillsInsteadOfSilentCut(t *testing.T) {
	body := spillDigits(30000)
	h, ctx, b, p, it := spillBoard(t, body)
	if !strings.Contains(p.Context, "## [ref:content/"+it.ID+"] Long article\n") || !strings.Contains(p.Context, body[:8000]) {
		t.Fatal("context lost header or first 8,000 characters")
	}
	if !strings.Contains(p.Context, "[… 22,000 more characters. Full text: ref:content/"+it.ID+"]") {
		t.Fatal("missing spill marker")
	}
	if len(p.Spilled) != 1 || p.Spilled[0].Kept != 8000 || !strings.Contains(strings.Join(p.Refs, ","), "content/"+it.ID) {
		t.Fatalf("spill record %#v %#v", p.Spilled, p.Refs)
	}
	// The review hash covers the final context: a source edit requires a fresh review.
	if _, e := h.d.DB.Exec(`UPDATE content SET body=? WHERE id=?`, "changed "+body, it.ID); e != nil {
		t.Fatal(e)
	}
	current, e := h.canvasPrepare(ctx, b, p.NodeID)
	if e != nil || current.ReviewHash == p.ReviewHash || current.GraphHash != p.GraphHash {
		t.Fatal("review hash ignored context change", e)
	}
}

func canvasSourceCall(t *testing.T, h *handlers, keyID string, in map[string]any) (map[string]any, error) {
	t.Helper()
	ctx, e := auth.WithAPIKeyActor(context.Background(), h.d, keyID)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(in)
	for _, tool := range h.d.MCP.Tools() {
		if tool.Name == "canvases.source" {
			if len(tool.Scopes) == 0 || tool.Scopes[0] != "canvas.read" {
				t.Fatal("canvases.source scope", tool.Scopes)
			}
			out, err := tool.Handler(ctx, raw)
			if err != nil {
				return nil, err
			}
			m, _ := out.(map[string]any)
			return m, nil
		}
	}
	t.Fatal("canvases.source not registered")
	return nil, nil
}

func TestCanvasSourceTool(t *testing.T) {
	for _, which := range []string{"full", "outside", "revoked", "private", "other_board"} {
		t.Run(which, func(t *testing.T) {
			body := spillDigits(30000)
			h, ctx, b, p, it := spillBoard(t, body)
			run, e := h.canvasEnqueue(ctx, p, "spill-source")
			if e != nil || run.Status != "waiting_agent" {
				t.Fatal(run, e)
			}
			in := map[string]any{"board_id": b.ID, "run_id": run.ID, "ref": "content/" + it.ID}
			switch which {
			case "outside":
				cs, _ := h.contentService()
				other, e := cs.Create(ctx, content.Input{Type: "post", Title: "Unselected", Body: "Not on board", Status: "draft", AuthorID: p.OwnerID})
				if e != nil {
					t.Fatal(e)
				}
				in["ref"] = "content/" + other.ID
			case "revoked":
				_, e = h.d.DB.Exec(`UPDATE admin_canvas_grants SET can_read=0 WHERE board_id=?`, b.ID)
			case "private":
				_, e = h.d.DB.Exec(`UPDATE content_types SET public=0 WHERE slug='post'`)
			case "other_board":
				id, err := h.canvasCreate(ctx, "Other")
				if err != nil {
					t.Fatal(err)
				}
				_, e = h.d.DB.Exec(`INSERT INTO admin_canvas_grants(board_id,key_id,can_read,can_append,can_run,node_ids) VALUES(?,?,1,1,1,?)`, id, p.KeyID, canvasEncode(p.Nodes))
				in["board_id"] = id
			}
			if e != nil {
				t.Fatal(e)
			}
			out, err := canvasSourceCall(t, h, p.KeyID, in)
			if which != "full" {
				if err == nil {
					t.Fatalf("%s ref served: %v", which, out)
				}
				if err != errCanvasConsent {
					t.Fatal("refusal must use the generic error", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if out["text"] != body || out["truncated"] != false || out["ref"] != "content/"+it.ID {
				t.Fatalf("full text not returned: %v %v", out["truncated"], out["ref"])
			}
		})
	}
}

func TestCanvasSpillSummary(t *testing.T) {
	p := canvasPrepared{KeyID: "k", Spilled: []canvasSpill{{Ref: "a", Kept: 8000, Total: 20000}, {Ref: "b", Kept: 6000, Total: 7000}}}
	s := canvasSpillSummary(p)
	if s["sources"] != 2 || s["characters"] != 13000 || s["on_request"] != true {
		t.Fatal(s)
	}
	p.AgentModel = &agentModelConfig{Mode: "managed"}
	if canvasSpillSummary(p)["on_request"] != false {
		t.Fatal("managed agent cannot fetch spilled text")
	}
	if canvasSpillSummary(canvasPrepared{})["on_request"] != false {
		t.Fatal("provider cannot fetch spilled text")
	}
}
