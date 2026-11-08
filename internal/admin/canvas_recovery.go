package admin

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
)

// A recovered copy contains private text and layout only. Browser storage must
// never restore consent, execution identities or access to referenced objects.
func (h *handlers) canvasRecoveryCopyHTTP(w http.ResponseWriter, r *http.Request) {
	sourceID := chi.URLParam(r, "id")
	if _, err := h.canvasOwned(r.Context(), sourceID); err != nil {
		h.canvasError(w, r, err)
		return
	}
	var in struct {
		Title string       `json:"title"`
		Cards []canvasCard `json:"cards"`
		Edges []canvasEdge `json:"edges"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || dec.Decode(new(any)) != io.EOF || in.Cards == nil || in.Edges == nil || len(in.Cards) > 100 || len(in.Edges) > 200 {
		h.canvasOpsError(w, http.StatusUnprocessableEntity, "invalid")
		return
	}
	cards, err := canvasRecoveryCards(in.Cards)
	if err != nil {
		if errors.Is(err, errCanvasInvalid) {
			h.canvasOpsError(w, http.StatusUnprocessableEntity, "invalid")
		} else {
			h.canvasError(w, r, err)
		}
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	if h.validateCanvas(r.Context(), in.Title, cards) != nil {
		h.canvasOpsError(w, http.StatusUnprocessableEntity, "invalid")
		return
	}
	owner := auth.UserFromContext(r.Context()).ID
	id, now := db.NewID(), db.Now()
	err = db.InTx(r.Context(), h.d.DB, func(tx *sql.Tx) error {
		// Recheck ownership while holding the write lock: a deleted or moved
		// source cannot authorize a later insert after the initial read.
		var source string
		if err := tx.QueryRowContext(r.Context(), `SELECT id FROM admin_canvases WHERE id=? AND owner_id=?`, sourceID, owner).Scan(&source); err != nil {
			return err
		}
		result, err := tx.ExecContext(r.Context(), `INSERT INTO admin_canvases(id,owner_id,title,document_json,edges_json,version,created_at,updated_at) SELECT ?,?,?,?,'[]',1,?,? WHERE (SELECT count(*) FROM admin_canvases WHERE owner_id=?)<100`, id, owner, in.Title, canvasEncode(cards), now, now, owner)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return errCanvasLimit
		}
		h.d.Audit.RecordTx(r.Context(), tx, "canvas.recovery_copy", "canvas", id, map[string]string{"source_id": sourceID})
		return nil
	})
	if errors.Is(err, errCanvasLimit) {
		h.canvasOpsError(w, http.StatusUnprocessableEntity, "board_limit")
		return
	}
	if err != nil {
		h.canvasError(w, r, err)
		return
	}
	h.aiJSON(w, http.StatusCreated, map[string]string{"id": id, "href": "/admin/canvases/" + id})
}

func canvasRecoveryCards(input []canvasCard) ([]canvasCard, error) {
	cards := make([]canvasCard, 0, len(input))
	for _, old := range input {
		id, err := canvasUUID()
		if err != nil {
			return nil, err
		}
		card := canvasCard{ID: id, Kind: old.Kind, Title: old.Title, Body: old.Body, X: old.X, Y: old.Y}
		switch old.Kind {
		case "note", "prompt":
		case "checklist":
			card.Items = old.Items
		case "link":
			card.URL = old.URL
		case "result", "agent", "provider", "source", "content", "widget", "schedule":
			card.Kind = "note"
		default:
			return nil, errCanvasInvalid
		}
		cards = append(cards, card)
	}
	return cards, nil
}
