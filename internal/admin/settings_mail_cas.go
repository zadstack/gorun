package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/db"
)

var errMailConflict = errors.New("admin: mail settings changed elsewhere")

func mailSnapshot(ctx context.Context, q generalReader) (map[string]string, error) {
	args := make([]any, len(mailKeys))
	for i, k := range mailKeys {
		args[i] = k
	}
	rows, err := q.QueryContext(ctx, `SELECT key,value_json FROM settings WHERE key IN (`+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`)`, args...)
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
func mailFromSnapshot(s map[string]string) mailInput {
	get := func(k, def string) string {
		var v string
		if json.Unmarshal([]byte(s[k]), &v) != nil {
			return def
		}
		return v
	}
	port := get("mail.port", "")
	if port == "" {
		var n int
		if json.Unmarshal([]byte(s["mail.port"]), &n) == nil && n > 0 {
			port = strconv.Itoa(n)
		} else {
			port = "587"
		}
	}
	provider := get("mail.provider", "smtp")
	if provider == "" {
		provider = "smtp"
	}
	tls := get("mail.tls", "starttls")
	if tls == "" {
		tls = "starttls"
	}
	return mailInput{EditToken: generalEditToken(s), Provider: provider, Host: get("mail.host", ""), Port: port, Username: get("mail.username", ""), From: get("mail.from", ""), FromName: get("mail.from_name", ""), TLS: tls}
}

// Both groups are checked inside the write transaction when an API patch changes both.
func (h *handlers) saveMailSettings(ctx context.Context, vals map[string]any, expected string, generalExpected *string) error {
	keys := slices.Sorted(maps.Keys(vals))
	err := db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		current, err := mailSnapshot(ctx, tx)
		if err != nil {
			return err
		}
		if expected == "" || generalEditToken(current) != expected {
			return errMailConflict
		}
		if generalExpected != nil {
			g, err := generalSnapshot(ctx, tx)
			if err != nil {
				return err
			}
			if generalEditToken(g) != *generalExpected {
				return errGeneralConflict
			}
		}
		for _, k := range keys {
			if err := h.d.Settings.SetTx(ctx, tx, k, vals[k]); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		h.d.Audit.Record(ctx, "settings.update", "settings", "", map[string][]string{"keys": keys})
	}
	return err
}
