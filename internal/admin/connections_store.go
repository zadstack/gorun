package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	"github.com/zadstack/gorun/internal/db"
)

func (h *handlers) listConnections(ctx context.Context) ([]connectionRecord, error) {
	raw, err := h.d.Settings.All(ctx, connectionPrefix)
	if err != nil {
		return nil, err
	}
	rows := make([]connectionRecord, 0, len(raw))
	for _, value := range raw {
		var c connectionRecord
		if err = json.Unmarshal(value, &c); err != nil {
			return nil, err
		}
		rows = append(rows, c)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, nil
}
func (h *handlers) writeConnection(ctx context.Context, c connectionRecord, expected, action string, remove bool) error {
	return db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error { return h.writeConnectionTx(ctx, tx, c, expected, action, remove) })
}

// writeConnectionTx is writeConnection inside the caller's transaction, so
// a profile and the destinations activated from it commit together.
func (h *handlers) writeConnectionTx(ctx context.Context, tx *sql.Tx, c connectionRecord, expected, action string, remove bool) error {
	{
		var raw string
		err := tx.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key=?`, connectionPrefix+c.ID).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			if expected != "" || remove {
				return errConnectionConflict
			}
			var n int
			if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM settings WHERE substr(key,1,?)=?`, len(connectionPrefix), connectionPrefix).Scan(&n); err != nil {
				return err
			}
			if n >= maxConnections {
				return errConnectionInvalid
			}
		} else if err != nil {
			return err
		} else {
			var current connectionRecord
			if json.Unmarshal([]byte(raw), &current) != nil {
				return errConnectionInvalid
			}
			if current.Version != expected {
				return errConnectionConflict
			}
		}
		if remove {
			_, err = tx.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, connectionPrefix+c.ID)
		} else {
			err = h.d.Settings.SetTx(ctx, tx, connectionPrefix+c.ID, c)
		}
		if err != nil {
			return err
		}
		h.d.Audit.RecordTx(ctx, tx, action, "connection", c.ID, map[string]any{"kind": c.Kind})
		return nil
	}
}
