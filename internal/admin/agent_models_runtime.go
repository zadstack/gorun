package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
)

func (p canvasPrepared) managedAgent() bool {
	return p.KeyID != "" && p.AgentModel != nil && p.AgentModel.Mode == "managed"
}

// Both dispatch and append use a fresh transactional authority snapshot.
func agentModelAuthorityTx(ctx context.Context, tx *sql.Tx, p canvasPrepared) (connectionRecord, error) {
	var conn connectionRecord
	if p.AgentModel == nil {
		return conn, nil
	}
	var raw string
	var current agentModelConfig
	e := tx.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key=?`, agentModelPrefix+p.KeyID).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) && *p.AgentModel == (agentModelConfig{Mode: "external"}) {
		return conn, nil
	}
	if e != nil || json.Unmarshal([]byte(raw), &current) != nil || current != *p.AgentModel {
		return conn, errCanvasConsent
	}
	if current.ConnectionID != "" {
		conn, e = agentModelConnectionTx(ctx, tx, current)
		if e != nil {
			return conn, errCanvasConsent
		}
	}
	if !p.managedAgent() {
		return conn, nil
	}
	var scopeRaw, nodeRaw, role string
	e = tx.QueryRowContext(ctx, `SELECT k.scopes_json,g.node_ids,u.role FROM api_keys k JOIN users u ON u.id=k.user_id JOIN admin_canvas_grants g ON g.key_id=k.id WHERE k.id=? AND k.user_id=? AND k.kind=? AND k.revoked_at='' AND (k.expires_at='' OR k.expires_at>?) AND u.status='active' AND g.board_id=? AND g.can_read=1 AND g.can_append=1 AND g.can_run=1`, p.KeyID, p.OwnerID, auth.KeyKindAgent, db.Now(), p.BoardID).Scan(&scopeRaw, &nodeRaw, &role)
	if e != nil {
		return conn, errCanvasConsent
	}
	var scopes, nodes []string
	if json.Unmarshal([]byte(scopeRaw), &scopes) != nil || json.Unmarshal([]byte(nodeRaw), &nodes) != nil {
		return conn, errCanvasConsent
	}
	for _, id := range p.Nodes {
		if !slices.Contains(nodes, id) {
			return conn, errCanvasConsent
		}
	}
	for _, scope := range []string{"canvas.read", "canvas.append", "canvas.run", "content.view", "content.edit"} {
		if scope != "content.edit" && !slices.Contains(scopes, "*") && !slices.Contains(scopes, scope) {
			return conn, errCanvasConsent
		}
		var n int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM role_permissions WHERE role=? AND permission IN ('*',?)`, role, scope).Scan(&n); e != nil || n == 0 {
			return conn, errCanvasConsent
		}
	}
	return conn, nil
}
func (h *handlers) agentModelRuntime(ctx context.Context, p canvasPrepared) (aiConfig, error) {
	var conn connectionRecord
	e := db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error { var e error; conn, e = agentModelAuthorityTx(ctx, tx, p); return e })
	if e != nil {
		return aiConfig{}, e
	}
	secret, e := h.connectionSecrets(conn)
	if e != nil {
		return aiConfig{}, errCanvasConsent
	}
	return aiConfig{Provider: conn.Provider, BaseURL: conn.Endpoint, Model: p.AgentModel.Model, APIKey: secret.Token, NamedConnection: true}, nil
}
