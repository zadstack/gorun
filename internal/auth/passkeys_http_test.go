package auth_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
)

func bootCfgPlainHTTP(t *testing.T) *site {
	return bootCfg(t, func(c *config.Config) { c.BaseURL = "http://example.com" }, adminStub{})
}

// The boot helper's GORUN_BASE_URL is http://localhost: RP ID "localhost".
const (
	pkOrigin = "http://localhost"
	pkRPID   = "localhost"
)

var b64 = base64.RawURLEncoding

// softAuthenticator is a minimal WebAuthn authenticator for tests: one
// ES256 (P-256) credential, "none" attestation, an explicit counter.
type softAuthenticator struct {
	t       *testing.T
	key     *ecdsa.PrivateKey
	credID  []byte
	handle  []byte
	counter uint32
	// Overrides for negative tests.
	origin, rpID string
}

func newSoftAuthenticator(t *testing.T) *softAuthenticator {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	rand.Read(id)
	return &softAuthenticator{t: t, key: key, credID: id, origin: pkOrigin, rpID: pkRPID}
}

// Minimal CBOR (RFC 8949) for the attestation object and COSE key.
func cborHead(major byte, n uint64) []byte {
	switch {
	case n < 24:
		return []byte{major<<5 | byte(n)}
	case n < 256:
		return []byte{major<<5 | 24, byte(n)}
	default:
		return []byte{major<<5 | 25, byte(n >> 8), byte(n)}
	}
}
func cborInt(v int) []byte {
	if v < 0 {
		return cborHead(1, uint64(-1-v))
	}
	return cborHead(0, uint64(v))
}
func cborBytes(b []byte) []byte { return append(cborHead(2, uint64(len(b))), b...) }
func cborText(s string) []byte  { return append(cborHead(3, uint64(len(s))), s...) }

func (a *softAuthenticator) coseKey() []byte {
	// Uncompressed SEC 1 point: 0x04 || X (32 bytes) || Y (32 bytes).
	point, err := a.key.PublicKey.Bytes()
	if err != nil || len(point) != 65 {
		panic("softAuthenticator: P-256 public key encoding")
	}
	x, y := point[1:33], point[33:]
	out := cborHead(5, 5)
	out = append(out, cborInt(1)...)
	out = append(out, cborInt(2)...) // kty: EC2
	out = append(out, cborInt(3)...)
	out = append(out, cborInt(-7)...) // alg: ES256
	out = append(out, cborInt(-1)...)
	out = append(out, cborInt(1)...) // crv: P-256
	out = append(out, cborInt(-2)...)
	out = append(out, cborBytes(x)...)
	out = append(out, cborInt(-3)...)
	out = append(out, cborBytes(y)...)
	return out
}

func (a *softAuthenticator) authData(flags byte, attested bool) []byte {
	rp := sha256.Sum256([]byte(a.rpID))
	out := append([]byte{}, rp[:]...)
	out = append(out, flags)
	out = binary.BigEndian.AppendUint32(out, a.counter)
	if attested {
		out = append(out, make([]byte, 16)...) // AAGUID
		out = binary.BigEndian.AppendUint16(out, uint16(len(a.credID)))
		out = append(out, a.credID...)
		out = append(out, a.coseKey()...)
	}
	return out
}

func (a *softAuthenticator) clientData(typ, challenge string) []byte {
	b, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": a.origin, "crossOrigin": false})
	return b
}

// create answers registration options (the begin response's data).
func (a *softAuthenticator) create(options map[string]any) map[string]any {
	pk := options["publicKey"].(map[string]any)
	user := pk["user"].(map[string]any)
	a.handle, _ = b64.DecodeString(user["id"].(string))
	cd := a.clientData("webauthn.create", pk["challenge"].(string))
	att := cborHead(5, 3)
	att = append(att, cborText("fmt")...)
	att = append(att, cborText("none")...)
	att = append(att, cborText("attStmt")...)
	att = append(att, cborHead(5, 0)...)
	att = append(att, cborText("authData")...)
	att = append(att, cborBytes(a.authData(0x45, true))...) // UP | UV | AT
	return map[string]any{
		"id": b64.EncodeToString(a.credID), "rawId": b64.EncodeToString(a.credID), "type": "public-key",
		"response":               map[string]any{"clientDataJSON": b64.EncodeToString(cd), "attestationObject": b64.EncodeToString(att), "transports": []string{"internal"}},
		"clientExtensionResults": map[string]any{},
	}
}

// get answers request options with a signed assertion; the counter goes up
// by one first.
func (a *softAuthenticator) get(options map[string]any) map[string]any {
	pk := options["publicKey"].(map[string]any)
	a.counter++
	ad := a.authData(0x05, false) // UP | UV
	cd := a.clientData("webauthn.get", pk["challenge"].(string))
	cdHash := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), cdHash[:]...)) // ES256 signs SHA-256(authData || clientDataHash)
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		a.t.Fatal(err)
	}
	return map[string]any{
		"id": b64.EncodeToString(a.credID), "rawId": b64.EncodeToString(a.credID), "type": "public-key",
		"response": map[string]any{"clientDataJSON": b64.EncodeToString(cd), "authenticatorData": b64.EncodeToString(ad),
			"signature": b64.EncodeToString(sig), "userHandle": b64.EncodeToString(a.handle)},
		"clientExtensionResults": map[string]any{},
	}
}

// postJSON sends a WebAuthn ceremony request the way passkeys.js does.
func (s *site) postJSON(path, csrf, origin string, body any) (int, map[string]any) {
	s.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	} else {
		buf.WriteString("{}")
	}
	req, _ := http.NewRequest("POST", s.srv.URL+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := s.c.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		return resp.StatusCode, map[string]any{"raw": string(raw), "location": resp.Header.Get("Location")}
	}
	return resp.StatusCode, env
}

func envData(t *testing.T, env map[string]any) map[string]any {
	t.Helper()
	d, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("no data in %v", env)
	}
	return d
}

func envCode(env map[string]any) string {
	if e, ok := env["error"].(map[string]any); ok {
		return e["code"].(string)
	}
	return ""
}

// addPasskey registers a passkey for the signed-in user and returns the
// finish response data.
func (s *site) addPasskey(a *softAuthenticator, name string) map[string]any {
	s.t.Helper()
	_, page := s.do("GET", "/admin/account/two-factor/passkeys", nil)
	csrf := csrfFrom(s.t, page)
	st, env := s.postJSON("/admin/account/two-factor/passkeys/register/begin", csrf, pkOrigin, nil)
	if st != 200 {
		s.t.Fatalf("register begin: %d %v", st, env)
	}
	st, env = s.postJSON("/admin/account/two-factor/passkeys/register/finish", csrf, pkOrigin, map[string]any{"name": name, "credential": a.create(envData(s.t, env))})
	if st != 200 {
		s.t.Fatalf("register finish: %d %v", st, env)
	}
	return envData(s.t, env)
}

// passkeyStep runs the passkey on the second sign-in step: begin, then
// finish with the assertion built by sign (default: a.get).
func (s *site) passkeyStep(a *softAuthenticator) (map[string]any, string) {
	s.t.Helper()
	r, page := s.do("GET", auth.SecondFactorPath, nil)
	if r.StatusCode != 200 || !strings.Contains(page, "data-passkey-login") {
		s.t.Fatalf("second step offers no passkey: %d", r.StatusCode)
	}
	csrf := csrfFrom(s.t, page)
	st, env := s.postJSON("/admin/login/2fa/passkey/begin", csrf, pkOrigin, nil)
	if st != 200 {
		s.t.Fatalf("login begin: %d %v", st, env)
	}
	return a.get(envData(s.t, env)), csrf
}

func (s *site) passkeyFinish(csrf, origin string, assertion map[string]any) (int, map[string]any) {
	return s.postJSON("/admin/login/2fa/passkey/finish", csrf, origin, assertion)
}

func TestPasskeyRegistrationAndSecondFactorSignIn(t *testing.T) {
	s := boot(t)
	u := newUser(t, s, "pk@example.com", "editor")
	s.login("pk@example.com", tfPassword)
	a := newSoftAuthenticator(t)
	data := s.addPasskey(a, "  Work laptop ")

	// First factor and no recovery codes yet: ten codes come back once.
	codes, _ := data["codes"].([]any)
	if len(codes) != 10 {
		t.Fatalf("recovery codes with the first passkey: %v", data)
	}
	// The user handle is 64 random bytes, not the user id or email.
	if len(a.handle) != 64 || bytes.Contains(a.handle, []byte(u.ID)) || bytes.Contains(a.handle, []byte("pk@example.com")) {
		t.Fatalf("user handle %x", a.handle)
	}
	_, page := s.do("GET", "/admin/account/two-factor/passkeys", nil)
	if !strings.Contains(page, "<bdi>Work laptop</bdi>") || !strings.Contains(page, "data-passkey ") && !strings.Contains(page, "data-passkey>") {
		t.Fatalf("passkey not listed:\n%s", page)
	}
	// A second passkey keeps the same handle and issues no new codes.
	b := newSoftAuthenticator(t)
	if data := s.addPasskey(b, ""); data["codes"] != nil {
		t.Fatal("codes issued again for a second passkey")
	}
	if !bytes.Equal(a.handle, b.handle) {
		t.Fatal("user handle changed between registrations")
	}
	// The default name is in the user's language (Arabic in these tests).
	if _, page := s.do("GET", "/admin/account/two-factor/passkeys", nil); !strings.Contains(page, "<bdi>مفتاح مرور</bdi>") {
		t.Fatal("default name not used")
	}
	s.logout()

	// The password alone now leads to the second step.
	if r := s.login("pk@example.com", tfPassword); r.Header.Get("Location") != auth.SecondFactorPath {
		t.Fatalf("login with a passkey: %s", r.Header.Get("Location"))
	}
	assertion, csrf := s.passkeyStep(a)
	st, env := s.passkeyFinish(csrf, pkOrigin, assertion)
	if st != 200 || envData(t, env)["redirect"] != "/admin" {
		t.Fatalf("passkey sign-in: %d %v", st, env)
	}
	if r, _ := s.do("GET", "/admin", nil); r.StatusCode != 200 {
		t.Fatalf("session after passkey: %d", r.StatusCode)
	}
	var count int
	var lastUsed string
	s.app.Deps.DB.QueryRow(`SELECT sign_count, last_used_at FROM user_passkeys WHERE credential_id = ?`, a.credID).Scan(&count, &lastUsed)
	if count != 1 || lastUsed == "" {
		t.Fatalf("counter %d, last used %q", count, lastUsed)
	}
	for _, action := range []string{"user.passkey_added", "user.passkey_used"} {
		if auditCount(t, s, action) == 0 {
			t.Errorf("no %s audit event", action)
		}
	}
	// A recovery code also works for a passkey-only user.
	s.logout()
	s.login("pk@example.com", tfPassword)
	if r, _ := s.secondFactor(codes[0].(string)); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("recovery code for a passkey-only user: %d", r.StatusCode)
	}
	// Neither challenges nor credential bytes reach the audit log.
	var meta string
	s.app.Deps.DB.QueryRow(`SELECT group_concat(meta_json) FROM audit_log`).Scan(&meta)
	if strings.Contains(meta, b64.EncodeToString(a.credID)) || strings.Contains(meta, codes[1].(string)) {
		t.Fatal("secret material in the audit log")
	}
}

func TestPasskeyRefusals(t *testing.T) {
	s := boot(t)
	newUser(t, s, "neg@example.com", "editor")
	s.login("neg@example.com", tfPassword)
	a := newSoftAuthenticator(t)
	s.addPasskey(a, "Key")
	s.logout()

	failed := func() int { return auditCount(t, s, "user.passkey_failed") }

	// Wrong Origin header: refused before anything is verified.
	s.login("neg@example.com", tfPassword)
	assertion, csrf := s.passkeyStep(a)
	if st, env := s.passkeyFinish(csrf, "https://evil.example", assertion); st != http.StatusForbidden || envCode(env) != "origin_mismatch" {
		t.Fatalf("wrong origin header: %d %v", st, env)
	}
	// The same assertion from the right origin works once…
	if st, env := s.passkeyFinish(csrf, pkOrigin, assertion); st != 200 {
		t.Fatalf("valid assertion: %d %v", st, env)
	}
	s.logout()
	// …and is refused on a new pending sign-in (replayed challenge).
	s.login("neg@example.com", tfPassword)
	_, csrf = s.passkeyStep(a)
	if st, env := s.passkeyFinish(csrf, pkOrigin, assertion); st != http.StatusUnauthorized || envCode(env) != "ceremony_expired" {
		t.Fatalf("replayed assertion: %d %v", st, env)
	}

	// Origin in clientDataJSON differs from the RP origin.
	s.login("neg@example.com", tfPassword)
	a.origin = "http://localhost.evil.example"
	assertion, csrf = s.passkeyStep(a)
	a.origin = pkOrigin
	if st, env := s.passkeyFinish(csrf, pkOrigin, assertion); st != http.StatusUnauthorized || envCode(env) != "invalid_credential" {
		t.Fatalf("clientData origin: %d %v", st, env)
	}
	// RP ID hash of another site.
	s.login("neg@example.com", tfPassword)
	a.rpID = "example.com"
	assertion, csrf = s.passkeyStep(a)
	a.rpID = pkRPID
	if st, env := s.passkeyFinish(csrf, pkOrigin, assertion); st != http.StatusUnauthorized || envCode(env) != "invalid_credential" {
		t.Fatalf("rp id hash: %d %v", st, env)
	}
	// Expired ceremony.
	s.login("neg@example.com", tfPassword)
	assertion, csrf = s.passkeyStep(a)
	s.app.Deps.DB.Exec(`UPDATE webauthn_challenges SET expires_at = '2000-01-01T00:00:00Z'`)
	if st, env := s.passkeyFinish(csrf, pkOrigin, assertion); st != http.StatusUnauthorized || envCode(env) != "ceremony_expired" {
		t.Fatalf("expired ceremony: %d %v", st, env)
	}
	// Only the challenge hash is stored, never the challenge.
	s.login("neg@example.com", tfPassword)
	assertion, csrf = s.passkeyStep(a)
	var stored string
	s.app.Deps.DB.QueryRow(`SELECT challenge_hash || session_data FROM webauthn_challenges`).Scan(&stored)
	var cd struct{ Challenge string }
	raw, _ := b64.DecodeString(assertion["response"].(map[string]any)["clientDataJSON"].(string))
	json.Unmarshal(raw, &cd)
	if cd.Challenge == "" || strings.Contains(stored, cd.Challenge) {
		t.Fatal("challenge stored in clear")
	}
	// A ceremony is bound to the pending sign-in that began it (fifth failed
	// second step of this user in the window).
	other := s.client()
	other.login("neg@example.com", tfPassword)
	_, otherPage := other.do("GET", auth.SecondFactorPath, nil)
	if st, env := other.passkeyFinish(csrfFrom(t, otherPage), pkOrigin, assertion); st != http.StatusUnauthorized || envCode(env) != "ceremony_expired" {
		t.Fatalf("ceremony of another pending sign-in: %d %v", st, env)
	}
	// The per-user limit shared with codes now refuses even a valid assertion.
	if st, env := s.passkeyFinish(csrf, pkOrigin, assertion); st != http.StatusTooManyRequests || envCode(env) != "locked" {
		t.Fatalf("rate limit: %d %v", st, env)
	}
	s.app.Deps.DB.Exec(`DELETE FROM login_attempts`) // the 15-minute window passes
	s.login("neg@example.com", tfPassword)
	assertion, csrf = s.passkeyStep(a)
	if st, env := s.passkeyFinish(csrf, pkOrigin, assertion); st != 200 {
		t.Fatalf("sign-in after the window: %d %v", st, env)
	}
	s.logout()
	if failed() < 5 {
		t.Fatalf("failures audited: %d", failed())
	}
}

func TestPasskeyOnlyUserRenewsRecoveryCodes(t *testing.T) {
	s := boot(t)
	newUser(t, s, "renew@example.com", "editor")
	s.login("renew@example.com", tfPassword)
	codes, _ := s.addPasskey(newSoftAuthenticator(t), "Key")["codes"].([]any)
	_, page := s.do("GET", "/admin/account/two-factor/passkeys", nil)
	if !strings.Contains(page, `id="pk-regen-code"`) {
		t.Fatal("no way to renew recovery codes without the app")
	}
	r, page := s.do("POST", "/admin/account/two-factor/recovery-codes", url.Values{"_csrf": {csrfFrom(t, page)}, "code": {codes[0].(string)}})
	if r.StatusCode != 200 || len(recoveryRe.FindAllString(page, -1)) != 10 {
		t.Fatalf("renew with a recovery code: %d", r.StatusCode)
	}
}

func TestPasskeySignCountRegressionRefused(t *testing.T) {
	s := boot(t)
	newUser(t, s, "clone@example.com", "editor")
	s.login("clone@example.com", tfPassword)
	a := newSoftAuthenticator(t)
	s.addPasskey(a, "Key")
	s.logout()
	a.counter = 10
	s.login("clone@example.com", tfPassword)
	assertion, csrf := s.passkeyStep(a) // counter 11
	if st, _ := s.passkeyFinish(csrf, pkOrigin, assertion); st != 200 {
		t.Fatalf("first sign-in: %d", st)
	}
	s.logout()
	// A copy of the key with an older counter.
	a.counter = 4
	s.login("clone@example.com", tfPassword)
	assertion, csrf = s.passkeyStep(a) // counter 5 < 11
	st, env := s.passkeyFinish(csrf, pkOrigin, assertion)
	if st != http.StatusUnauthorized || envCode(env) != "sign_count" {
		t.Fatalf("counter regression: %d %v", st, env)
	}
	if r, _ := s.do("GET", "/admin", nil); r.StatusCode == 200 {
		t.Fatal("signed in after a counter regression")
	}
	var count int
	s.app.Deps.DB.QueryRow(`SELECT sign_count FROM user_passkeys`).Scan(&count)
	var meta string
	s.app.Deps.DB.QueryRow(`SELECT meta_json FROM audit_log WHERE action = 'user.passkey_failed' ORDER BY id DESC LIMIT 1`).Scan(&meta)
	if count != 11 || !strings.Contains(meta, "sign_count") {
		t.Fatalf("stored counter %d, audit %s", count, meta)
	}
}

func TestPasskeyRegistrationRefusals(t *testing.T) {
	s := boot(t)
	newUser(t, s, "reg@example.com", "editor")
	s.login("reg@example.com", tfPassword)
	_, page := s.do("GET", "/admin/account/two-factor/passkeys", nil)
	csrf := csrfFrom(t, page)
	if st, env := s.postJSON("/admin/account/two-factor/passkeys/register/begin", csrf, "https://evil.example", nil); st != http.StatusForbidden {
		t.Fatalf("begin from another origin: %d %v", st, env)
	}
	if st, _ := s.postJSON("/admin/account/two-factor/passkeys/register/begin", "", pkOrigin, nil); st != http.StatusForbidden {
		t.Fatalf("begin without CSRF: %d", st)
	}
	a := newSoftAuthenticator(t)
	_, env := s.postJSON("/admin/account/two-factor/passkeys/register/begin", csrf, pkOrigin, nil)
	cred := a.create(envData(t, env))
	if st, env := s.postJSON("/admin/account/two-factor/passkeys/register/finish", csrf, pkOrigin, map[string]any{"name": "a\nb", "credential": cred}); st != http.StatusUnprocessableEntity {
		t.Fatalf("bad name: %d %v", st, env)
	}
	if st, _ := s.postJSON("/admin/account/two-factor/passkeys/register/finish", csrf, pkOrigin, map[string]any{"name": "ok", "credential": cred}); st != 200 {
		t.Fatalf("finish: %d", st)
	}
	// Same response again: the ceremony is gone.
	if st, env := s.postJSON("/admin/account/two-factor/passkeys/register/finish", csrf, pkOrigin, map[string]any{"name": "ok", "credential": cred}); st != http.StatusBadRequest || envCode(env) != "ceremony_expired" {
		t.Fatalf("replayed registration: %d %v", st, env)
	}
	// The same credential registered again (new ceremony) is refused.
	_, env = s.postJSON("/admin/account/two-factor/passkeys/register/begin", csrf, pkOrigin, nil)
	opts := envData(t, env)
	if ex, _ := opts["publicKey"].(map[string]any)["excludeCredentials"].([]any); len(ex) != 1 {
		t.Fatalf("excludeCredentials: %v", opts)
	}
	if st, env := s.postJSON("/admin/account/two-factor/passkeys/register/finish", csrf, pkOrigin, map[string]any{"credential": a.create(opts)}); st != http.StatusConflict {
		t.Fatalf("duplicate credential: %d %v", st, env)
	}
	// Without a recent password confirmation registration is sent to reauth.
	s.app.Deps.DB.Exec(`UPDATE sessions SET reauth_at = '2000-01-01T00:00:00Z'`)
	if st, env := s.postJSON("/admin/account/two-factor/passkeys/register/begin", csrf, pkOrigin, nil); st != http.StatusSeeOther || !strings.HasPrefix(env["location"].(string), "/admin/reauth") {
		t.Fatalf("begin without recent auth: %d %v", st, env)
	}
}

// Removing a passkey weakens the sign-in, so even a fresh session must enter
// the current password (as for turning off the app).
func TestPasskeyRemovalNeedsThePassword(t *testing.T) {
	s := boot(t)
	newUser(t, s, "rm@example.com", "editor")
	s.login("rm@example.com", tfPassword)
	s.addPasskey(newSoftAuthenticator(t), "Key")
	var id string
	s.app.Deps.DB.QueryRow(`SELECT id FROM user_passkeys`).Scan(&id)
	_, page := s.do("GET", "/admin/account/two-factor/passkeys", nil)
	csrf := csrfFrom(t, page)
	if !strings.Contains(page, `action="/admin/account/two-factor/passkeys/`+id+`/remove"`) || !strings.Contains(page, `id="pk-remove-`+id+`"`) {
		t.Fatal("no password field on the remove form")
	}
	remove := func(pid, password string) int {
		r, _ := s.do("POST", "/admin/account/two-factor/passkeys/"+pid+"/remove", url.Values{"_csrf": {csrf}, "password": {password}})
		return r.StatusCode
	}
	if st := remove(id, ""); st != http.StatusUnauthorized {
		t.Fatalf("remove without the password: %d", st)
	}
	if st := remove(id, "wrong password!"); st != http.StatusUnauthorized {
		t.Fatalf("remove with a wrong password: %d", st)
	}
	// Another user's passkey id does nothing.
	other := newUser(t, s, "other@example.com", "editor")
	s.app.Deps.DB.Exec(`INSERT INTO user_passkeys (id, user_id, rp_id, credential_id, public_key, name, created_at) VALUES ('theirs', ?, 'localhost', x'aa', x'bb', 'Theirs', '2026-01-01T00:00:00Z')`, other.ID)
	remove("theirs", tfPassword)
	var n int
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM user_passkeys WHERE id = 'theirs'`).Scan(&n)
	if n != 1 {
		t.Fatal("removed another user's passkey")
	}
	if st := remove(id, tfPassword); st != http.StatusSeeOther {
		t.Fatalf("remove: %d", st)
	}
	var left, codes int
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM user_passkeys WHERE id = ?`, id).Scan(&left)
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM user_recovery_codes`).Scan(&codes)
	var meta string
	s.app.Deps.DB.QueryRow(`SELECT meta_json FROM audit_log WHERE action = 'user.passkey_removed'`).Scan(&meta)
	if left != 0 || codes != 0 || !strings.Contains(meta, `"two_factor_off":true`) {
		t.Fatalf("after removal: %d passkeys, %d recovery codes, audit %s", left, codes, meta)
	}
	// Without a factor the password signs in directly again.
	s.logout()
	if r := s.login("rm@example.com", tfPassword); r.Header.Get("Location") != "/admin" {
		t.Fatalf("login after removing the last passkey: %s", r.Header.Get("Location"))
	}
}

func TestPasskeySatisfiesRoleEnforcementAndResetRemovesIt(t *testing.T) {
	s := boot(t)
	u := newUser(t, s, "req@example.com", "author")
	if err := s.app.Deps.Settings.Set(context.Background(), auth.TwoFactorRolesSetting, []string{"author"}); err != nil {
		t.Fatal(err)
	}
	s.login("req@example.com", tfPassword)
	if r, _ := s.do("GET", "/admin", nil); r.Header.Get("Location") != "/admin/account/two-factor" {
		t.Fatalf("not held for enrolment: %d", r.StatusCode)
	}
	// The passkey routes are reachable while held, and a passkey satisfies the rule.
	s.addPasskey(newSoftAuthenticator(t), "Phone")
	if r, _ := s.do("GET", "/admin", nil); r.StatusCode != 200 {
		t.Fatalf("still held with a passkey: %d", r.StatusCode)
	}
	// The last usable factor of a required role cannot be removed; a passkey
	// made for another site address does not count as one.
	var id string
	s.app.Deps.DB.QueryRow(`SELECT id FROM user_passkeys`).Scan(&id)
	s.app.Deps.DB.Exec(`INSERT INTO user_passkeys (id, user_id, rp_id, credential_id, public_key, name, created_at) VALUES ('old', ?, 'old.example', x'aa', x'bb', 'Old', '2026-01-01T00:00:00Z')`, u.ID)
	_, page := s.do("GET", "/admin/account/two-factor/passkeys", nil)
	if !strings.Contains(page, "data-passkey-other-site") {
		t.Fatal("passkey of another site address not marked")
	}
	if r, _ := s.do("POST", "/admin/account/two-factor/passkeys/"+id+"/remove", url.Values{"_csrf": {csrfFrom(t, page)}, "password": {tfPassword}}); r.StatusCode != http.StatusForbidden {
		t.Fatalf("last required factor removed: %d", r.StatusCode)
	}
	if r, _ := s.do("POST", "/admin/account/two-factor/passkeys/old/remove", url.Values{"_csrf": {csrfFrom(t, page)}, "password": {tfPassword}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("unusable passkey not removable: %d", r.StatusCode)
	}
	if ok, _ := auth.TwoFactorEnabled(context.Background(), s.app.Deps.DB, u.ID); !ok {
		t.Fatal("a passkey does not count as two-factor")
	}
	// The administrator's reset removes passkeys too.
	if err := auth.ResetTwoFactor(context.Background(), s.app.Deps, u.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM user_passkeys WHERE user_id = ?`, u.ID).Scan(&n)
	var meta string
	s.app.Deps.DB.QueryRow(`SELECT meta_json FROM audit_log WHERE action = 'user.2fa_reset'`).Scan(&meta)
	if n != 0 || !strings.Contains(meta, `"passkeys":1`) {
		t.Fatalf("after reset: %d passkeys, audit %s", n, meta)
	}
}

// A required role may turn off the app while a usable passkey remains; the
// recovery codes stay for the passkey.
func TestRequiredRoleSwitchesFromAppToPasskey(t *testing.T) {
	s := boot(t)
	newUser(t, s, "switch@example.com", "author")
	s.app.Deps.Settings.Set(context.Background(), auth.TwoFactorRolesSetting, []string{"author"})
	s.login("switch@example.com", tfPassword)
	key, _ := s.enrol()
	_, page := s.do("GET", "/admin/account/two-factor", nil)
	disable := func(code string) int {
		r, _ := s.do("POST", "/admin/account/two-factor/disable", url.Values{"_csrf": {csrfFrom(t, page)}, "password": {tfPassword}, "code": {code}})
		return r.StatusCode
	}
	if st := disable(auth.TOTPCodeForTest(key, time.Now().Add(30*time.Second))); st != http.StatusForbidden {
		t.Fatalf("required app factor disabled without a passkey: %d", st)
	}
	s.addPasskey(newSoftAuthenticator(t), "Key")
	if st := disable(auth.TOTPCodeForTest(key, time.Now().Add(30*time.Second))); st != http.StatusSeeOther {
		t.Fatalf("disable with a passkey left: %d", st)
	}
	var codes int
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM user_recovery_codes WHERE used_at = ''`).Scan(&codes)
	if codes != 10 {
		t.Fatalf("recovery codes after turning off the app: %d", codes)
	}
	if r, _ := s.do("GET", "/admin", nil); r.StatusCode != 200 {
		t.Fatalf("held for enrolment with a passkey: %d", r.StatusCode)
	}
}

func TestPasskeysUnavailableWithoutSecureBaseURL(t *testing.T) {
	s := bootCfgPlainHTTP(t)
	newUser(t, s, "http@example.com", "editor")
	s.login("http@example.com", tfPassword)
	r, page := s.do("GET", "/admin/account/two-factor/passkeys", nil)
	if r.StatusCode != 200 || !strings.Contains(page, "data-passkeys-unavailable") || strings.Contains(page, "data-passkey-register") {
		t.Fatalf("passkeys offered on plain http: %d", r.StatusCode)
	}
	if st, env := s.postJSON("/admin/account/two-factor/passkeys/register/begin", csrfFrom(t, page), "http://example.com", nil); st != http.StatusConflict || envCode(env) != "passkeys_unavailable" {
		t.Fatalf("begin on plain http: %d %v", st, env)
	}
}
