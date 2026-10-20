package admin

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/i18n"
)

type agentRecipeProfile struct{ Name, Kind string }
type agentRecipeTool struct{ Name, Connection string }
type agentRecipePage struct {
	CanManage, Preview                                                    bool
	Keys                                                                  []agentRoleKey
	KeyID, KeyName, Snapshot, Mode, Model, Error, DraftID, UnavailableKey string
	Profiles                                                              []agentRecipeProfile
	Tools                                                                 []agentRecipeTool
	Title, Instructions, Context, Destination                             string
}

// Read saved configuration only. This function never discovers tools, resolves
// hosts, decrypts credentials or contacts a provider. The digest is a stale-form
// guard, not execution consent; execution still uses the normal canvas review.
func recipeConfiguration(ctx context.Context, r agentBundleReader, owner, key string) (agentRecipePage, error) {
	p := agentRecipePage{KeyID: key}
	if !agentBundleOwnerCurrent(ctx, r, owner, key) {
		return p, errAgentBundle
	}
	b, e := currentAgentBundle(ctx, r, owner, key)
	if e != nil {
		return p, e
	}
	var scopes string
	if e = r.QueryRowContext(ctx, `SELECT name,scopes_json FROM api_keys WHERE id=? AND user_id=?`, key, owner).Scan(&p.KeyName, &scopes); e != nil {
		return p, e
	}
	m := agentModelConfig{Mode: "external"}
	var raw string
	e = r.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key=?`, agentModelPrefix+key).Scan(&raw)
	if e != nil && e != sql.ErrNoRows {
		return p, e
	}
	if e == nil && (json.Unmarshal([]byte(raw), &m) != nil || !validAgentModel(m)) {
		return p, errAgentBundle
	}
	p.Mode, p.Model = m.Mode, m.Model
	profiles := map[string]string{}
	readProfile := func(id, version, kind string) error {
		var text string
		var c connectionRecord
		if r.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key=?`, connectionPrefix+id).Scan(&text) != nil || json.Unmarshal([]byte(text), &c) != nil || c.ID != id || c.Version != version || c.Kind != kind || validateConnection(c) != nil {
			return errAgentBundle
		}
		p.Profiles = append(p.Profiles, agentRecipeProfile{Name: c.Name, Kind: c.Kind})
		profiles[id] = c.Name
		return nil
	}
	if m.ConnectionID != "" {
		if e = readProfile(m.ConnectionID, m.ConnectionVersion, "ai"); e != nil {
			return p, e
		}
	}
	for _, ref := range b.MCP {
		if e = readProfile(ref.ID, ref.Version, "mcp"); e != nil {
			return p, e
		}
	}
	if b.Tools != nil {
		if m.Mode != "managed" || b.Tools.ModelVersion != m.Version {
			return p, errManagedTools
		}
		for _, t := range b.Tools.Allow {
			p.Tools = append(p.Tools, agentRecipeTool{Name: t.Name, Connection: profiles[t.ConnectionID]})
		}
	}
	// Include the exact stored documents in the digest: even an operator edit that
	// forgets to bump a connection version makes an already open form stale.
	snapshot := []string{canvasEncode(b), canvasEncode(m), scopes}
	for _, ref := range b.MCP {
		var v string
		if r.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key=?`, connectionPrefix+ref.ID).Scan(&v) != nil {
			return p, errAgentBundle
		}
		snapshot = append(snapshot, v)
	}
	if m.ConnectionID != "" {
		var v string
		if r.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key=?`, connectionPrefix+m.ConnectionID).Scan(&v) != nil {
			return p, errAgentBundle
		}
		snapshot = append(snapshot, v)
	}
	sum := sha256.Sum256([]byte(canvasEncode(snapshot)))
	p.Snapshot = hex.EncodeToString(sum[:])
	return p, nil
}

func (h *handlers) agentRecipeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if !h.agentRolesAllowed(r) {
		http.Error(w, "Forbidden", 403)
		return
	}
	p := agentRecipePage{DraftID: db.NewID(), CanManage: auth.Can(r.Context(), PermAPIKeys) && auth.Can(r.Context(), "settings.manage"), Title: h.tr(r, "agentrecipes.name"), Instructions: h.tr(r, "agentrecipes.instructions_default"), Destination: h.tr(r, "agentrecipes.destination_default")}
	key := r.URL.Query().Get("agent")
	if r.Method == "POST" {
		if !p.CanManage {
			http.Error(w, "Forbidden", 403)
			return
		}
		// CSRF middleware may already have parsed the form. Bound both the
		// original length and cached parsed form, not just the remaining body.
		if r.ContentLength > 64<<10 {
			http.Error(w, "Invalid form", 400)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		if r.ParseForm() != nil || len(r.PostForm.Encode()) > 64<<10 {
			http.Error(w, "Invalid form", 400)
			return
		}
		key = r.PostForm.Get("agent")
	}
	code := 200
	if key != "" {
		if !p.CanManage {
			http.Error(w, "Forbidden", 403)
			return
		}
		cfg, e := recipeConfiguration(r.Context(), h.d.DB, auth.UserFromContext(r.Context()).ID, key)
		if e != nil {
			if _, ownErr := h.canvasAgentKey(r.Context(), key); ownErr != nil {
				http.NotFound(w, r)
				return
			}
			code = 409
			p.Error = "agentrecipes.unavailable"
			p.UnavailableKey = key
		} else {
			p.KeyID, p.KeyName, p.Snapshot, p.Mode, p.Model, p.Profiles, p.Tools = cfg.KeyID, cfg.KeyName, cfg.Snapshot, cfg.Mode, cfg.Model, cfg.Profiles, cfg.Tools
		}
	}
	if r.Method == "POST" && code == 200 {
		p.DraftID = r.PostForm.Get("draft_id")
		p.Title = strings.TrimSpace(r.PostForm.Get("title"))
		p.Instructions = strings.TrimSpace(r.PostForm.Get("instructions"))
		p.Context = strings.TrimSpace(r.PostForm.Get("context"))
		p.Destination = strings.TrimSpace(r.PostForm.Get("destination"))
		if key == "" || r.PostForm.Get("snapshot") != p.Snapshot {
			code = 409
			p.Error = "agentrecipes.stale"
		} else if !connectionIDPattern.MatchString(p.DraftID) || p.Title == "" || !canvasText(p.Title, 120) || p.Instructions == "" || !canvasText(p.Instructions, 4000) || !canvasText(p.Context, 4000) || p.Destination == "" || !canvasText(p.Destination, 120) {
			code = 422
			p.Error = "agentrecipes.invalid"
		} else if r.PostForm.Get("action") == "preview" {
			p.Preview = true
		} else if r.PostForm.Get("action") == "save" && r.PostForm.Get("reviewed") == "1" {
			id, e := h.saveAgentRecipe(r.Context(), p)
			if e != nil {
				code = 409
				p.Error = "agentrecipes.stale"
			} else {
				http.Redirect(w, r, "/admin/canvases/"+id, 303)
				return
			}
		} else {
			code = 422
			p.Error = "agentrecipes.invalid"
		}
	}
	if p.CanManage {
		keys, e := h.keys.List(r.Context())
		if e != nil {
			h.serverError(w, r, e)
			return
		}
		now := time.Now().UTC().Format(time.RFC3339)
		for _, k := range keys {
			if k.UserID == auth.UserFromContext(r.Context()).ID && k.Kind == auth.KeyKindAgent && k.RevokedAt == "" && (k.ExpiresAt == "" || k.ExpiresAt > now) {
				p.Keys = append(p.Keys, agentRoleKey{ID: k.ID, Name: k.Name})
			}
		}
	}
	h.tpl.RenderWith(w, r, code, "admin", "pages/agents/recipes.html", p)
}

func (h *handlers) saveAgentRecipe(ctx context.Context, p agentRecipePage) (string, error) {
	u := auth.UserFromContext(ctx)
	if u == nil || auth.ActorFromContext(ctx).Kind != "user" || !auth.Can(ctx, "content.edit") || !auth.Can(ctx, PermAPIKeys) || !auth.Can(ctx, "settings.manage") || !WorkspaceShowsAdvanced(ctx, h.d.Settings) {
		return "", errAgentBundle
	}
	if !connectionIDPattern.MatchString(p.DraftID) {
		return "", errCanvasInvalid
	}
	ids := make([]string, 4)
	for i := range ids {
		sum := sha256.Sum256([]byte(p.DraftID + string(rune('0'+i))))
		v := hex.EncodeToString(sum[:16])
		ids[i] = v[:8] + "-" + v[8:12] + "-" + v[12:16] + "-" + v[16:20] + "-" + v[20:]
	}

	cards := []canvasCard{{ID: ids[0], Kind: "prompt", Title: h.trLocale(ctx, "agentrecipes.instructions"), Body: p.Instructions, X: 40, Y: 40}, {ID: ids[1], Kind: "note", Title: h.trLocale(ctx, "agentrecipes.context"), Body: p.Context, X: 40, Y: 430}, {ID: ids[2], Kind: "agent", AgentID: p.KeyID, Title: p.Destination, X: 520, Y: 140}, {ID: ids[3], Kind: "note", Title: h.trLocale(ctx, "agentrecipes.next"), Body: h.trLocale(ctx, "agentrecipes.next_help"), X: 1000, Y: 140}}
	edges := []canvasEdge{{From: ids[0], To: ids[2]}, {From: ids[1], To: ids[2]}}
	if h.validateCanvas(ctx, p.Title, cards) != nil || validateCanvasEdges(cards, edges) != nil {
		return "", errCanvasInvalid
	}
	id, now := p.DraftID, db.Now()
	e := db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		cfg, e := recipeConfiguration(ctx, tx, u.ID, p.KeyID)
		if e != nil || cfg.Snapshot != p.Snapshot {
			return errAgentModelConflict
		}
		var n int
		if tx.QueryRowContext(ctx, `SELECT count(*) FROM users u WHERE u.id=? AND u.status='active' AND EXISTS(SELECT 1 FROM role_permissions WHERE role=u.role AND permission IN('*','content.edit')) AND NOT EXISTS(SELECT 1 FROM settings WHERE key='site.workspace' AND json_extract(value_json,'$')='simple')`, u.ID).Scan(&n) != nil || n != 1 {
			return errAgentBundle
		}
		res, e := tx.ExecContext(ctx, `INSERT INTO admin_canvases(id,owner_id,title,document_json,edges_json,version,created_at,updated_at) SELECT ?,?,?,?,?,1,?,? WHERE (SELECT count(*) FROM admin_canvases WHERE owner_id=?)<100 ON CONFLICT(id) DO NOTHING`, id, u.ID, p.Title, canvasEncode(cards), canvasEncode(edges), now, now, u.ID)
		if e != nil {
			return e
		}
		nrows, e := res.RowsAffected()
		if e != nil {
			return e
		}
		if nrows != 1 {
			var oldTitle, oldCards, oldEdges string
			if tx.QueryRowContext(ctx, `SELECT title,document_json,edges_json FROM admin_canvases WHERE id=? AND owner_id=?`, id, u.ID).Scan(&oldTitle, &oldCards, &oldEdges) == nil && oldTitle == p.Title && oldCards == canvasEncode(cards) && oldEdges == canvasEncode(edges) {
				return nil
			}
			return errCanvasLimit
		}
		h.d.Audit.RecordTx(ctx, tx, "agent.recipe.created", "canvas", id, map[string]string{"recipe": "updates-researcher", "agent_id": p.KeyID})
		return nil
	})
	return id, e
}

func (h *handlers) trLocale(ctx context.Context, key string) string {
	return h.d.I18n.T(i18n.Locale(ctx), key)
}
