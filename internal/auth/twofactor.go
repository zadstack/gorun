package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

// TwoFactorRolesSetting lists the roles (JSON array of slugs) whose members
// must enrol a second factor before they can use the admin.
const TwoFactorRolesSetting = "auth.two_factor_roles"

const (
	recoveryCodeCount = 10
	recoveryCodeLen   = 10 // characters of Crockford base32: 50 random bits
	// recoveryAlphabet is Crockford's base32 (no i, l, o, u) in lower case.
	recoveryAlphabet = "0123456789abcdefghjkmnpqrstvwxyz"
)

var (
	errTwoFactorCode       = errors.New("auth: invalid two-factor code")
	errTwoFactorEnabled    = errors.New("auth: two-factor authentication is already enabled")
	errTwoFactorNotEnabled = errors.New("auth: two-factor authentication is not enabled")
	errNoPendingEnrolment  = errors.New("auth: no pending two-factor enrolment")
)

// twoFactorStatus is what the account page shows.
type twoFactorStatus struct {
	Enabled      bool
	EnabledAt    string
	Pending      bool
	RecoveryLeft int
}

// twoFactorStore keeps TOTP secrets (encrypted) and recovery codes (hashed).
type twoFactorStore struct {
	db    *sql.DB
	key   []byte
	audit *audit.Writer
	log   *slog.Logger
	now   func() time.Time
	// unreadable remembers users whose secret failed to decrypt, so the
	// operator error is logged once per user and process, not per attempt.
	unreadable sync.Map
}

func newTwoFactorStore(d *kernel.Deps) *twoFactorStore {
	return &twoFactorStore{db: d.DB, key: d.Config.SecretKey, audit: d.Audit, log: d.Log, now: time.Now}
}

// secretUnreadable reports a stored secret that does not decrypt, almost
// always because GORUN_SECRET_KEY changed after enrolment. Only the user id
// is logged, never the ciphertext or the error detail.
func (s *twoFactorStore) secretUnreadable(userID string) {
	if _, seen := s.unreadable.LoadOrStore(userID, struct{}{}); seen || s.log == nil {
		return
	}
	s.log.Error("auth: two-factor secret cannot be decrypted; was GORUN_SECRET_KEY changed? "+
		"The user can still sign in with a recovery code; reset their two-factor (admin Users page or "+
		"`gorun user two-factor-reset --email …`) so they can enrol again", "user_id", userID)
}

func (s *twoFactorStore) record(ctx context.Context, action, userID string, meta any) {
	if s.audit != nil {
		s.audit.Record(ctx, action, "user", userID, meta)
	}
}

// totpAAD binds a sealed secret to its user: a ciphertext copied onto another
// account does not decrypt.
func totpAAD(userID string) string { return "auth.totp:" + userID }

func (s *twoFactorStore) seal(userID string, secret []byte) (string, error) {
	return db.Encrypt(s.key, secret, totpAAD(userID))
}

func (s *twoFactorStore) open(userID, enc string) ([]byte, error) {
	return db.Decrypt(s.key, enc, totpAAD(userID))
}

// status reports enrolment state and unused recovery codes.
func (s *twoFactorStore) status(ctx context.Context, userID string) (twoFactorStatus, error) {
	var st twoFactorStatus
	var enabledAt sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT enabled_at FROM user_totp WHERE user_id = ?`, userID).Scan(&enabledAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return st, nil
	case err != nil:
		return st, fmt.Errorf("auth: two-factor status: %w", err)
	}
	st.EnabledAt = enabledAt.String
	st.Enabled = st.EnabledAt != ""
	st.Pending = !st.Enabled
	if st.Enabled {
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM user_recovery_codes WHERE user_id = ? AND used_at = ''`, userID).Scan(&st.RecoveryLeft); err != nil {
			return st, fmt.Errorf("auth: count recovery codes: %w", err)
		}
	}
	return st, nil
}

// begin stores a new pending secret (replacing an earlier pending one) and
// returns it. An enabled factor is never replaced.
func (s *twoFactorStore) begin(ctx context.Context, userID string) ([]byte, error) {
	secret := newTOTPSecret()
	enc, err := s.seal(userID, secret)
	if err != nil {
		return nil, err
	}
	now := db.FormatTime(s.now())
	res, err := s.db.ExecContext(ctx, `INSERT INTO user_totp (user_id, secret_enc, created_at, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET secret_enc = excluded.secret_enc, last_step = 0, updated_at = excluded.updated_at
		WHERE user_totp.enabled_at = ''`, userID, enc, now, now)
	if err != nil {
		return nil, fmt.Errorf("auth: begin two-factor: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, errTwoFactorEnabled
	}
	return secret, nil
}

// pendingSecret returns the secret of an unconfirmed enrolment.
func (s *twoFactorStore) pendingSecret(ctx context.Context, userID string) ([]byte, error) {
	var enc string
	err := s.db.QueryRowContext(ctx, `SELECT secret_enc FROM user_totp WHERE user_id = ? AND enabled_at = ''`, userID).Scan(&enc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNoPendingEnrolment
	}
	if err != nil {
		return nil, fmt.Errorf("auth: load pending two-factor: %w", err)
	}
	return s.open(userID, enc)
}

// confirm enables the pending secret when code is valid for it and returns
// the first set of recovery codes (plaintext, shown once).
func (s *twoFactorStore) confirm(ctx context.Context, userID, code string) ([]string, error) {
	secret, err := s.pendingSecret(ctx, userID)
	if err != nil {
		return nil, err
	}
	step, ok := matchTOTP(secret, code, s.now(), 0)
	if !ok {
		return nil, errTwoFactorCode
	}
	codes := newRecoveryCodes()
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		now := db.FormatTime(s.now())
		res, err := tx.ExecContext(ctx, `UPDATE user_totp SET enabled_at = ?, last_step = ?, updated_at = ? WHERE user_id = ? AND enabled_at = ''`,
			now, step, now, userID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return errNoPendingEnrolment
		}
		return storeRecoveryCodes(ctx, tx, userID, codes, now)
	})
	if err != nil {
		return nil, err
	}
	s.record(ctx, "user.2fa_enabled", userID, nil)
	return codes, nil
}

// verify checks a login or confirmation code: a TOTP code for a step after
// the last accepted one, or else an unused recovery code. It returns
// "totp" or "recovery". Both paths consume the code atomically, so two
// concurrent requests cannot use one code twice.
func (s *twoFactorStore) verify(ctx context.Context, userID, code string) (string, error) {
	var enc string
	var last int64
	err := s.db.QueryRowContext(ctx, `SELECT secret_enc, last_step FROM user_totp WHERE user_id = ? AND enabled_at != ''`, userID).Scan(&enc, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errTwoFactorNotEnabled
	}
	if err != nil {
		return "", fmt.Errorf("auth: load two-factor: %w", err)
	}
	if norm := normalizeOTP(code); len(norm) == totpDigits {
		secret, err := s.open(userID, enc)
		if err != nil {
			// No app code can match a secret we cannot read: answer like a
			// wrong code so the user can fall back to a recovery code.
			s.secretUnreadable(userID)
			return "", errTwoFactorCode
		}
		step, ok := matchTOTP(secret, norm, s.now(), last)
		if !ok {
			return "", errTwoFactorCode
		}
		res, err := s.db.ExecContext(ctx, `UPDATE user_totp SET last_step = ?, updated_at = ? WHERE user_id = ? AND last_step < ?`,
			step, db.FormatTime(s.now()), userID, step)
		if err != nil {
			return "", fmt.Errorf("auth: record two-factor step: %w", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return "", errTwoFactorCode // replayed concurrently
		}
		return "totp", nil
	}
	if err := s.useRecoveryCode(ctx, userID, code); err != nil {
		return "", err
	}
	return "recovery", nil
}

// useRecoveryCode marks a matching unused recovery code as used.
func (s *twoFactorStore) useRecoveryCode(ctx context.Context, userID, code string) error {
	norm := normalizeRecoveryCode(code)
	if len(norm) != recoveryCodeLen {
		return errTwoFactorCode
	}
	want := []byte(recoveryCodeHash(userID, norm))
	rows, err := s.db.QueryContext(ctx, `SELECT id, code_hash FROM user_recovery_codes WHERE user_id = ? AND used_at = ''`, userID)
	if err != nil {
		return fmt.Errorf("auth: load recovery codes: %w", err)
	}
	match := ""
	for rows.Next() {
		var id, hash string
		if err := rows.Scan(&id, &hash); err != nil {
			rows.Close()
			return err
		}
		// Compare every row in constant time; no early exit.
		if subtle.ConstantTimeCompare([]byte(hash), want) == 1 && match == "" {
			match = id
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if match == "" {
		return errTwoFactorCode
	}
	res, err := s.db.ExecContext(ctx, `UPDATE user_recovery_codes SET used_at = ? WHERE id = ? AND used_at = ''`, db.FormatTime(s.now()), match)
	if err != nil {
		return fmt.Errorf("auth: use recovery code: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errTwoFactorCode
	}
	var left int
	_ = s.db.QueryRowContext(ctx, `SELECT count(*) FROM user_recovery_codes WHERE user_id = ? AND used_at = ''`, userID).Scan(&left)
	s.record(ctx, "user.2fa_recovery_used", userID, map[string]int{"remaining": left})
	return nil
}

// regenerate replaces every recovery code; the old ones stop working.
func (s *twoFactorStore) regenerate(ctx context.Context, userID string) ([]string, error) {
	codes := newRecoveryCodes()
	err := db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM user_totp WHERE user_id = ? AND enabled_at != ''`, userID).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			return errTwoFactorNotEnabled
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM user_recovery_codes WHERE user_id = ?`, userID); err != nil {
			return err
		}
		return storeRecoveryCodes(ctx, tx, userID, codes, db.FormatTime(s.now()))
	})
	if err != nil {
		return nil, err
	}
	s.record(ctx, "user.2fa_recovery_regenerated", userID, nil)
	return codes, nil
}

// remove deletes the factor, its recovery codes and pending sign-ins.
func (s *twoFactorStore) remove(ctx context.Context, userID string) (bool, error) {
	removed := false
	err := db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM user_totp WHERE user_id = ?`, userID)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		removed = n > 0
		for _, q := range []string{`DELETE FROM user_recovery_codes WHERE user_id = ?`, `DELETE FROM login_challenges WHERE user_id = ?`} {
			if _, err := tx.ExecContext(ctx, q, userID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("auth: remove two-factor: %w", err)
	}
	return removed, nil
}

func storeRecoveryCodes(ctx context.Context, tx *sql.Tx, userID string, codes []string, now string) error {
	for _, c := range codes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_recovery_codes (id, user_id, code_hash, created_at) VALUES (?, ?, ?, ?)`,
			db.NewID(), userID, recoveryCodeHash(userID, normalizeRecoveryCode(c)), now); err != nil {
			return fmt.Errorf("auth: store recovery code: %w", err)
		}
	}
	return nil
}

// newRecoveryCodes returns fresh codes formatted "xxxxx-xxxxx".
func newRecoveryCodes() []string {
	out := make([]string, recoveryCodeCount)
	buf := make([]byte, recoveryCodeLen)
	for i := range out {
		if _, err := rand.Read(buf); err != nil {
			panic("auth: crypto/rand failed: " + err.Error())
		}
		var b strings.Builder
		for j, x := range buf {
			if j == recoveryCodeLen/2 {
				b.WriteByte('-')
			}
			b.WriteByte(recoveryAlphabet[x&31]) // 256 is a multiple of 32: no bias
		}
		out[i] = b.String()
	}
	return out
}

// normalizeRecoveryCode lower-cases, drops separators and applies
// Crockford's decoding of look-alike letters (i, l → 1; o → 0).
func normalizeRecoveryCode(s string) string {
	s = strings.ToLower(normalizeOTP(s))
	return strings.NewReplacer("i", "1", "l", "1", "o", "0").Replace(s)
}

// recoveryCodeHash binds a code to its user. Codes are random 50-bit values
// generated by the server, so SHA-256 resists offline guessing as well as
// the session and API-key hashes do, while Argon2 would make every attempt
// (which checks all ten codes) cost ten password hashes.
func recoveryCodeHash(userID, normalized string) string {
	sum := sha256.Sum256([]byte("gorun-recovery:" + userID + ":" + normalized))
	return hex.EncodeToString(sum[:])
}

// TwoFactorEnabled reports whether userID has a confirmed second factor.
func TwoFactorEnabled(ctx context.Context, d *sql.DB, userID string) (bool, error) {
	var n int
	err := d.QueryRowContext(ctx, `SELECT count(*) FROM user_totp WHERE user_id = ? AND enabled_at != ''`, userID).Scan(&n)
	return n == 1, err
}

// ResetTwoFactor removes another user's second factor and recovery codes
// (an administrator's recovery path) and records user.2fa_reset. Existing
// sessions stay signed in; the next sign-in asks only for the password, or
// for enrolment when the user's role requires it.
func ResetTwoFactor(ctx context.Context, d *kernel.Deps, userID string) error {
	s := newTwoFactorStore(d)
	removed, err := s.remove(ctx, userID)
	if err != nil {
		return err
	}
	if removed {
		s.record(ctx, "user.2fa_reset", userID, nil)
	}
	return nil
}

// TwoFactorRequiredRoles returns the roles that must use a second factor.
func TwoFactorRequiredRoles(ctx context.Context, d *kernel.Deps) []string {
	if d.Settings == nil {
		return nil
	}
	var roles []string
	if err := d.Settings.Get(ctx, TwoFactorRolesSetting, &roles); err != nil {
		return nil
	}
	return roles
}

// twoFactorEnrolmentDue reports whether u must enrol before using the admin.
func twoFactorEnrolmentDue(ctx context.Context, d *kernel.Deps, u *User) bool {
	if u == nil || u.twoFactor {
		return false
	}
	return slices.Contains(TwoFactorRequiredRoles(ctx, d), u.Role)
}
