package menus

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
)

var errEditConflict = errors.New("menus: changed elsewhere")

type menuReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func menuEditToken(m *Menu) string {
	raw, _ := json.Marshal(m)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func checkMenuToken(ctx context.Context, tx *sql.Tx, id, expected string) error {
	if expected == "" {
		return errEditConflict
	}
	current, err := loadMenu(ctx, tx, `id = ? ORDER BY slug`, id)
	if err != nil {
		return err
	}
	if menuEditToken(current) != expected {
		return errEditConflict
	}
	return nil
}

func submittedMenuToken(r *http.Request) *string {
	if _, ok := r.PostForm["edit_token"]; !ok {
		return nil
	}
	v := r.PostForm.Get("edit_token")
	return &v
}
