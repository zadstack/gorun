package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"slices"

	"github.com/zadstack/gorun/internal/db"
)

var errAIConflict = errors.New("AI settings changed")

func aiSnapshot(ctx context.Context, q generalReader) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT key,value_json FROM settings WHERE key IN ('ai.provider','ai.base_url','ai.model','ai.api_key')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}
func aiFromSnapshot(s map[string]string) aiInput {
	get := func(k string) string { var v string; _ = json.Unmarshal([]byte(s[k]), &v); return v }
	return aiInput{EditToken: generalEditToken(s), Provider: get("ai.provider"), BaseURL: get("ai.base_url"), Model: get("ai.model")}
}
func checkAISnapshot(ctx context.Context, q generalReader, expected string) error {
	current, err := aiSnapshot(ctx, q)
	if err != nil {
		return err
	}
	if expected == "" || generalEditToken(current) != expected {
		return errAIConflict
	}
	return nil
}
func (h *handlers) saveAISettings(ctx context.Context, vals map[string]any, expected string) error {
	keys := slices.Sorted(maps.Keys(vals))
	return db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		if err := checkAISnapshot(ctx, tx, expected); err != nil {
			return err
		}
		for _, k := range keys {
			if err := h.d.Settings.SetTx(ctx, tx, k, vals[k]); err != nil {
				return err
			}
		}
		h.d.Audit.RecordTx(ctx, tx, "settings.update", "settings", "", map[string][]string{"keys": keys})
		return nil
	})
}
