package admin

import (
	"context"
	"html/template"
	"net/http"
	"strings"
)

type canvasPreviewCard struct {
	Title, Body, KindKey string
	Left, Top            int
}
type canvasPreview struct {
	ID, Title, UpdatedAt string
	Count                int
	Cards                []canvasPreviewCard
}

func (h *handlers) canvasPreview(r *http.Request, b *canvasBoard) canvasPreview {
	out := canvasPreview{ID: b.ID, Title: b.Title, UpdatedAt: b.UpdatedAt, Count: len(b.Cards)}
	maxX, maxY := 360, 360
	for _, card := range b.Cards {
		maxX = max(maxX, card.X+336)
		maxY = max(maxY, card.Y+336)
	}
	for _, card := range b.Cards {
		if len(out.Cards) == 6 {
			break
		}
		body := card.Body
		if card.Kind == "checklist" {
			items := []string{}
			for _, item := range card.Items {
				if len(items) == 2 {
					break
				}
				prefix := "☐ "
				if item.Checked {
					prefix = "☑ "
				}
				items = append(items, prefix+item.Text)
			}
			body = strings.Join(items, " · ")
		}
		if card.Kind == "content" {
			body = h.tr(r, "admin.canvas.unavailable")
			if ref, ok := h.canvasReference(r.Context(), card.ContentID); ok {
				body = ref.Title
			}
		}
		if card.Kind == "widget" {
			body = h.tr(r, "admin.canvas.widget")
		}
		runes := []rune(body)
		if len(runes) > 100 {
			body = string(runes[:100]) + "…"
		}
		out.Cards = append(out.Cards, canvasPreviewCard{Title: card.Title, Body: body, KindKey: "admin.canvas." + card.Kind, Left: card.X * 70 / maxX, Top: card.Y * 65 / maxY})
	}
	return out
}
func (h *handlers) widgetCanvasGallery(ctx context.Context, r *http.Request) (template.HTML, error) {
	if !WorkspaceShowsAdvanced(ctx, h.d.Settings) {
		return "", nil
	}
	boards, err := h.canvasBoards(ctx)
	if err != nil {
		return "", err
	}
	previews := []canvasPreview{}
	for _, board := range boards {
		if len(previews) == 6 {
			break
		}
		full, err := h.canvasOwned(ctx, board.ID)
		if err != nil {
			return "", err
		}
		previews = append(previews, h.canvasPreview(r, full))
	}
	activity, err := h.canvasActivity(ctx)
	if err != nil {
		return "", err
	}
	return h.fragment(r, "pages/system/widgets/canvas_gallery.html", struct {
		Boards   []canvasPreview
		Activity canvasActivityPage
	}{previews, activity})
}
