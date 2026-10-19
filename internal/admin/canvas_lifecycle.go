package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
)

type canvasTemplate struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

func (h *handlers) canvasLifecycleRoutes() {
	r := h.d.Router.Admin
	r.Post("/canvases/{id}/cards/{card}/draft", h.canvasDraftHTTP)
	r.Get("/canvases/{id}/draft-options", h.canvasDraftOptionsHTTP)
	r.Post("/canvases/{id}/template", h.canvasTemplateSaveHTTP)
	r.Get("/canvas-templates", h.canvasTemplatesHTTP)
	r.Post("/canvas-templates/{template}/reuse", h.canvasTemplateReuseHTTP)
	r.Delete("/canvas-templates/{template}", h.canvasTemplateDeleteHTTP)
}

// Structural templates intentionally omit free text, URLs and execution state:
// prompts and notes may themselves contain credentials or customer information.
func canvasTemplateDocument(b *canvasBoard) ([]canvasCard, []canvasEdge, error) {
	cards := []canvasCard{}
	ids := map[string]string{}
	for _, old := range b.Cards {
		id, err := canvasUUID()
		if err != nil {
			return nil, nil, err
		}
		c := canvasCard{ID: id, Kind: old.Kind, X: old.X, Y: old.Y, Title: old.Kind}
		switch old.Kind {
		case "result", "link", "widget":
			c.Kind = "note"
			c.Title = "note"
		case "source":
			c.SourceLimit = 3
		case "provider":
			c.GenerationKind = old.GenerationKind
		case "schedule":
			c.Interval = old.Interval
		case "checklist":
			c.Items = []canvasCheck{}
		}
		ids[old.ID] = c.ID
		cards = append(cards, c)
	}
	edges := []canvasEdge{}
	for _, e := range b.Edges {
		if ids[e.From] != "" && ids[e.To] != "" {
			edges = append(edges, canvasEdge{ids[e.From], ids[e.To]})
		}
	}
	return cards, edges, nil
}

func (h *handlers) canvasTemplatesHTTP(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		h.canvasError(w, r, sql.ErrNoRows)
		return
	}
	rows, e := h.d.DB.QueryContext(r.Context(), `SELECT id,title FROM admin_canvas_templates WHERE owner_id=? ORDER BY created_at DESC,id LIMIT 100`, u.ID)
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	defer rows.Close()
	out := []canvasTemplate{}
	for rows.Next() {
		var t canvasTemplate
		if e = rows.Scan(&t.ID, &t.Title); e != nil {
			h.canvasError(w, r, e)
			return
		}
		out = append(out, t)
	}
	if e = rows.Err(); e != nil {
		h.canvasError(w, r, e)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	h.aiJSON(w, 200, out)
}
func (h *handlers) canvasTemplateSaveHTTP(w http.ResponseWriter, r *http.Request) {
	b, e := h.canvasOwned(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if e = r.ParseForm(); e != nil {
		h.canvasOpsError(w, 422, "invalid")
		return
	}
	title := strings.TrimSpace(r.PostForm.Get("title"))
	if title == "" || !canvasText(title, 120) {
		h.canvasOpsError(w, 422, "invalid")
		return
	}
	cards, edges, err := canvasTemplateDocument(b)
	if err != nil {
		h.canvasError(w, r, err)
		return
	}
	for i := range cards {
		key := "admin.canvas." + cards[i].Kind
		if cards[i].Kind == "source" {
			key = "canvasops.ops_source"
		} else if cards[i].Kind == "prompt" || cards[i].Kind == "agent" || cards[i].Kind == "provider" || cards[i].Kind == "schedule" {
			key = "canvasops." + cards[i].Kind
		}
		cards[i].Title = h.tr(r, key)
	}
	id := db.NewID()
	u := auth.UserFromContext(r.Context())
	res, e := h.d.DB.ExecContext(r.Context(), `INSERT INTO admin_canvas_templates(id,owner_id,title,document_json,edges_json,created_at) SELECT ?,?,?,?,?,? WHERE (SELECT count(*) FROM admin_canvas_templates WHERE owner_id=?)<100`, id, u.ID, title, canvasEncode(cards), canvasEncode(edges), db.Now(), u.ID)
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	n, e := res.RowsAffected()
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	if n != 1 {
		h.canvasOpsError(w, 422, "limit")
		return
	}
	h.d.Audit.Record(r.Context(), "canvas.template_save", "canvas_template", id, nil)
	h.aiJSON(w, 201, canvasTemplate{id, title})
}
func (h *handlers) canvasTemplateReuseHTTP(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		h.canvasError(w, r, sql.ErrNoRows)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if e := r.ParseForm(); e != nil {
		h.canvasOpsError(w, 422, "invalid")
		return
	}
	title := strings.TrimSpace(r.PostForm.Get("title"))
	if title == "" || !canvasText(title, 120) {
		h.canvasOpsError(w, 422, "invalid")
		return
	}
	id := db.NewID()
	now := db.Now()
	e := db.InTx(r.Context(), h.d.DB, func(tx *sql.Tx) error {
		var raw, edgeRaw string
		if e := tx.QueryRowContext(r.Context(), `SELECT document_json,edges_json FROM admin_canvas_templates WHERE id=? AND owner_id=?`, chi.URLParam(r, "template"), u.ID).Scan(&raw, &edgeRaw); e != nil {
			return e
		}
		b := &canvasBoard{}
		if e := json.Unmarshal([]byte(raw), &b.Cards); e != nil {
			return e
		}
		if e := json.Unmarshal([]byte(edgeRaw), &b.Edges); e != nil {
			return e
		}
		cards, edges, err := canvasTemplateDocument(b)
		if err != nil {
			return err
		}
		for i := range cards {
			key := "admin.canvas." + cards[i].Kind
			if cards[i].Kind == "source" {
				key = "canvasops.ops_source"
			} else if cards[i].Kind == "prompt" || cards[i].Kind == "agent" || cards[i].Kind == "provider" || cards[i].Kind == "schedule" {
				key = "canvasops." + cards[i].Kind
			}
			cards[i].Title = h.tr(r, key)
		}
		if e := validateCanvasEdges(cards, edges); e != nil {
			return e
		}
		if e := h.validateCanvas(r.Context(), title, cards); e != nil {
			return e
		}
		res, e := tx.ExecContext(r.Context(), `INSERT INTO admin_canvases(id,owner_id,title,document_json,edges_json,version,created_at,updated_at) SELECT ?,?,?,?,?,1,?,? WHERE (SELECT count(*) FROM admin_canvases WHERE owner_id=?)<100`, id, u.ID, title, canvasEncode(cards), canvasEncode(edges), now, now, u.ID)
		if e != nil {
			return e
		}
		n, e := res.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return errCanvasLimit
		}
		h.d.Audit.RecordTx(r.Context(), tx, "canvas.template_reuse", "canvas", id, map[string]string{"template_id": chi.URLParam(r, "template")})
		return nil
	})
	if errors.Is(e, errCanvasLimit) || errors.Is(e, errCanvasInvalid) {
		h.canvasOpsError(w, 422, "invalid")
		return
	}
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	h.aiJSON(w, 201, map[string]string{"href": "/admin/canvases/" + id})
}
func (h *handlers) canvasTemplateDeleteHTTP(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		h.canvasError(w, r, sql.ErrNoRows)
		return
	}
	res, e := h.d.DB.ExecContext(r.Context(), `DELETE FROM admin_canvas_templates WHERE id=? AND owner_id=?`, chi.URLParam(r, "template"), u.ID)
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	n, e := res.RowsAffected()
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	if n != 1 {
		h.canvasError(w, r, sql.ErrNoRows)
		return
	}
	h.d.Audit.Record(r.Context(), "canvas.template_delete", "canvas_template", chi.URLParam(r, "template"), nil)
	h.aiJSON(w, 200, map[string]bool{"ok": true})
}

func (h *handlers) canvasDraftHTTP(w http.ResponseWriter, r *http.Request) {
	if !auth.Can(r.Context(), "content.create") {
		h.canvasOpsError(w, 403, "forbidden")
		return
	}
	b, e := h.canvasOwned(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	var card *canvasCard
	for i := range b.Cards {
		if b.Cards[i].ID == chi.URLParam(r, "card") {
			card = &b.Cards[i]
			break
		}
	}
	if card == nil {
		h.canvasError(w, r, sql.ErrNoRows)
		return
	}
	if card.Kind != "result" && card.Kind != "note" || strings.TrimSpace(card.Body) == "" {
		h.canvasOpsError(w, 422, "invalid")
		return
	}
	// Only text results or attributed agent notes qualify; media run captions do not.
	runID := card.RunID
	var actorKind, actorName, provenanceRun string
	e = h.d.DB.QueryRowContext(r.Context(), `SELECT actor_kind,actor_name,run_id FROM admin_canvas_provenance WHERE board_id=? AND card_id=?`, b.ID, card.ID).Scan(&actorKind, &actorName, &provenanceRun)
	if e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			h.canvasOpsError(w, 422, "invalid")
		} else {
			h.canvasError(w, r, e)
		}
		return
	}
	if runID == "" {
		runID = provenanceRun
	}
	var raw string
	e = h.d.DB.QueryRowContext(r.Context(), `SELECT document FROM admin_canvas_runs WHERE id=? AND board_id=? AND owner_id=? AND status='succeeded' AND result_card_id=?`, runID, b.ID, auth.UserFromContext(r.Context()).ID, card.ID).Scan(&raw)
	if e != nil {
		h.canvasOpsError(w, 422, "invalid")
		return
	}
	var p canvasPrepared
	if json.Unmarshal([]byte(raw), &p) != nil || p.MediaFile != "" || p.MediaMIME != "" || p.Candidate.Input.Kind != "" && p.Candidate.Input.Kind != "text" {
		h.canvasOpsError(w, 422, "invalid")
		return
	}
	cs, ok := h.contentService()
	if !ok {
		h.canvasOpsError(w, 503, "unavailable")
		return
	}
	creator, ok := cs.(content.DraftCreator)
	if !ok {
		h.canvasOpsError(w, 503, "unavailable")
		return
	}
	// Retry returns the original draft without modifying it, even after manual edits.
	if item, e := h.canvasExistingDraft(r.Context(), b.ID, card.ID); e == nil {
		h.canvasDraftResponse(w, item)
		return
	} else if !errors.Is(e, sql.ErrNoRows) {
		h.canvasError(w, r, e)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if e = r.ParseForm(); e != nil {
		h.canvasOpsError(w, 422, "invalid")
		return
	}
	typ := r.PostForm.Get("type")
	t, e := cs.GetType(r.Context(), typ)
	if e != nil || !t.Public {
		h.canvasOpsError(w, 422, "invalid")
		return
	}
	body := "<p>" + strings.ReplaceAll(html.EscapeString(card.Body), "\n", "<br>") + "</p>"
	title := strings.TrimSpace(card.Title)
	if title == "" {
		title = h.tr(r, "canvaslife.new_draft")
	}
	source := map[string]string{"board_id": b.ID, "card_id": card.ID, "run_id": runID, "actor_kind": actorKind, "actor_name": actorName, "text": card.Body}
	item, e := creator.CreateDraftWith(r.Context(), content.Input{Type: typ, Title: title, Body: body}, func(tx *sql.Tx, it *content.Item) error {
		var version int64
		if e := tx.QueryRowContext(r.Context(), `SELECT version FROM admin_canvases WHERE id=? AND owner_id=?`, b.ID, auth.UserFromContext(r.Context()).ID).Scan(&version); e != nil {
			return e
		}
		if version != b.Version {
			return errCanvasConsent
		}
		_, e := tx.ExecContext(r.Context(), `INSERT INTO admin_canvas_drafts(board_id,card_id,content_id,owner_id,run_id,source_json,created_at) VALUES(?,?,?,?,?,?,?)`, b.ID, card.ID, it.ID, auth.UserFromContext(r.Context()).ID, runID, canvasEncode(source), db.Now())
		if e != nil {
			return e
		}
		h.d.Audit.RecordTx(r.Context(), tx, "canvas.create_draft", "content", it.ID, map[string]string{"board_id": b.ID, "card_id": card.ID, "run_id": runID})
		return nil
	})
	if e != nil {
		if prior, lookup := h.canvasExistingDraft(r.Context(), b.ID, card.ID); lookup == nil {
			h.canvasDraftResponse(w, prior)
			return
		}
		if errors.Is(e, errCanvasConsent) {
			h.canvasOpsError(w, 409, "conflict")
			return
		}
		h.canvasOpsError(w, 422, "draft_failed")
		return
	}
	h.canvasDraftResponse(w, item)
}
func (h *handlers) canvasExistingDraft(ctx context.Context, board, card string) (*content.Item, error) {
	var id string
	e := h.d.DB.QueryRowContext(ctx, `SELECT content_id FROM admin_canvas_drafts WHERE board_id=? AND card_id=? AND owner_id=?`, board, card, auth.UserFromContext(ctx).ID).Scan(&id)
	if e != nil {
		return nil, e
	}
	cs, ok := h.contentService()
	if !ok {
		return nil, sql.ErrNoRows
	}
	item, e := cs.Get(ctx, id)
	if e != nil {
		return nil, e
	}
	if (auth.UserFromContext(ctx).Role == "author" || auth.UserFromContext(ctx).Role == "contributor") && item.AuthorID != auth.UserFromContext(ctx).ID {
		return nil, sql.ErrNoRows
	}
	return item, nil
}
func (h *handlers) canvasDraftResponse(w http.ResponseWriter, item *content.Item) {
	h.aiJSON(w, 200, map[string]string{"id": item.ID, "href": "/admin/content/" + item.Type + "/" + item.ID})
}

func (h *handlers) canvasDraftOptionsHTTP(w http.ResponseWriter, r *http.Request) {
	if !auth.Can(r.Context(), "content.create") {
		h.canvasOpsError(w, 403, "forbidden")
		return
	}
	if _, e := h.canvasOwned(r.Context(), chi.URLParam(r, "id")); e != nil {
		h.canvasError(w, r, e)
		return
	}
	cs, ok := h.contentService()
	if !ok {
		h.canvasOpsError(w, 503, "unavailable")
		return
	}
	types, e := cs.Types(r.Context())
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	out := []map[string]string{}
	for _, typ := range types {
		if typ.Public {
			out = append(out, map[string]string{"id": typ.Slug, "title": typ.Slug})
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	h.aiJSON(w, 200, out)
}
