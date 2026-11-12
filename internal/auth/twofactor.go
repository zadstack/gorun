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

// twoFactorStatus is what the account page shows. Enabled and Pending are
// about the authenticator app (TOTP); Passkeys counts registered passkeys.
type twoFactorStatus struct {
	Enabled      bool
	EnabledAt    string
	Pending      bool
	RecoveryLeft int
	Passkeys     int
}

// Active reports any confirmed second factor (app or passkey).
func (s twoFactorStatus) Active() bool { return s.Enabled || s.Passkeys > 0 }

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
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM user_passkeys WHERE user_id = ?`, userID).Scan(&st.Passkeys); err != nil {
		return st, fmt.Errorf("auth: count passkeys: %w", err)
	}
	var enabledAt sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT enabled_at FROM user_totp WHERE user_id = ?`, userID).Scan(&enabledAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return st, fmt.Errorf("auth: two-factor status: %w", err)
	default:
		st.EnabledAt = enabledAt.String
		st.Enabled = st.EnabledAt != ""
		st.Pending = !st.Enabled
	}
	if st.Active() {
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
		// A passkey may have issued codes earlier: one set at a time.
		if _, err := tx.ExecContext(ctx, `DELETE FROM user_recovery_codes WHERE user_id = ?`, userID); err != nil {
			return err
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
// the last accepted one, or else an unused recovery code (also for users
// whose only factor is a passkey). It returns "totp" or "recovery". Both
// paths consume the code atomically, so two concurrent requests cannot use
// one code twice.
func (s *twoFactorStore) verify(ctx context.Context, userID, code string) (string, error) {
	var enc string
	var last int64
	err := s.db.QueryRowContext(ctx, `SELECT secret_enc, last_step FROM user_totp WHERE user_id = ? AND enabled_at != ''`, userID).Scan(&enc, &last)
	hasTOTP := err == nil
	if errors.Is(err, sql.ErrNoRows) {
		enabled, err := TwoFactorEnabled(ctx, s.db, userID)
		if err != nil {
			return "", err
		}
		if !enabled {
			return "", errTwoFactorNotEnabled
		}
	} else if err != nil {
		return "", fmt.Errorf("auth: load two-factor: %w", err)
	}
	if norm := normalizeOTP(code); len(norm) == totpDigits {
		if !hasTOTP {
			return "", errTwoFactorCode
		}
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
		if err := tx.QueryRowContext(ctx, twoFactorEnabledSQL, userID, userID).Scan(&n); err != nil {
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

// remove deletes the app factor (TOTP) and pending sign-ins. Recovery codes
// go too unless passkeys remain, which still need them. With required set,
// the app is only removed while a passkey created for rpID exists; the
// check is part of the DELETE, so a parallel passkey removal cannot leave
// the user without any factor (errLastRequiredFactor).
func (s *twoFactorStore) remove(ctx context.Context, userID, rpID string, required bool) (bool, error) {
	removed := false
	err := db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		q := `DELETE FROM user_totp WHERE user_id = ?`
		args := []any{userID}
		if required {
			q += ` AND EXISTS (SELECT 1 FROM user_passkeys WHERE user_id = ? AND rp_id = ? AND ? != '')`
			args = append(args, userID, rpID, rpID)
		}
		res, err := tx.ExecContext(ctx, q, args...)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 && required {
			var exists int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM user_totp WHERE user_id = ?`, userID).Scan(&exists); err != nil {
				return err
			}
			if exists > 0 {
				return errLastRequiredFactor
			}
		}
		n, _ := res.RowsAffected()
		removed = n > 0
		for _, q := range []string{
			`DELETE FROM user_recovery_codes WHERE user_id = ? AND NOT EXISTS (SELECT 1 FROM user_passkeys WHERE user_id = user_recovery_codes.user_id)`,
			`DELETE FROM login_challenges WHERE user_id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, userID); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, errLastRequiredFactor) {
		return false, err
	}
	if err != nil {
		return false, fmt.Errorf("auth: remove two-factor: %w", err)
	}
	return removed, nil
}

// removeAll deletes every second factor of the user (app, recovery codes,
// passkeys) with pending sign-ins and open passkey ceremonies. It reports
// whether a factor existed and how many passkeys were removed.
func (s *twoFactorStore) removeAll(ctx context.Context, userID string) (bool, int64, error) {
	var totp, passkeys int64
	err := db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM user_totp WHERE user_id = ?`, userID)
		if err != nil {
			return err
		}
		totp, _ = res.RowsAffected()
		if res, err = tx.ExecContext(ctx, `DELETE FROM user_passkeys WHERE user_id = ?`, userID); err != nil {
			return err
		}
		passkeys, _ = res.RowsAffected()
		for _, q := range []string{`DELETE FROM user_recovery_codes WHERE user_id = ?`, `DELETE FROM login_challenges WHERE user_id = ?`,
			`DELETE FROM webauthn_challenges WHERE user_id = ?`} {
			if _, err := tx.ExecContext(ctx, q, userID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return false, 0, fmt.Errorf("auth: reset two-factor: %w", err)
	}
	return totp > 0 || passkeys > 0, passkeys, nil
}

// issueFirstRecoveryCodes gives a user whose first factor is a new passkey
// ten recovery codes, unless they already hold some (then it returns nil):
// a parallel registration never silently replaces a set already shown.
func (s *twoFactorStore) issueFirstRecoveryCodes(ctx context.Context, userID string) ([]string, error) {
	codes := newRecoveryCodes()
	issued := false
	err := db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		// Insert-select takes the write lock before reading, so the check
		// and the insert see the same state.
		for _, c := range codes {
			res, err := tx.ExecContext(ctx, `INSERT INTO user_recovery_codes (id, user_id, code_hash, created_at)
				SELECT ?, ?, ?, ? WHERE ? OR NOT EXISTS (SELECT 1 FROM user_recovery_codes WHERE user_id = ? AND used_at = '')`,
				db.NewID(), userID, recoveryCodeHash(userID, normalizeRecoveryCode(c)), db.FormatTime(s.now()), issued, userID)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil
			}
			issued = true
		}
		return nil
	})
	if err != nil || !issued {
		return nil, err
	}
	return codes, nil
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

// twoFactorEnabledSQL is 1 when the user (both arguments) has a confirmed
// app factor or at least one passkey, else 0.
const twoFactorEnabledSQL = `SELECT EXISTS (SELECT 1 FROM user_totp WHERE user_id = ? AND enabled_at != '')
	OR EXISTS (SELECT 1 FROM user_passkeys WHERE user_id = ?)`

// TwoFactorEnabled reports whether userID has a confirmed second factor: an
// authenticator app or at least one passkey. Passkeys count whatever RP ID
// they were created for, so changing GORUN_BASE_URL never silently drops
// the second step (recovery codes or a reset get the user in).
func TwoFactorEnabled(ctx context.Context, d *sql.DB, userID string) (bool, error) {
	var n int
	err := d.QueryRowContext(ctx, twoFactorEnabledSQL, userID, userID).Scan(&n)
	return n == 1, err
}

// ResetTwoFactor removes another user's second factors (authenticator app,
// passkeys) and recovery codes (an administrator's recovery path) and
// records user.2fa_reset. Existing
// sessions stay signed in; the next sign-in asks only for the password, or
// for enrolment when the user's role requires it.
func ResetTwoFactor(ctx context.Context, d *kernel.Deps, userID string) error {
	_, _, err := resetTwoFactor(ctx, d, userID, "")
	return err
}

// ResetTwoFactorFromCLI is ResetTwoFactor for the server's shell
// (`gorun user two-factor-reset`), the lockout recovery when no administrator
// can sign in. It needs no acting user: the audit entry has the system actor
// and {"via":"cli"}. It reports whether a factor was removed and how many
// passkeys went with it.
func ResetTwoFactorFromCLI(ctx context.Context, d *kernel.Deps, userID string) (bool, int64, error) {
	return resetTwoFactor(ctx, d, userID, "cli")
}

func resetTwoFactor(ctx context.Context, d *kernel.Deps, userID, via string) (bool, int64, error) {
	s := newTwoFactorStore(d)
	removed, passkeys, err := s.removeAll(ctx, userID)
	if err != nil {
		return false, 0, err
	}
	if removed {
		meta := map[string]any{}
		if via != "" {
			meta["via"] = via
		}
		if passkeys > 0 {
			meta["passkeys"] = passkeys
		}
		if len(meta) == 0 {
			s.record(ctx, "user.2fa_reset", userID, nil)
		} else {
			s.record(ctx, "user.2fa_reset", userID, meta)
		}
	}
	return removed, passkeys, nil
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
