package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
)

const agentBundlePrefix = "agents.bundle."
const agentBundleTextLimit = 12000

var errAgentBundle = errors.New("agent bundle: invalid or unavailable")

type agentSkill struct {
	Name         string `json:"name"`
	Instructions string `json:"instructions"`
	ResourceName string `json:"resource_name,omitempty"`
	ResourceText string `json:"resource_text,omitempty"`
}
type agentMCPReference struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}
type agentBundle struct {
	OwnerID string              `json:"owner_id"`
	Version string              `json:"version"`
	Skills  []agentSkill        `json:"skills"`
	MCP     []agentMCPReference `json:"mcp"`
}
type agentBundleReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func agentBundleInstructions(b agentBundle) string {
	parts := []string{}
	for _, s := range b.Skills {
		text := "Skill: " + s.Name + "\n" + s.Instructions
		if s.ResourceText != "" {
			text += "\nReference (" + s.ResourceName + "):\n" + s.ResourceText
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n\n")
}
func validateAgentBundle(b agentBundle) error {
	if len(b.Skills) > 8 || len(b.MCP) > 8 {
		return errAgentBundle
	}
	names := map[string]bool{}
	for _, s := range b.Skills {
		if strings.TrimSpace(s.Name) == "" || names[s.Name] || !canvasText(s.Name, 80) || strings.TrimSpace(s.Instructions) == "" || !canvasText(s.Instructions, 4000) || !canvasText(s.ResourceName, 80) || !canvasText(s.ResourceText, 4000) || (s.ResourceName == "") != (s.ResourceText == "") {
			return errAgentBundle
		}
		names[s.Name] = true
	}
	seen := map[string]bool{}
	for _, c := range b.MCP {
		if !connectionIDPattern.MatchString(c.ID) || !connectionIDPattern.MatchString(c.Version) || seen[c.ID] {
			return errAgentBundle
		}
		seen[c.ID] = true
	}
	if len(agentBundleInstructions(b)) > agentBundleTextLimit {
		return errAgentBundle
	}
	return nil
}
func readAgentBundle(ctx context.Context, r agentBundleReader, key string) (agentBundle, error) {
	var b agentBundle
	var raw string
	e := r.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key=?`, agentBundlePrefix+key).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return b, nil
	}
	if e != nil {
		return b, e
	}
	if json.Unmarshal([]byte(raw), &b) != nil || !connectionIDPattern.MatchString(b.OwnerID) || !connectionIDPattern.MatchString(b.Version) || validateAgentBundle(b) != nil {
		return b, errAgentBundle
	}
	return b, nil
}

// Bundles never own or copy connection credentials. Every resolution checks the
// active key/owner's current management authority and the pinned MCP version.
func agentBundleOwnerCurrent(ctx context.Context, r agentBundleReader, owner, key string) bool {
	var n int
	e := r.QueryRowContext(ctx, `SELECT count(*) FROM api_keys k JOIN users u ON u.id=k.user_id WHERE k.id=? AND k.user_id=? AND k.kind='agent' AND k.revoked_at='' AND (k.expires_at='' OR k.expires_at>?) AND u.status='active' AND EXISTS(SELECT 1 FROM role_permissions WHERE role=u.role AND permission IN('*',?)) AND EXISTS(SELECT 1 FROM role_permissions WHERE role=u.role AND permission IN('*','settings.manage'))`, key, owner, db.Now(), PermAPIKeys).Scan(&n)
	return e == nil && n == 1
}
func currentAgentBundle(ctx context.Context, r agentBundleReader, owner, key string) (agentBundle, error) {
	b, e := readAgentBundle(ctx, r, key)
	if e != nil || b.Version == "" {
		return b, e
	}
	if b.OwnerID != owner {
		return b, errAgentBundle
	}
	if !agentBundleOwnerCurrent(ctx, r, owner, key) {
		return b, errAgentBundle
	}
	for _, ref := range b.MCP {
		var raw string
		var c connectionRecord
		if r.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key=?`, connectionPrefix+ref.ID).Scan(&raw) != nil || json.Unmarshal([]byte(raw), &c) != nil || c.ID != ref.ID || c.Version != ref.Version || c.Kind != "mcp" || validateConnection(c) != nil {
			return b, errAgentBundle
		}
	}
	return b, nil
}
func (h *handlers) saveAgentBundle(ctx context.Context, key, expected string, b agentBundle) (agentBundle, error) {
	u := auth.UserFromContext(ctx)
	if u == nil || auth.ActorFromContext(ctx).Kind != "user" || !auth.Can(ctx, PermAPIKeys) || !auth.Can(ctx, "settings.manage") || !WorkspaceShowsAdvanced(ctx, h.d.Settings) || !connectionIDPattern.MatchString(key) || validateAgentBundle(b) != nil {
		return b, errAgentBundle
	}
	b.OwnerID = u.ID
	b.Version = db.NewID()
	e := db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		old, e := readAgentBundle(ctx, tx, key)
		if e != nil {
			return e
		}
		if old.Version != expected {
			return errAgentModelConflict
		}
		if old.Version != "" && old.OwnerID != u.ID {
			return errAgentBundle
		}
		// Validate using a temporary stored document inside the transaction. Failed
		// ownership/reference checks roll it back, including invalidation triggers.
		if e = h.d.Settings.SetTx(ctx, tx, agentBundlePrefix+key, b); e != nil {
			return e
		}
		if _, e = currentAgentBundle(ctx, tx, u.ID, key); e != nil {
			return e
		}
		h.d.Audit.RecordTx(ctx, tx, "agent.bundle.updated", "api_key", key, map[string]any{"skills": len(b.Skills), "mcp": len(b.MCP)})
		return nil
	})
	return b, e
}
