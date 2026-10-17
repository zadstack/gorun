package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

// DefaultMagicLinkTTL applies when NewMagicLink gets ttl == 0.
const DefaultMagicLinkTTL = 15 * time.Minute

// NewMagicLink creates a single-use login token for userID (sent by email
// as BaseURL + "/auth/magic/" + token). Only its SHA-256 is stored.
func NewMagicLink(ctx context.Context, deps *kernel.Deps, userID string, ttl time.Duration) (string, error) {
	if ttl == 0 {
		ttl = DefaultMagicLinkTTL
	}
	token := randomToken(32)
	now := time.Now()
	_, err := deps.DB.ExecContext(ctx, `INSERT INTO magic_links (token_hash, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		hashToken(token), userID, db.FormatTime(now.Add(ttl)), db.FormatTime(now))
	if err != nil {
		return "", fmt.Errorf("auth: create magic link: %w", err)
	}
	return token, nil
}

// magicLinkUsable reports whether token is known, unused and unexpired,
// without consuming it.
func magicLinkUsable(ctx context.Context, deps *kernel.Deps, token string) bool {
	var n int
	err := deps.DB.QueryRowContext(ctx, `SELECT count(*) FROM magic_links WHERE token_hash = ? AND used_at = '' AND expires_at > ?`,
		hashToken(token), db.Now()).Scan(&n)
	return err == nil && n == 1
}

// ConsumeMagicLink marks token used and returns its active user, or
// ErrInvalidToken when it is unknown, used or expired.
func ConsumeMagicLink(ctx context.Context, deps *kernel.Deps, token string) (*User, error) {
	now := db.Now()
	var userID string
	err := deps.DB.QueryRowContext(ctx, `UPDATE magic_links SET used_at = ?
		WHERE token_hash = ? AND used_at = '' AND expires_at > ? RETURNING user_id`, now, hashToken(token), now).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, fmt.Errorf("auth: consume magic link: %w", err)
	}
	u, err := scanUser(deps.DB.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, userID))
	if err != nil || u.Status != StatusActive {
		return nil, ErrInvalidToken
	}
	return u, nil
}
