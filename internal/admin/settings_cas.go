package admin

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/i18n"
)

var errGeneralConflict = errors.New("admin: general settings changed elsewhere")

type generalReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func generalSnapshot(ctx context.Context, q generalReader) (map[string]string, error) {
	args := make([]any, len(generalKeys))
	for i, k := range generalKeys {
		args[i] = k
	}
	rows, err := q.QueryContext(ctx, `SELECT key,value_json FROM settings WHERE key IN (`+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var key, raw string
		if err := rows.Scan(&key, &raw); err != nil {
			return nil, err
		}
		values[key] = raw
	}
	return values, rows.Err()
}

func generalEditToken(values map[string]string) string {
	raw, _ := json.Marshal(values)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (h *handlers) generalFromSnapshot(values map[string]string) generalInput {
	get := func(key, def string) string {
		raw, ok := values[key]
		if !ok {
			return def
		}
		var value string
		if json.Unmarshal([]byte(raw), &value) != nil {
			return def
		}
		return value
	}
	n := 10
	var saved int
	if json.Unmarshal([]byte(values["site.posts_per_page"]), &saved) == nil && saved >= 1 && saved <= 100 {
		n = saved
	}
	return generalInput{Title: get("site.title", ""), Tagline: get("site.tagline", ""), Locale: get("site.locale", i18n.DefaultLocale), Timezone: get("site.timezone", "UTC"), BaseURL: get("site.base_url", h.d.Config.BaseURL), Theme: get("site.theme", h.d.Config.Theme), FrontPage: get("site.front_page", ""), Logo: get("site.logo", ""), ContactEmail: get("contact.email", ""), PostsPerPage: strconv.Itoa(n)}
}
