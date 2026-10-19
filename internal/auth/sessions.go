package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

const (
	sessionCookie = "gp_session"
	sessionTTL    = 14 * 24 * time.Hour
	// sessions are extended (sliding expiry) at most once per this interval.
	sessionRefresh = 24 * time.Hour
)

type session struct {
	ID, UserID, CSRF, ExpiresAt, ReauthAt string
}

type sessionStore struct {
	db  *sql.DB
	now func() time.Time
}

func newSessionStore(d *sql.DB) *sessionStore { return &sessionStore{db: d, now: time.Now} }

// randomToken returns n random bytes, base64url-encoded.
func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("auth: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// hashToken is the SHA-256 hex of a secret token; only hashes are stored.
func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

// create starts a session and returns the cookie token.
func (s *sessionStore) create(ctx context.Context, userID, ip, ua string) (string, session, error) {
	token := randomToken(32)
	now := s.now()
	sess := session{ID: hashToken(token), UserID: userID, CSRF: randomToken(32), ExpiresAt: db.FormatTime(now.Add(sessionTTL)), ReauthAt: db.FormatTime(now)}
	if len(ua) > 512 {
		ua = ua[:512]
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (id, user_id, csrf_token, ip, ua, expires_at, created_at, reauth_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, sess.ID, userID, sess.CSRF, ip, ua, sess.ExpiresAt, db.FormatTime(now), sess.ReauthAt)
	if err != nil {
		return "", session{}, fmt.Errorf("auth: create session: %w", err)
	}
	return token, sess, nil
}

// load returns the session and its active user. refreshed reports that the
// expiry was extended (sliding), so the cookie should be re-sent.
func (s *sessionStore) load(ctx context.Context, token string) (*session, *User, bool, error) {
	if token == "" {
		return nil, nil, false, ErrNotFound
	}
	var sess session
	row := s.db.QueryRowContext(ctx, `SELECT s.id, s.user_id, s.csrf_token, s.expires_at, s.reauth_at, u.id, u.email, u.name, u.role, u.locale, u.status, u.last_login_at, u.created_at, u.updated_at, u.onboarding_required
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.id = ?`, hashToken(token))
	var u User
	err := row.Scan(&sess.ID, &sess.UserID, &sess.CSRF, &sess.ExpiresAt, &sess.ReauthAt,
		&u.ID, &u.Email, &u.Name, &u.Role, &u.Locale, &u.Status, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt, &u.onboardingRequired)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, false, ErrNotFound
	}
	if err != nil {
		return nil, nil, false, fmt.Errorf("auth: load session: %w", err)
	}
	now := s.now()
	exp, _ := db.ParseTime(sess.ExpiresAt)
	if !now.Before(exp) || u.Status != StatusActive {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sess.ID)
		return nil, nil, false, ErrNotFound
	}
	refreshed := false
	if exp.Sub(now) < sessionTTL-sessionRefresh {
		sess.ExpiresAt = db.FormatTime(now.Add(sessionTTL))
		if _, err := s.db.ExecContext(ctx, `UPDATE sessions SET expires_at = ? WHERE id = ?`, sess.ExpiresAt, sess.ID); err != nil {
			return nil, nil, false, fmt.Errorf("auth: extend session: %w", err)
		}
		refreshed = true
	}
	return &sess, &u, refreshed, nil
}

// markReauthenticated records that the session's user just confirmed their
// password.
func (s *sessionStore) markReauthenticated(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET reauth_at = ? WHERE id = ?`, db.FormatTime(s.now()), hashToken(token))
	return err
}

func (s *sessionStore) delete(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, hashToken(token))
	return err
}
