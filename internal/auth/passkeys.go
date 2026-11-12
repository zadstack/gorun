package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"golang.org/x/net/idna"

	"github.com/zadstack/gorun/internal/db"
)

// Passkeys (WebAuthn) as a second factor; docs/specs/passkeys.md and
// docs/LOGIN-SECURITY.md#passkeys.

const (
	passkeyCeremonyTTL   = 5 * time.Minute
	passkeyHandleSize    = 64   // bytes, the WebAuthn maximum (L3 §4 "User Handle")
	maxCredentialIDBytes = 1023 // WebAuthn L3 §7.1 step 25
	maxPasskeyName       = 64   // runes
	maxPasskeyBody       = 64 << 10

	ceremonyRegister = "register"
	ceremonyLogin    = "login"
)

var (
	errPasskeysUnavailable = errors.New("auth: passkeys need an https GORUN_BASE_URL (or http://localhost)")
	errPasskeyExists       = errors.New("auth: this passkey is already registered")
	errCeremonyNotFound    = errors.New("auth: no open passkey ceremony for this challenge")
	errSignCountRegressed  = errors.New("auth: passkey signature counter did not increase")
	errLastRequiredFactor  = errors.New("auth: the last second factor of a role that requires one cannot be removed")
)

// webauthnRP is the Relying Party derived from GORUN_BASE_URL.
type webauthnRP struct {
	wa     *webauthn.WebAuthn
	rpID   string
	origin string
}

// rpFromBaseURL derives the RP ID (the host) and the single allowed origin
// (scheme://host[:port]) from the configured public base URL. WebAuthn needs
// a secure context and a domain RP ID, so plain http is accepted only for
// localhost and IP addresses never (WebAuthn L3 §4, §5.1.3 step 7).
func rpFromBaseURL(baseURL string) (rpID, origin string, err error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Host == "" || u.User != nil {
		return "", "", errPasskeysUnavailable
	}
	host := u.Hostname()
	if host == "" || net.ParseIP(host) != nil || strings.HasSuffix(host, ".") {
		return "", "", errPasskeysUnavailable
	}
	// Browsers send the ASCII (punycode) form of an internationalized host
	// in Origin and clientDataJSON. ASCII hosts are left as they are (the
	// IDNA profile would refuse valid names such as "r3--sn.example").
	if strings.ContainsFunc(host, func(r rune) bool { return r > 0x7f }) {
		if host, err = idna.Lookup.ToASCII(host); err != nil {
			return "", "", errPasskeysUnavailable
		}
	}
	host = strings.ToLower(host)
	local := host == "localhost" || strings.HasSuffix(host, ".localhost")
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && local:
	default:
		return "", "", errPasskeysUnavailable
	}
	if err := protocol.ValidateRPID(host); err != nil {
		return "", "", errPasskeysUnavailable
	}
	origin = u.Scheme + "://" + host
	// Serialize the port as browsers do: decimal without leading zeros, and
	// never the scheme's default port.
	if p := u.Port(); p != "" {
		port, err := strconv.Atoi(p)
		if err != nil || port < 1 || port > 65535 {
			return "", "", errPasskeysUnavailable
		}
		if !(u.Scheme == "https" && port == 443) && !(u.Scheme == "http" && port == 80) {
			origin += ":" + strconv.Itoa(port)
		}
	}
	return host, origin, nil
}

// newWebAuthnRP builds the library configuration for one request; name is
// shown by authenticators (the site title).
func newWebAuthnRP(baseURL, name string) (*webauthnRP, error) {
	rpID, origin, err := rpFromBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(name) == "" {
		name = "GoRun"
	}
	timeout := webauthn.TimeoutConfig{Enforce: true, Timeout: passkeyCeremonyTTL, TimeoutUVD: passkeyCeremonyTTL}
	wa, err := webauthn.New(&webauthn.Config{
		RPID:                  rpID,
		RPDisplayName:         name,
		RPOrigins:             []string{origin},
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationPreferred,
		},
		Timeouts: webauthn.TimeoutsConfig{Login: timeout, Registration: timeout},
	})
	if err != nil {
		return nil, fmt.Errorf("auth: webauthn config: %w", err)
	}
	return &webauthnRP{wa: wa, rpID: rpID, origin: origin}, nil
}

// passkey is one row of the user's list.
type passkey struct {
	ID, Name, CreatedAt, LastUsedAt string
	Synced                          bool // backup state: the passkey syncs between devices
	Usable                          bool // created for the current RP ID
}

// webauthnUser adapts a user and their credentials to webauthn.User.
type webauthnUser struct {
	u      *User
	handle []byte
	creds  []webauthn.Credential
}

func (w *webauthnUser) WebAuthnID() []byte { return w.handle }
func (w *webauthnUser) WebAuthnName() string {
	return w.u.Email
}
func (w *webauthnUser) WebAuthnDisplayName() string {
	if strings.TrimSpace(w.u.Name) != "" {
		return w.u.Name
	}
	return w.u.Email
}
func (w *webauthnUser) WebAuthnCredentials() []webauthn.Credential { return w.creds }

// passkeyStore keeps credentials, user handles and open ceremonies.
type passkeyStore struct {
	db  *sql.DB
	now func() time.Time
}

func newPasskeyStore(d *sql.DB) *passkeyStore { return &passkeyStore{db: d, now: time.Now} }

// handle returns the user's WebAuthn user handle, creating a random one
// when create is set and none exists (nil, nil otherwise).
func (s *passkeyStore) handle(ctx context.Context, userID string, create bool) ([]byte, error) {
	var h []byte
	err := s.db.QueryRowContext(ctx, `SELECT handle FROM webauthn_users WHERE user_id = ?`, userID).Scan(&h)
	if err == nil || !errors.Is(err, sql.ErrNoRows) || !create {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return h, err
	}
	h = make([]byte, passkeyHandleSize)
	if _, err := rand.Read(h); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO webauthn_users (user_id, handle, created_at) VALUES (?, ?, ?)
		ON CONFLICT (user_id) DO NOTHING`, userID, h, db.FormatTime(s.now())); err != nil {
		return nil, fmt.Errorf("auth: store webauthn user handle: %w", err)
	}
	// A concurrent request may have won the insert: read back the stored one.
	return s.handle(ctx, userID, false)
}

// webauthnUser loads u's handle and the credentials registered for rpID.
func (s *passkeyStore) webauthnUser(ctx context.Context, u *User, rpID string, create bool) (*webauthnUser, error) {
	h, err := s.handle(ctx, u.ID, create)
	if err != nil {
		return nil, err
	}
	creds, err := s.credentials(ctx, u.ID, rpID)
	if err != nil {
		return nil, err
	}
	return &webauthnUser{u: u, handle: h, creds: creds}, nil
}

func (s *passkeyStore) credentials(ctx context.Context, userID, rpID string) ([]webauthn.Credential, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT credential_id, public_key, sign_count, transports, aaguid, flags, attestation_type, attestation_format
		FROM user_passkeys WHERE user_id = ? AND rp_id = ? ORDER BY created_at`, userID, rpID)
	if err != nil {
		return nil, fmt.Errorf("auth: load passkeys: %w", err)
	}
	defer rows.Close()
	var out []webauthn.Credential
	for rows.Next() {
		var c webauthn.Credential
		var count int64
		var transports string
		var flags int
		if err := rows.Scan(&c.ID, &c.PublicKey, &count, &transports, &c.Authenticator.AAGUID, &flags, &c.AttestationType, &c.AttestationFormat); err != nil {
			return nil, err
		}
		c.Authenticator.SignCount = uint32(count) //nolint:gosec // stored from a uint32
		c.Flags = webauthn.NewCredentialFlags(protocol.AuthenticatorFlags(flags))
		for t := range strings.SplitSeq(transports, ",") {
			if t != "" {
				c.Transport = append(c.Transport, protocol.AuthenticatorTransport(t))
			}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// list returns all of the user's passkeys, newest first; Usable marks those
// created for rpID.
func (s *passkeyStore) list(ctx context.Context, userID, rpID string) ([]passkey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, created_at, last_used_at, flags, rp_id FROM user_passkeys
		WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("auth: list passkeys: %w", err)
	}
	defer rows.Close()
	var out []passkey
	for rows.Next() {
		var p passkey
		var flags int
		var rp string
		if err := rows.Scan(&p.ID, &p.Name, &p.CreatedAt, &p.LastUsedAt, &flags, &rp); err != nil {
			return nil, err
		}
		p.Synced = protocol.AuthenticatorFlags(flags).HasBackupState()
		p.Usable = rpID != "" && rp == rpID
		out = append(out, p)
	}
	return out, rows.Err()
}

// add stores a verified credential.
func (s *passkeyStore) add(ctx context.Context, userID, rpID, name string, c *webauthn.Credential) (string, error) {
	if len(c.ID) == 0 || len(c.ID) > maxCredentialIDBytes {
		return "", errors.New("auth: credential id has an invalid length")
	}
	transports := make([]string, 0, len(c.Transport))
	for _, t := range c.Transport {
		transports = append(transports, string(t))
	}
	var known int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM user_passkeys WHERE credential_id = ?`, c.ID).Scan(&known); err != nil {
		return "", err
	}
	if known > 0 { // WebAuthn L3 §7.1 step 26: not yet registered for any user
		return "", errPasskeyExists
	}
	id := db.NewID()
	_, err := s.db.ExecContext(ctx, `INSERT INTO user_passkeys (id, user_id, rp_id, credential_id, public_key, sign_count, transports, aaguid, flags,
		attestation_type, attestation_format, name, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, userID, rpID, c.ID, c.PublicKey, int64(c.Authenticator.SignCount), strings.Join(transports, ","), c.Authenticator.AAGUID,
		int(c.Flags.ProtocolValue()), c.AttestationType, c.AttestationFormat, name, db.FormatTime(s.now()))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return "", errPasskeyExists
		}
		return "", fmt.Errorf("auth: store passkey: %w", err)
	}
	return id, nil
}

// recordUse writes back the counter, flags and last use after a verified
// assertion. The update only applies while the stored counter is still
// below the new one (or both are zero, for authenticators without a
// counter), so two parallel assertions cannot both pass the check.
func (s *passkeyStore) recordUse(ctx context.Context, userID string, c *webauthn.Credential) (string, error) {
	var id string
	n := int64(c.Authenticator.SignCount)
	err := s.db.QueryRowContext(ctx, `UPDATE user_passkeys SET sign_count = ?, flags = ?, last_used_at = ?
		WHERE user_id = ? AND credential_id = ? AND (sign_count < ? OR (? = 0 AND sign_count = 0)) RETURNING id`,
		n, int(c.Flags.ProtocolValue()), db.FormatTime(s.now()), userID, c.ID, n, n).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errSignCountRegressed
	}
	if err != nil {
		return "", fmt.Errorf("auth: record passkey use: %w", err)
	}
	return id, nil
}

// remove deletes one of the user's passkeys and returns its name and
// whether any second factor is left. With required set, it refuses
// (errLastRequiredFactor) unless the app factor or another passkey usable
// under rpID remains; the check and the delete are one statement, so two
// parallel removals cannot both pass it. When no factor is left the
// recovery codes go too, in the same transaction.
func (s *passkeyStore) remove(ctx context.Context, userID, id, rpID string, required bool) (string, bool, error) {
	var name string
	left := false
	err := db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		q := `DELETE FROM user_passkeys WHERE id = ? AND user_id = ?`
		args := []any{id, userID}
		if required {
			q += ` AND (EXISTS (SELECT 1 FROM user_totp WHERE user_id = ? AND enabled_at != '')
				OR EXISTS (SELECT 1 FROM user_passkeys o WHERE o.user_id = ? AND o.id != ? AND o.rp_id = ? AND ? != ''))`
			args = append(args, userID, userID, id, rpID, rpID)
		}
		err := tx.QueryRowContext(ctx, q+` RETURNING name`, args...).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM user_passkeys WHERE id = ? AND user_id = ?`, id, userID).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return errLastRequiredFactor
			}
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		var enabled int
		if err := tx.QueryRowContext(ctx, twoFactorEnabledSQL, userID, userID).Scan(&enabled); err != nil {
			return err
		}
		left = enabled == 1
		if !left {
			// Nothing left to ask for: drop codes, pending sign-ins and open
			// passkey requests, as turning off the app does.
			for _, q := range []string{`DELETE FROM user_recovery_codes WHERE user_id = ?`, `DELETE FROM login_challenges WHERE user_id = ?`,
				`DELETE FROM webauthn_challenges WHERE user_id = ?`} {
				if _, err := tx.ExecContext(ctx, q, userID); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errLastRequiredFactor) || errors.Is(err, ErrNotFound) {
			return "", false, err
		}
		return "", false, fmt.Errorf("auth: remove passkey: %w", err)
	}
	return name, left, nil
}

// usableCount counts the user's passkeys created for rpID.
func (s *passkeyStore) usableCount(ctx context.Context, userID, rpID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM user_passkeys WHERE user_id = ? AND rp_id = ?`, userID, rpID).Scan(&n)
	return n, err
}

func challengeHash(challenge string) string {
	sum := sha256.Sum256([]byte("gorun-webauthn:" + challenge))
	return hex.EncodeToString(sum[:])
}

// saveCeremony stores an open ceremony: only the hash of its challenge, the
// rest of the session data without it. An earlier open ceremony of the same
// kind and binding is dropped, so each session or pending sign-in has at
// most one.
func (s *passkeyStore) saveCeremony(ctx context.Context, kind, userID, binding string, sd *webauthn.SessionData) error {
	hash := challengeHash(sd.Challenge)
	stored := *sd
	stored.Challenge = ""
	data, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	now := s.now()
	return db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM webauthn_challenges WHERE kind = ? AND binding = ?`, kind, binding); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO webauthn_challenges (challenge_hash, kind, user_id, binding, session_data, expires_at, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, hash, kind, userID, binding, string(data), db.FormatTime(now.Add(passkeyCeremonyTTL)), db.FormatTime(now))
		return err
	})
}

// takeCeremony deletes and returns the open ceremony for challenge (as sent
// back by the client in clientDataJSON). It must match kind and binding and
// be unexpired; deleting before verification makes every challenge single
// use, also under concurrent requests.
func (s *passkeyStore) takeCeremony(ctx context.Context, kind, binding, challenge string) (string, *webauthn.SessionData, error) {
	if challenge == "" || len(challenge) > 256 {
		return "", nil, errCeremonyNotFound
	}
	var userID, data string
	err := s.db.QueryRowContext(ctx, `DELETE FROM webauthn_challenges WHERE challenge_hash = ? AND kind = ? AND binding = ? AND expires_at > ?
		RETURNING user_id, session_data`, challengeHash(challenge), kind, binding, db.FormatTime(s.now())).Scan(&userID, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, errCeremonyNotFound
	}
	if err != nil {
		return "", nil, fmt.Errorf("auth: load passkey ceremony: %w", err)
	}
	var sd webauthn.SessionData
	if err := json.Unmarshal([]byte(data), &sd); err != nil {
		return "", nil, fmt.Errorf("auth: decode passkey ceremony: %w", err)
	}
	// Its hash matched a challenge we issued and have now consumed.
	sd.Challenge = challenge
	return userID, &sd, nil
}

// cleanPasskeyName trims the name the user gave a passkey; empty means the
// default name. ok is false when it is too long or has control characters.
func cleanPasskeyName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > maxPasskeyName || !utf8.ValidString(name) {
		return "", false
	}
	// Control characters, line separators and bidi overrides (which could
	// reorder the surrounding text where the name is shown) are refused.
	if strings.ContainsFunc(name, func(r rune) bool {
		// Cf covers bidi controls, zero-width characters, U+FEFF and tags.
		return unicode.IsControl(r) || r == 0x2028 || r == 0x2029 || unicode.Is(unicode.Cf, r) ||
			unicode.Is(unicode.Noncharacter_Code_Point, r)
	}) {
		return "", false
	}
	return name, true
}

// cleanupCeremoniesSQL is part of the hourly auth cleanup.
const cleanupCeremoniesSQL = `DELETE FROM webauthn_challenges WHERE expires_at <= ?`
