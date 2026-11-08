package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/auth"
)

const canvasSearchLimit = 20

type canvasSearchResult struct {
	BoardID    string `json:"board_id"`
	BoardTitle string `json:"board_title"`
	CardID     string `json:"card_id,omitempty"`
	Title      string `json:"title"`
	Snippet    string `json:"snippet,omitempty"`
	Href       string `json:"href"`
	score      int
}

func (h *handlers) canvasSearchHTTP(w http.ResponseWriter, r *http.Request) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) > 1 || len(query["q"]) > 1 || len(query) == 1 && query["q"] == nil {
		h.aiJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid"})
		return
	}
	raw := query.Get("q")
	if !canvasText(raw, 1000) {
		h.aiJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid"})
		return
	}
	normalized := normalizeCanvasSearch(raw)
	if utf8.RuneCountInString(normalized) > 160 {
		h.aiJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid"})
		return
	}
	items := []canvasSearchResult{}
	if strings.TrimSpace(raw) == "" || utf8.RuneCountInString(normalized) >= 2 {
		items, err = h.searchCanvases(r.Context(), normalized)
		if err != nil {
			h.canvasError(w, r, err)
			return
		}
	}
	h.aiJSON(w, http.StatusOK, map[string]any{"items": items})
}

// Search follows the same owner boundary as the canvas editor. Only fields the
// editor renders as plain text are indexed, never URLs, IDs, run documents,
// credentials, linked content bodies, grants or provider settings.
func (h *handlers) searchCanvases(ctx context.Context, query string) ([]canvasSearchResult, error) {
	out := []canvasSearchResult{}
	u := auth.UserFromContext(ctx)
	if u == nil {
		return out, nil
	}
	rows, err := h.d.DB.QueryContext(ctx, `SELECT id,title,document_json FROM admin_canvases WHERE owner_id=? ORDER BY updated_at DESC,id LIMIT 100`, u.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	terms := strings.Fields(query)
	for rows.Next() {
		var id, title, raw string
		if err = rows.Scan(&id, &title, &raw); err != nil {
			return nil, err
		}
		href := "/admin/canvases/" + url.PathEscape(id)
		boardText := normalizeCanvasSearch(title)
		if query == "" || canvasSearchContains(boardText, terms) {
			score := 1
			if boardText == query {
				score = 0
			}
			out = append(out, canvasSearchResult{BoardID: id, BoardTitle: title, Title: title, Href: href, score: score})
		}
		if query == "" {
			if len(out) == 8 {
				break
			}
			continue
		}
		var cards []canvasCard
		if err = json.Unmarshal([]byte(raw), &cards); err != nil {
			return nil, err
		}
		for _, card := range cards {
			if !canvasCardID.MatchString(card.ID) {
				continue
			}
			text := card.Body
			for _, item := range card.Items {
				text += "\n" + item.Text
			}
			cardTitle := normalizeCanvasSearch(card.Title)
			ownText := cardTitle + " " + normalizeCanvasSearch(text)
			if !canvasSearchContains(boardText+" "+ownText, terms) || !slices.ContainsFunc(terms, func(term string) bool { return strings.Contains(ownText, term) }) {
				continue
			}
			score := 4
			if cardTitle == query {
				score = 2
			} else if canvasSearchContains(boardText+" "+cardTitle, terms) {
				score = 3
			}
			out = append(out, canvasSearchResult{BoardID: id, BoardTitle: title, CardID: card.ID, Title: card.Title, Snippet: canvasSearchSnippet(text, terms), Href: href + "#card-" + card.ID, score: score})
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	// Stable ranking retains the database's newest-first ordering for ties.
	slices.SortStableFunc(out, func(a, b canvasSearchResult) int { return a.score - b.score })
	if len(out) > canvasSearchLimit {
		out = out[:canvasSearchLimit]
	}
	return out, nil
}
