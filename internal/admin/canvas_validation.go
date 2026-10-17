package admin

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/auth"
)

var canvasCardID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var errCanvasInvalid = errors.New("invalid canvas document")

type canvasReference struct {
	Title string `json:"title"`
	Href  string `json:"href"`
}

func canvasText(s string, max int) bool {
	return utf8.ValidString(s) && len(s) <= max && !strings.ContainsRune(s, 0)
}
func canvasURL(s string) bool {
	if s == "" || len(s) > 2048 || strings.Contains(s, "\\") || strings.IndexFunc(s, unicode.IsControl) >= 0 || strings.TrimSpace(s) != s {
		return false
	}
	u, e := url.Parse(s)
	if e != nil || u.User != nil {
		return false
	}
	if strings.HasPrefix(s, "/") {
		return !strings.HasPrefix(s, "//") && u.Host == "" && u.Scheme == ""
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != ""
}
func (h *handlers) canvasReference(ctx context.Context, id string) (canvasReference, bool) {
	u := auth.UserFromContext(ctx)
	if u == nil || !auth.Can(ctx, "content.view") || !connectionIDPattern.MatchString(id) {
		return canvasReference{}, false
	}
	cs, ok := h.contentService()
	if !ok {
		return canvasReference{}, false
	}
	it, e := cs.Get(ctx, id)
	if e != nil || it.Status == "trash" || ((u.Role == "author" || u.Role == "contributor") && it.AuthorID != u.ID) {
		return canvasReference{}, false
	}
	typ, e := cs.GetType(ctx, it.Type)
	if e != nil || !typ.Public {
		return canvasReference{}, false
	}
	return canvasReference{it.Title, "/admin/content/" + url.PathEscape(it.Type) + "/" + it.ID}, true
}
func (h *handlers) validateCanvas(ctx context.Context, title string, cards []canvasCard) error {
	if !canvasText(title, 120) || strings.TrimSpace(title) == "" || len(cards) > 100 {
		return errCanvasInvalid
	}
	seen := map[string]bool{}
	for _, c := range cards {
		if !canvasCardID.MatchString(c.ID) || seen[c.ID] || !canvasText(c.Title, 120) || !canvasText(c.Body, 4000) || c.X < 0 || c.Y < 0 || c.X > 10000 || c.Y > 10000 {
			return errCanvasInvalid
		}
		seen[c.ID] = true
		if c.Kind != "provider" && (c.GenerationKind != "" || c.PresetID != "") || c.Kind != "result" && c.RunID != "" {
			return errCanvasInvalid
		}
		if c.Kind != "agent" && c.AgentID != "" {
			return errCanvasInvalid
		}
		if c.Kind != "source" && (c.SourceType != "" || c.SourceLimit != 0 || len(c.SourceIDs) != 0) || c.Kind != "provider" && c.ProviderID != "" || c.Kind != "schedule" && c.Interval != "" {
			return errCanvasInvalid
		}
		if c.Kind != "link" && c.URL != "" || c.Kind != "content" && c.ContentID != "" || c.Kind != "widget" && c.WidgetID != "" || c.Kind != "checklist" && len(c.Items) != 0 {
			return errCanvasInvalid
		}
		switch c.Kind {
		case "note", "prompt":
		case "source":
			if c.SourceLimit < 1 || c.SourceLimit > 5 || len(c.SourceIDs) > 5 {
				return errCanvasInvalid
			}
			if c.SourceType == "" {
				if len(c.SourceIDs) > 0 {
					return errCanvasInvalid
				}
				break
			}
			cs, ok := h.contentService()
			if !ok {
				return errCanvasInvalid
			}
			typ, e := cs.GetType(ctx, c.SourceType)
			if e != nil || !typ.Public {
				return errCanvasInvalid
			}
			for _, id := range c.SourceIDs {
				if _, ok := h.canvasReference(ctx, id); !ok {
					return errCanvasInvalid
				}
				it, e := cs.Get(ctx, id)
				if e != nil || it.Type != c.SourceType {
					return errCanvasInvalid
				}
			}
		case "agent":
			if c.AgentID != "" && !connectionIDPattern.MatchString(c.AgentID) {
				return errCanvasInvalid
			}
		case "result":
			if !connectionIDPattern.MatchString(c.RunID) {
				return errCanvasInvalid
			}
			var count int
			if h.d.DB.QueryRowContext(ctx, `SELECT count(*) FROM admin_canvas_runs WHERE id=? AND owner_id=? AND result_card_id=? AND status='succeeded'`, c.RunID, auth.UserFromContext(ctx).ID, c.ID).Scan(&count) != nil || count != 1 {
				return errCanvasInvalid
			}
		case "provider":
			if c.GenerationKind != "" && !studioKind(c.GenerationKind) || c.PresetID != "" && !studioID.MatchString(c.PresetID) {
				return errCanvasInvalid
			}
			if c.ProviderID != "" && !studioID.MatchString(c.ProviderID) {
				return errCanvasInvalid
			}
		case "schedule":
			if canvasInterval(c.Interval) == 0 {
				return errCanvasInvalid
			}
		case "link":
			if !canvasURL(c.URL) {
				return errCanvasInvalid
			}
		case "content":
			if c.ContentID == "" {
				break
			}
			if _, ok := h.canvasReference(ctx, c.ContentID); !ok {
				return errCanvasInvalid
			}
		case "widget":
			if !h.canvasWidgetAllowed(ctx, c.WidgetID) {
				return errCanvasInvalid
			}
		case "checklist":
			if len(c.Items) > 30 {
				return errCanvasInvalid
			}
			for _, i := range c.Items {
				if !canvasText(i.Text, 300) || strings.TrimSpace(i.Text) == "" {
					return errCanvasInvalid
				}
			}
		default:
			return errCanvasInvalid
		}
	}
	return nil
}
