package admin_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type canvasSearchItem struct {
	BoardID    string `json:"board_id"`
	BoardTitle string `json:"board_title"`
	CardID     string `json:"card_id"`
	Title      string `json:"title"`
	Snippet    string `json:"snippet"`
	Href       string `json:"href"`
}

func searchCanvases(t *testing.T, s *site, q string) []canvasSearchItem {
	t.Helper()
	r, body := s.do("GET", "/admin/canvases/search?q="+url.QueryEscape(q), nil)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("search %q: %d %s", q, r.StatusCode, body)
	}
	if !strings.Contains(r.Header.Get("Cache-Control"), "no-store") || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("search headers: %#v", r.Header)
	}
	var result struct {
		Items []canvasSearchItem `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &result); err != nil || result.Items == nil {
		t.Fatalf("search response: %s (%v)", body, err)
	}
	return result.Items
}

func saveSearchBoard(t *testing.T, s *site, title string, cards ...map[string]any) string {
	t.Helper()
	path := newCanvas(t, s)
	input := canvasInput(cards...)
	input["title"] = title
	if code, body := studioJSON(t, s, path+"/document", input, true); code != http.StatusOK {
		t.Fatalf("save search fixture: %d %#v", code, body)
	}
	return path
}

func TestCanvasSearchArabicContentRankingAndLinks(t *testing.T) {
	s := boot(t)
	s.login("canvas-search@example.com", "admin", "ar")
	cardID := "12345678-1234-4123-8123-123456789abc"
	text := "مقدمة " + strings.Repeat("تفاصيل ", 32) + "إِطْلَاق مَكْتَبَة ١٢٣ في القاهرة"
	path := saveSearchBoard(t, s, "خطة الحملة", map[string]any{"id": cardID, "kind": "note", "title": "ملاحظات الفريق", "body": text})
	items := searchCanvases(t, s, "اطلاق مكتبه 123")
	if len(items) != 1 || items[0].CardID != cardID || items[0].BoardID != strings.TrimPrefix(path, "/admin/canvases/") || items[0].Href != path+"#card-"+cardID || !strings.Contains(items[0].Snippet, "إِطْلَاق") || !strings.Contains(items[0].Snippet, "مَكْتَبَة") {
		t.Fatalf("Arabic result/snippet/link: %#v", items)
	}
	if got := searchCanvases(t, s, "الحمله اطلاق"); len(got) != 1 || got[0].CardID != cardID {
		t.Fatalf("board context should combine with one card: %#v", got)
	}
	checklistID := "22345678-1234-4123-8123-123456789abc"
	saveSearchBoard(t, s, "Checklist", map[string]any{"id": checklistID, "kind": "checklist", "items": []any{map[string]any{"text": "مُراجَعة الهُوِيّة ۴۵", "checked": false}}})
	if got := searchCanvases(t, s, "مراجعه الهويه 45"); len(got) != 1 || got[0].CardID != checklistID {
		t.Fatalf("checklist/Persian digits: %#v", got)
	}
	// An exact board name wins even when a more recently edited card contains it.
	exact := saveSearchBoard(t, s, "Launch")
	saveSearchBoard(t, s, "Campaign planning", map[string]any{"id": cardID, "kind": "note", "title": "Launch", "body": "launch in November"})
	got := searchCanvases(t, s, "LAUNCH")
	if len(got) != 2 || got[0].Href != exact || got[0].CardID != "" || got[1].CardID != cardID {
		t.Fatalf("exact title ranking: %#v", got)
	}
}

func TestCanvasSearchOwnerIsolationAndTextAllowlist(t *testing.T) {
	s := boot(t)
	owner := s.login("canvas-search-owner@example.com", "author", "en")
	cardID := "12345678-1234-4123-8123-123456789abc"
	path := saveSearchBoard(t, s, "Owner-only search board", map[string]any{"id": cardID, "kind": "link", "title": "Allowed title", "body": "Allowed visible body", "url": "https://example.org/URL_SENTINEL"})
	if got := searchCanvases(t, s, "visible body"); len(got) != 1 || got[0].Href != path+"#card-"+cardID {
		t.Fatalf("visible owner text: %#v", got)
	}
	if got := searchCanvases(t, s, "URL_SENTINEL"); len(got) != 0 {
		t.Fatalf("non-text field searched: %#v", got)
	}
	if got := searchCanvases(t, s, cardID); len(got) != 0 {
		t.Fatalf("card ID searched: %#v", got)
	}
	if _, body := s.do("GET", "/admin/canvases/search?q=visible", nil); strings.Contains(body, "URL_SENTINEL") {
		t.Fatalf("non-text field leaked in a matching response: %s", body)
	}
	s.login("canvas-search-other@example.com", "admin", "en")
	if got := searchCanvases(t, s, "Owner-only"); len(got) != 0 {
		t.Fatalf("foreign board title leaked: %#v", got)
	}
	if got := searchCanvases(t, s, "visible body"); len(got) != 0 {
		t.Fatalf("foreign card body leaked: %#v", got)
	}
	s.signIn(owner.Email)
	if _, err := s.app.Deps.DB.Exec(`DELETE FROM admin_canvases WHERE id=?`, strings.TrimPrefix(path, "/admin/canvases/")); err != nil {
		t.Fatal(err)
	}
	if got := searchCanvases(t, s, "visible body"); len(got) != 0 {
		t.Fatalf("deleted board retained in search: %#v", got)
	}
	s.c = newClient()
	r, body := s.do("GET", "/admin/canvases/search?q=visible", nil)
	if r.StatusCode != http.StatusSeeOther || !strings.HasPrefix(r.Header.Get("Location"), "/admin/login") || strings.Contains(body, "Allowed visible body") {
		t.Fatalf("anonymous search: %d %s", r.StatusCode, body)
	}
}

func TestCanvasSearchBoundsAndTermsStayWithinOneCard(t *testing.T) {
	s := boot(t)
	s.login("canvas-search-bounds@example.com", "admin", "en")
	saveSearchBoard(t, s, "Separate cards", map[string]any{"id": "12345678-1234-4123-8123-123456789abc", "kind": "note", "body": "UniqueAlpha"}, map[string]any{"id": "22345678-1234-4123-8123-123456789abc", "kind": "note", "body": "UniqueBeta"})
	if got := searchCanvases(t, s, "UniqueAlpha UniqueBeta"); len(got) != 0 {
		t.Fatalf("terms combined across cards: %#v", got)
	}
	for _, q := range []string{"a", "؟!!", "ـَ"} {
		if got := searchCanvases(t, s, q); len(got) != 0 {
			t.Fatalf("short query %q: %#v", q, got)
		}
	}
	for _, query := range []string{"q=one&q=two", "q[x]=value", "q=" + strings.Repeat("a", 161), "q=" + strings.Repeat("a", 1001), "q=%00", "q=%FF"} {
		r, body := s.do("GET", "/admin/canvases/search?"+query, nil)
		if r.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, `"error":"invalid"`) {
			t.Errorf("invalid query %q: %d %s", query, r.StatusCode, body)
		}
	}
	for i := range 23 {
		saveSearchBoard(t, s, fmt.Sprintf("Result limit %02d", i))
	}
	if got := searchCanvases(t, s, "Result limit"); len(got) != 20 {
		t.Fatalf("result bound: %d", len(got))
	}
	if got := searchCanvases(t, s, " "); len(got) != 8 {
		t.Fatalf("recent boards bound: %d", len(got))
	}
}
