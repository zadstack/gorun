package admin

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/mailer"
)

var inboundCommand = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`)
var inboundDelivery = regexp.MustCompile(`^[A-Za-z0-9_-]{16,100}$`)
var errInboundRate = errors.New("inbound: rate limit")

type canvasInbound struct {
	ID                                 string `json:"id"`
	NodeID                             string `json:"node_id"`
	AgentID                            string `json:"agent_id"`
	ProviderID                         string `json:"provider_id"`
	Endpoint                           string `json:"endpoint"`
	EndpointPath                       string `json:"endpoint_path"`
	Command                            string `json:"command"`
	Enabled                            bool   `json:"enabled"`
	Status                             string `json:"status"`
	Revision                           int64  `json:"revision"`
	Prompt                             string `json:"prompt"`
	Mode                               string `json:"execution_mode"`
	Model                              string `json:"model,omitempty"`
	CanManage                          bool   `json:"can_manage"`
	board, owner, key, secret, consent string
}

func (h *handlers) canvasInboundRoutes() {
	h.d.Router.Admin.Get("/canvases/{id}/cards/{node}/inbound", h.canvasInboundHTTP)
	h.d.Router.Admin.Post("/canvases/{id}/cards/{node}/inbound", h.canvasInboundHTTP)
	// Webhooks is intentionally outside browser-session/CSRF middleware. HMAC is the credential.
	h.d.Router.Webhooks.Post("/canvas/{endpoint}", h.canvasInboundReceive)
}
func (h *handlers) loadCanvasInbound(ctx context.Context, id string) (canvasInbound, error) {
	var x canvasInbound
	e := h.d.DB.QueryRowContext(ctx, `SELECT id,board_id,node_id,owner_id,key_id,command,secret,revision,enabled,status,consent_hash FROM admin_canvas_inbound WHERE id=?`, id).Scan(&x.ID, &x.board, &x.NodeID, &x.owner, &x.key, &x.Command, &x.secret, &x.Revision, &x.Enabled, &x.Status, &x.consent)
	return x, e
}
func (h *handlers) inboundMetadata(ctx context.Context, b *canvasBoard, node string) (canvasInbound, error) {
	x := canvasInbound{NodeID: node, CanManage: true, Status: "unconfigured"}
	found := false
	for _, c := range b.Cards {
		if c.ID == node && (c.Kind == "agent" || c.Kind == "provider") {
			found = true
			x.AgentID = c.AgentID
			x.ProviderID = c.ProviderID
		}
	}
	if !found {
		return x, errCanvasInvalid
	}
	var id string
	e := h.d.DB.QueryRowContext(ctx, `SELECT id FROM admin_canvas_inbound WHERE board_id=? AND node_id=?`, b.ID, node).Scan(&id)
	if e == nil {
		stored, err := h.loadCanvasInbound(ctx, id)
		if err != nil {
			return x, err
		}
		x.ID = stored.ID
		x.Command = stored.Command
		x.Enabled = stored.Enabled
		x.Status = stored.Status
		x.Revision = stored.Revision
	}
	if e != nil && e != sql.ErrNoRows {
		return x, e
	}
	if x.ID != "" {
		x.EndpointPath = "/webhooks/canvas/" + x.ID
		x.Endpoint = strings.TrimRight(h.d.Config.BaseURL, "/") + x.EndpointPath
	}
	x.Mode = "provider"
	if x.AgentID != "" {
		x.Mode = "external"
		if m, err := h.loadAgentModel(ctx, x.AgentID); err == nil {
			x.Mode = m.Mode
			x.Model = m.Model
		}
	}
	if p, err := h.canvasPrepare(ctx, b, node); err == nil {
		x.Prompt = p.Prompt
	}
	return x, nil
}
func (h *handlers) canvasInboundHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	b, e := h.canvasOwned(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	if !auth.Can(r.Context(), "content.edit") || auth.ActorFromContext(r.Context()).Kind != "user" {
		h.canvasOpsError(w, 403, "forbidden")
		return
	}
	node := chi.URLParam(r, "node")
	meta, e := h.inboundMetadata(r.Context(), b, node)
	if e != nil {
		h.canvasOpsError(w, 422, "invalid_node")
		return
	}
	if r.Method == "GET" {
		h.aiJSON(w, 200, map[string]any{"inbound": meta})
		return
	}
	if !auth.RecentlyAuthenticated(r.Context(), auth.ReauthWindow) {
		h.aiJSON(w, 403, map[string]string{"error": "reauth_required", "reauth_url": "/admin/reauth?next=/admin/canvases/" + b.ID})
		return
	}
	var in struct {
		Action      string `json:"action"`
		Command     string `json:"command"`
		ReviewToken string `json:"review_token"`
	}
	if studioRead(w, r, &in) != nil {
		h.canvasOpsError(w, 422, "invalid")
		return
	}
	ctx := r.Context()
	secret := ""
	switch in.Action {
	case "configure", "rotate":
		if in.Action == "configure" && !inboundCommand.MatchString(in.Command) {
			h.canvasOpsError(w, 422, "invalid_command")
			return
		}
		if in.Action == "rotate" && meta.ID == "" {
			h.canvasOpsError(w, 409, "unconfigured")
			return
		}
		if meta.ID == "" || in.Action == "rotate" {
			var raw [32]byte
			if _, e = rand.Read(raw[:]); e != nil {
				h.canvasOpsError(w, 500, "unavailable")
				return
			}
			secret = hex.EncodeToString(raw[:])
		}
		id := meta.ID
		if id == "" {
			id = db.NewID()
		}
		cipher := ""
		if secret != "" {
			cipher, e = mailer.EncryptSecret(h.d.Config.SecretKey, secret)
			if e != nil {
				h.canvasOpsError(w, 500, "unavailable")
				return
			}
		}
		command := in.Command
		if in.Action == "rotate" {
			command = meta.Command
		}
		_, e = h.d.DB.ExecContext(ctx, `INSERT INTO admin_canvas_inbound(id,board_id,node_id,owner_id,key_id,command,secret,created_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(board_id,node_id) DO UPDATE SET command=excluded.command,secret=CASE WHEN excluded.secret='' THEN admin_canvas_inbound.secret ELSE excluded.secret END,key_id=excluded.key_id,revision=admin_canvas_inbound.revision+1,enabled=0,status='disabled',consent_hash=''`, id, b.ID, node, auth.UserFromContext(ctx).ID, meta.AgentID, command, cipher, db.Now())
	case "disable":
		_, e = h.d.DB.ExecContext(ctx, `UPDATE admin_canvas_inbound SET enabled=0,status='disabled',revision=revision+1,consent_hash='' WHERE board_id=? AND node_id=?`, b.ID, node)
	case "review":
		if meta.ID == "" {
			h.canvasOpsError(w, 409, "unconfigured")
			return
		}
		p, err := h.canvasPrepare(ctx, b, node)
		if err != nil {
			h.canvasOpsError(w, 409, "consent_changed")
			return
		}
		p.InboundID = meta.ID
		p.InboundRevision = meta.Revision
		token, expires := db.NewID(), time.Now().Add(10*time.Minute).UTC().Format(time.RFC3339)
		result, err := h.d.DB.ExecContext(ctx, `INSERT INTO admin_canvas_reviews(id,board_id,owner_id,node_id,document,expires_at) SELECT ?,?,?,?,?,? WHERE (SELECT count(*) FROM admin_canvas_reviews WHERE board_id=? AND expires_at>?)<100`, token, b.ID, p.OwnerID, node, canvasEncode(p), expires, b.ID, db.Now())
		if err != nil {
			h.canvasOpsError(w, 500, "unavailable")
			return
		}
		n, err := result.RowsAffected()
		if err != nil || n != 1 {
			h.canvasOpsError(w, 429, "review_limit")
			return
		}
		h.aiJSON(w, 200, map[string]any{"inbound": meta, "review_token": token, "expires_at": expires, "board_version": b.Version, "node_id": node, "provider": p.Provider, "prompt": p.Prompt, "context": p.Context, "bytes": len(p.Context)})
		return
	case "enable":
		var raw string
		if h.d.DB.QueryRowContext(ctx, `SELECT document FROM admin_canvas_reviews WHERE id=? AND board_id=? AND owner_id=? AND node_id=? AND expires_at>?`, in.ReviewToken, b.ID, auth.UserFromContext(ctx).ID, node, db.Now()).Scan(&raw) != nil {
			h.canvasOpsError(w, 409, "review_expired")
			return
		}
		var reviewed canvasPrepared
		p, err := h.canvasPrepare(ctx, b, node)
		if err != nil || json.Unmarshal([]byte(raw), &reviewed) != nil || reviewed.InboundID != meta.ID || reviewed.InboundRevision != meta.Revision || reviewed.ReviewHash != p.ReviewHash || reviewed.Version != p.Version {
			h.canvasOpsError(w, 409, "consent_changed")
			return
		}
		e = db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
			var version int64
			if err := tx.QueryRowContext(ctx, `SELECT version FROM admin_canvases WHERE id=? AND owner_id=?`, b.ID, p.OwnerID).Scan(&version); err != nil || version != p.Version {
				return errCanvasConsent
			}
			result, err := tx.ExecContext(ctx, `UPDATE admin_canvas_inbound SET enabled=1,status='enabled',consent_hash=?,key_id=? WHERE id=? AND revision=? AND owner_id=?`, p.GraphHash, p.KeyID, meta.ID, meta.Revision, p.OwnerID)
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if n != 1 {
				return errCanvasConsent
			}
			return nil
		})
	default:
		h.canvasOpsError(w, 422, "invalid_action")
		return
	}
	if e != nil {
		h.canvasOpsError(w, 409, "consent_changed")
		return
	}
	meta, e = h.inboundMetadata(ctx, b, node)
	if e != nil {
		h.canvasOpsError(w, 500, "unavailable")
		return
	}
	h.d.Audit.Record(ctx, "canvas.inbound."+in.Action, "canvas", b.ID, map[string]string{"node_id": node})
	response := map[string]any{"inbound": meta}
	if secret != "" {
		response["secret"] = secret
	}
	h.aiJSON(w, 200, response)
}
func canvasInboundActiveTx(ctx context.Context, tx *sql.Tx, p canvasPrepared) bool {
	var n int
	return tx.QueryRowContext(ctx, `SELECT count(*) FROM admin_canvas_inbound WHERE id=? AND board_id=? AND node_id=? AND owner_id=? AND enabled=1 AND revision=? AND consent_hash=?`, p.InboundID, p.BoardID, p.NodeID, p.OwnerID, p.InboundRevision, p.GraphHash).Scan(&n) == nil && n == 1
}
func (h *handlers) canvasInboundActive(ctx context.Context, p canvasPrepared) bool {
	var n int
	return h.d.DB.QueryRowContext(ctx, `SELECT count(*) FROM admin_canvas_inbound WHERE id=? AND board_id=? AND node_id=? AND owner_id=? AND enabled=1 AND revision=? AND consent_hash=?`, p.InboundID, p.BoardID, p.NodeID, p.OwnerID, p.InboundRevision, p.GraphHash).Scan(&n) == nil && n == 1
}
func (h *handlers) inboundRateAllowed() bool {
	h.inboundRateMu.Lock()
	defer h.inboundRateMu.Unlock()
	if time.Since(h.inboundRateWindow) >= time.Minute {
		h.inboundRateWindow = time.Now()
		h.inboundRateCount = 0
	}
	h.inboundRateCount++
	return h.inboundRateCount <= 300
}
func (h *handlers) canvasInboundReceive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	fail := func(code int) { h.canvasOpsError(w, code, "not_accepted") }
	timestamp, delivery, signature := r.Header.Get("X-GoRun-Timestamp"), r.Header.Get("X-GoRun-Delivery"), r.Header.Get("X-GoRun-Signature")
	at, e := strconv.ParseInt(timestamp, 10, 64)
	if e != nil || !inboundDelivery.MatchString(delivery) || len(signature) != 71 || !strings.HasPrefix(signature, "sha256=") || time.Since(time.Unix(at, 0)) > 5*time.Minute || time.Until(time.Unix(at, 0)) > time.Minute {
		fail(401)
		return
	}
	body, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if e != nil {
		fail(413)
		return
	}
	endpoint, e := h.loadCanvasInbound(r.Context(), chi.URLParam(r, "endpoint"))
	if e != nil {
		fail(401)
		return
	}
	key, e := mailer.DecryptSecret(h.d.Config.SecretKey, endpoint.secret)
	if e != nil {
		fail(401)
		return
	}
	supplied, e := hex.DecodeString(signature[7:])
	if e != nil {
		fail(401)
		return
	}
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(timestamp + "." + delivery + "."))
	mac.Write(body)
	if !hmac.Equal(mac.Sum(nil), supplied) {
		fail(401)
		return
	}
	var in struct {
		Command string `json:"command"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	open, openErr := decoder.Token()
	field, fieldErr := decoder.Token()
	valueErr := decoder.Decode(&in.Command)
	close, closeErr := decoder.Token()
	if openErr != nil || fieldErr != nil || valueErr != nil || closeErr != nil || open != json.Delim('{') || field != "command" || close != json.Delim('}') || decoder.Decode(new(any)) != io.EOF || in.Command != endpoint.Command {
		fail(422)
		return
	}
	user, e := h.users.Get(r.Context(), endpoint.owner)
	if e != nil || user.Status != auth.StatusActive {
		fail(409)
		return
	}
	ctx := auth.WithUser(r.Context(), user)
	board, e := h.canvasOwned(ctx, endpoint.board)
	if e != nil {
		fail(409)
		return
	}
	prepared, e := h.canvasPrepare(ctx, board, endpoint.NodeID)
	if e != nil || prepared.GraphHash != endpoint.consent || !endpoint.Enabled {
		fail(409)
		return
	}
	prepared.InboundID = endpoint.ID
	prepared.InboundRevision = endpoint.Revision
	if !h.inboundRateAllowed() {
		fail(429)
		return
	}
	sum := sha256.Sum256(body)
	bodyHash := hex.EncodeToString(sum[:])
	duplicate := false
	status := ""
	e = db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		if !canvasInboundActiveTx(ctx, tx, prepared) {
			return errCanvasConsent
		}
		var previous string
		err := tx.QueryRowContext(ctx, `SELECT body_hash FROM admin_canvas_inbound_deliveries WHERE endpoint_id=? AND delivery_id=?`, endpoint.ID, delivery).Scan(&previous)
		if err == nil {
			if previous != bodyHash {
				return errCanvasConsent
			}
			duplicate = true
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM admin_canvas_inbound_deliveries WHERE endpoint_id=? AND created_at>?`, endpoint.ID, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)).Scan(&count); err != nil {
			return err
		}
		if count >= 10 {
			return errInboundRate
		}
		run, err := h.canvasEnqueueTx(ctx, tx, prepared, "inbound:"+endpoint.ID+":"+delivery)
		if err != nil {
			return err
		}
		status = run.Status
		_, err = tx.ExecContext(ctx, `INSERT INTO admin_canvas_inbound_deliveries(endpoint_id,delivery_id,body_hash,run_id,created_at) VALUES(?,?,?,?,?)`, endpoint.ID, delivery, bodyHash, run.ID, db.Now())
		if err == nil {
			h.d.Audit.RecordTx(ctx, tx, "canvas.inbound.accepted", "canvas", prepared.BoardID, map[string]string{"node_id": prepared.NodeID, "endpoint_id": endpoint.ID, "delivery_id": delivery, "run_id": run.ID})
		}
		return err
	})
	if errors.Is(e, errInboundRate) || errors.Is(e, errCanvasLimit) {
		fail(429)
		return
	}
	if e != nil {
		fail(409)
		return
	}
	if duplicate {
		h.aiJSON(w, 200, map[string]any{"accepted": true, "duplicate": true})
		return
	}
	h.aiJSON(w, 202, map[string]any{"accepted": true, "status": status})
}
