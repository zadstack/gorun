package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
)

const agentModelPrefix = "agents.model."

var (
	errAgentModelInvalid   = errors.New("agent model: invalid assignment")
	errAgentModelConflict  = errors.New("agent model: configuration changed")
	errAgentModelForbidden = errors.New("agent model: forbidden")
)

type agentModelConfig struct {
	Mode              string `json:"mode"`
	ConnectionID      string `json:"connection_id"`
	ConnectionVersion string `json:"connection_version"`
	Model             string `json:"model"`
	Version           string `json:"version"`
}

func (h *handlers) loadAgentModel(ctx context.Context, id string) (agentModelConfig, error) {
	c := agentModelConfig{Mode: "external"}
	if !connectionIDPattern.MatchString(id) {
		return c, errAgentModelInvalid
	}
	var raw string
	e := h.d.DB.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key=?`, agentModelPrefix+id).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return c, nil
	}
	if e != nil {
		return c, e
	}
	if json.Unmarshal([]byte(raw), &c) != nil || !validAgentModel(c) {
		return c, errAgentModelInvalid
	}
	return c, nil
}
func validAgentModel(c agentModelConfig) bool {
	if c.Mode != "external" && c.Mode != "managed" {
		return false
	}
	if c.ConnectionID == "" {
		return c.Mode == "external" && c.ConnectionVersion == "" && c.Model == ""
	}
	return connectionIDPattern.MatchString(c.ConnectionID) && connectionIDPattern.MatchString(c.ConnectionVersion) && aiModelRe.MatchString(c.Model)
}
func (h *handlers) saveAgentModel(ctx context.Context, id, expected string, c agentModelConfig) (agentModelConfig, error) {
	if auth.ActorFromContext(ctx).Kind != "user" || !auth.Can(ctx, PermAPIKeys) || !auth.Can(ctx, "settings.manage") {
		return c, errAgentModelForbidden
	}
	if !connectionIDPattern.MatchString(id) || !validAgentModel(c) {
		return c, errAgentModelInvalid
	}
	c.Version = db.NewID()
	e := db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		u := auth.UserFromContext(ctx)
		var n int
		if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM api_keys k JOIN users u ON u.id=k.user_id WHERE k.id=? AND k.kind=? AND k.user_id=? AND k.revoked_at='' AND (k.expires_at='' OR k.expires_at>?) AND u.status='active' AND EXISTS(SELECT 1 FROM role_permissions WHERE role=u.role AND permission IN ('*',?)) AND EXISTS(SELECT 1 FROM role_permissions WHERE role=u.role AND permission IN ('*','settings.manage'))`, id, auth.KeyKindAgent, u.ID, db.Now(), PermAPIKeys).Scan(&n); e != nil {
			return e
		}
		if n != 1 {
			return errAgentModelForbidden
		}
		var raw string
		e := tx.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key=?`, agentModelPrefix+id).Scan(&raw)
		if errors.Is(e, sql.ErrNoRows) {
			if expected != "" {
				return errAgentModelConflict
			}
		} else if e != nil {
			return e
		} else {
			var old agentModelConfig
			if json.Unmarshal([]byte(raw), &old) != nil || old.Version != expected {
				return errAgentModelConflict
			}
		}
		if c.ConnectionID != "" {
			if _, e = agentModelConnectionTx(ctx, tx, c); e != nil {
				return e
			}
		}
		if e = h.d.Settings.SetTx(ctx, tx, agentModelPrefix+id, c); e != nil {
			return e
		}
		h.d.Audit.RecordTx(ctx, tx, "agent.model.updated", "api_key", id, map[string]string{"mode": c.Mode})
		return nil
	})
	return c, e
}
func agentModelConnectionTx(ctx context.Context, tx *sql.Tx, c agentModelConfig) (connectionRecord, error) {
	var raw string
	var conn connectionRecord
	if e := tx.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key=?`, connectionPrefix+c.ConnectionID).Scan(&raw); e != nil {
		return conn, errAgentModelConflict
	}
	if json.Unmarshal([]byte(raw), &conn) != nil || conn.ID != c.ConnectionID || conn.Version != c.ConnectionVersion {
		return conn, errAgentModelConflict
	}
	if conn.Kind != "ai" || (conn.Provider != "openai" && conn.Provider != "anthropic") {
		return conn, errAgentModelInvalid
	}
	endpoint, e := normalizeConnectionEndpoint(conn.Endpoint)
	if e != nil || endpoint != conn.Endpoint {
		return conn, errAgentModelInvalid
	}
	return conn, nil
}
