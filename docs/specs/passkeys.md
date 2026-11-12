# Spec: Passkeys (WebAuthn) and a QR code for TOTP enrolment

Status: in review. Builds on the TOTP two-factor sign-in of 0.22.0
([docs/LOGIN-SECURITY.md](../LOGIN-SECURITY.md#two-factor-authentication-totp)). The bar is the one the
0.22.0 security review set for `internal/auth`: server-side state, hashed single-use tokens, constant-time
comparisons, persistent rate limits, every security event audited, no secrets in logs.

## Objective

Staff who sign in to the admin get two improvements to the second factor:

1. **QR code for TOTP enrolment.** The enrolment page draws the `otpauth://` URI as a QR code, so a phone
   authenticator can scan it from a desktop screen. The setup key and the link stay for manual entry.
2. **Passkeys (WebAuthn) as a second factor.** A user registers one or more passkeys (platform authenticator,
   phone, security key) from My account → Two-factor authentication, names them, sees when each was added and
   last used, and removes them. On the sign-in step after the password (or magic link, or shop form) a user with
   a passkey can use it instead of typing a code. A passkey counts as two-factor for per-role enforcement.
   Administrator reset and `gorun user two-factor-reset` remove passkeys too.

Capability map (two independently testable modules; build order `totp-qr` → `passkeys`):

| Module id | Responsibility | Depends on |
|---|---|---|
| `totp-qr` | Server-side inline SVG QR of the enrolment URI | existing TOTP enrolment |
| `passkeys` | WebAuthn registration, second-factor assertion, storage, enforcement, reset | existing pending sign-in (`login_challenges`), login limiter, reauth |

## Assumptions

1. The public origin of the instance is `GORUN_BASE_URL` (`config.Config.BaseURL`, required and non-localhost
   in prod). It is the only source for the WebAuthn RP ID and origin; the `site.base_url` setting and request
   headers never decide it (same reasoning as the session cookie's `Secure` flag in SECURITY.md).
2. Passkeys are a **second factor** here: the password (or magic link) stays the first factor.
3. A browser without WebAuthn, or an instance whose base URL cannot host WebAuthn (plain http on a real host, or an
   IP address), simply does not offer passkeys; TOTP and recovery codes are unaffected.
4. The admin layout's CSP allows same-origin scripts (`script-src 'self' 'nonce-…'`), so a static
   `/admin/static/passkeys.js` file needs no CSP change.

## Scope

### totp-qr

- Encoder: `rsc.io/qr` v0.2.0 (see [Dependencies](#dependency-decisions)), error-correction level M.
- `internal/auth/totp_qr.go`: `qrSVG(text) (template.HTML, error)` returns one `<svg>` element with a white
  quiet zone of 4 modules, `viewBox` in module units, one `<path>` whose `d` contains one `M x yh1v1h-1z` run
  per horizontal run of dark modules, `shape-rendering="crispEdges"`, `role="img"` and an `aria-label`.
  Inline SVG: no `data:` URI, no external request, no script, so CSP is untouched.
- The enrolment page shows the QR above the setup key; the text changes from "no QR code" to "scan, or enter
  the key". The SVG uses fixed black on white (scanners need contrast) in both themes. The page keeps
  `Cache-Control: no-store` and `hx-history="false"` (the QR is the secret).
- If encoding fails (it cannot for a URI of this size, but the error is handled) the page omits the QR and keeps
  the key.

### passkeys

**Relying Party.** `webauthnRP(baseURL)` derives:

- RP ID = host of `GORUN_BASE_URL` (lower-case, no port). Refused (passkeys unavailable) when the host is an IP
  address or empty: an RP ID must be a valid domain ([WebAuthn L3 §5.1.3 step 7][s-create]).
- Origin = `scheme://host[:port]` of the base URL (default ports dropped, internationalized hosts in punycode, as
  browsers serialize origins). Allowed only for `https`, or `http` when the host is
  `localhost` or ends in `.localhost` (WebAuthn is exposed only in secure contexts, and `http://localhost` is the
  one plain-http origin a client accepts: [L3 §4 "Relying Party Identifier"][s-rpid], [§5][s-api]).
- Every WebAuthn endpoint first checks the request's `Origin` header equals that origin (403 otherwise, audited
  as `user.passkey_failed` with reason `origin` when a user is known), then the library verifies the origin in
  `clientDataJSON` and the RP ID hash in the authenticator data ([L3 §7.1][s-reg], [§7.2][s-assert]).
- `RPOrigins` holds exactly that one origin; cross-origin and top-origin use stay disabled (library defaults).
- Attestation conveyance `none` (we do not keep an authenticator allow-list); credential algorithms are the
  library defaults (ES256, EdDSA, RS256 …).
- Authenticator selection: `residentKey: preferred`, `userVerification: preferred`. Preferred resident keys keep
  credentials usable for a later passwordless sign-in without re-registration.

**User handle.** One random 64-byte handle per user in `webauthn_users`, created on the first registration,
never the user id or email ([L3 §4 "User Handle"][s-rpid]: at most 64 bytes, "MUST NOT contain personally
identifying information"; library guidance: random, use all 64 bytes, store once per user, not on the credential
row). `user.name` = email, `user.displayName` = name (or email).

**Storage** (`auth/0014_passkeys.sql`, additive):

```sql
CREATE TABLE webauthn_users (user_id PK → users ON DELETE CASCADE, handle BLOB UNIQUE, created_at)
CREATE TABLE user_passkeys (
  id TEXT PK, user_id → users ON DELETE CASCADE, rp_id, credential_id BLOB UNIQUE,
  public_key BLOB, sign_count INTEGER, transports TEXT (comma list), aaguid BLOB,
  flags INTEGER (protocol flag octet: UV, BE, BS), attestation_type, attestation_format,
  name, created_at, last_used_at)
CREATE TABLE webauthn_challenges (
  challenge_hash TEXT PK, kind ('register'|'login'), user_id → users ON DELETE CASCADE,
  binding TEXT, session_data TEXT, expires_at, created_at)
```

- `credential_id` is unique across all users ([L3 §7.1 step 26][s-reg]: "Verify that the credentialId is not
  yet registered for any user") and at most 1023 bytes (step 25).
- Credentials are partitioned by `rp_id` (library Storage guidance): only rows whose `rp_id` equals the current
  RP ID are offered or accepted. Rows from an old base URL stay listed (marked) so the user can remove them.
- The public key is not secret; it is stored in clear like the library's reference schema. The credential row
  grants nothing without the private key, which never leaves the authenticator.

**Challenge sessions.** The library returns a `SessionData` with a 32-byte random challenge
(`protocol.DefaultChallengeLength`; [L3 §13.4.3][s-chal] asks for at least 16 random bytes). GoRun stores it
server-side:

- Key: SHA-256 (hex) of the challenge. The challenge itself is removed from the stored JSON, so a database
  copy does not hold usable challenges.
- `binding`: for `register`, the SHA-256 of the session token (the `sessions.id`); for `login`, the
  `login_challenges.token_hash` of the pending sign-in. A ceremony finishes only from the same browser session /
  pending sign-in that began it.
- Expiry: 5 minutes, also enforced by the library (`Timeouts.*.Enforce`).
- Single use: Finish parses the response, hashes `clientDataJSON.challenge` and **deletes** the matching row
  (`DELETE … WHERE challenge_hash = ? AND kind = ? AND binding = ? AND expires_at > now RETURNING …`) before any
  verification, so a replayed or concurrent second response finds nothing. The stored `SessionData` is restored
  with the challenge from the client, which is safe because its hash matched a row we issued.
- Begin deletes earlier unfinished rows of the same kind and binding (at most one open ceremony each); expired
  rows go in the hourly `auth.cleanup`.

**Registration** (`/admin/account/two-factor/passkeys/…`, session users only, CSRF header):

- `POST …/register/begin` needs a recent sign-in or password confirmation (`RequireRecentAuth`, 10 minutes,
  like starting TOTP). Returns the creation options JSON with `excludeCredentials` = the user's passkeys for this
  RP ID.
- `POST …/register/finish` body `{"name": "...", "credential": {…}}` (64 KiB cap). Name: trimmed, 1–64 runes,
  no control characters; empty → "Passkey". Verifies with `CreateCredential`, refuses a known credential id,
  stores the row, audits `user.passkey_added` (`{"passkey_id","name"}`).
- When the user holds no unused recovery codes after the passkey is stored (normally: their first second factor),
  the response carries ten fresh recovery codes, shown once by the page (same codes, hashing and rules as TOTP).
- `POST …/{id}/remove` asks for the current password in the form (`confirmPassword`, rate-limited like sign-in;
  password-less accounts need a recent sign-in). Changed after the adversarial review: `RequireRecentAuth` alone
  passes for any session younger than 10 minutes, and removing a factor weakens the sign-in, so it gets the same
  bar as turning off the app (which asks for the password and a code). Only the owner's rows. Refused when the
  user's role requires two-factor and no other usable factor (app, or a passkey of the current RP ID) remains;
  check and delete are one statement in one transaction. Removing the last factor also deletes the recovery codes.
  Audit `user.passkey_removed` (`two_factor_off` when nothing is left).
- Users whose only factor is a passkey renew recovery codes with one of their recovery codes. A required role may
  turn off the app while a usable passkey remains.
- Users held by role enforcement can reach these routes (they live under the enrolment path).

**Second-factor sign-in** (`/admin/login/2fa/passkey/begin|finish`, public admin paths, double-submit CSRF):

- Both need the `gp_2fa` pending sign-in cookie (path `/admin/login/2fa` covers them) and a live challenge row.
- Begin returns request options with `allowCredentials` = the user's passkeys for this RP ID and
  `userVerification: preferred`; the WebAuthn challenge row is bound to the pending sign-in.
- Finish counts the attempt in the same persistent limiter as codes (5 per user, 30 per IP / 15 min, shared
  with TOTP so switching methods gains nothing), consumes the WebAuthn challenge, verifies with `ValidateLogin`
  (origin, RP ID hash, challenge, user presence, signature, backup-flag consistency).
- **Sign-count regression.** If the stored or new counter is non-zero and the new one is not greater, the library
  sets `CloneWarning` ([L3 §6.1.1][s-count]: possible cloned authenticator; the RP decides). GoRun refuses the
  sign-in, keeps the stored counter, and audits `user.passkey_failed` with reason `sign_count`.
- Failure: the pending sign-in's attempt counter goes up (dies after 5) and `user.passkey_failed` is audited
  (reason `invalid`, `locked`, `origin`, `sign_count`, `expired`). Never the credential or assertion bytes.
- Success: counter, flags and `last_used_at` are written back, the pending sign-in is consumed (single use),
  the session starts with the pending sign-in's method, `user.passkey_used` is audited, the JSON answer names the
  same destination as the code form (`next`, or onboarding).

**Two-factor state.** `TwoFactorEnabled` (and the session's cached flag) becomes "TOTP confirmed **or** at least
one passkey". So: a pending sign-in is required, role enforcement is satisfied, and the admin user page offers
**Reset two-factor**. Recovery codes work for passkey-only users. Disabling TOTP keeps recovery codes while
passkeys remain. Enabling TOTP replaces any existing recovery codes with a new set.

**Reset.** `ResetTwoFactor` (admin, audited `user.2fa_reset`) and `gorun user two-factor-reset` delete the TOTP
factor, recovery codes, pending sign-ins, passkeys and WebAuthn challenges in one transaction; the audit meta adds
`{"passkeys": n}`; the CLI prints how many passkeys were removed.

**Front end** (`web/admin/static/passkeys.js`, no framework): feature-detects `window.PublicKeyCredential`,
converts the JSON options (base64url ↔ `ArrayBuffer`), calls `navigator.credentials.create/get`, posts the
result as JSON with `X-CSRF-Token`, then follows the returned redirect or shows the error. Unsupported browsers
see a short explanation instead of the button. Cancellation (`NotAllowedError`) shows "cancelled", not an error.

### Deferred: passwordless (primary) sign-in

Not in this change. Reasons:

- It adds an unauthenticated ceremony endpoint (discoverable login) that creates server state for anyone; it needs
  its own budget and abuse handling, separate from the pending sign-in that bounds the second-factor flow today.
- It changes what a "factor" is: a passkey with user verification would replace password **and** code. That needs
  a policy decision (require `userVerification: required`; how it meets role enforcement; whether magic links and
  the shop forms get it) and its own review.
- Conditional UI on the login page, account enumeration behaviour and the audit vocabulary need design.

Registrations made now prefer resident keys, so the same passkeys can later be used passwordless without
re-registering.

## Dependency decisions

| Need | Choice | Why |
|---|---|---|
| WebAuthn RP verification | `github.com/go-webauthn/webauthn` **v0.18.2** (2026-09-19) | De-facto Go library, BSD-3-Clause, conformance-tested, follows L3, actively maintained (several releases in 2026). Writing CBOR/COSE/attestation parsing in-house would be a larger security risk than the dependency. |
| QR encoding | `rsc.io/qr` **v0.2.0** | ~1.2k lines, BSD-3-Clause (The Go Authors), pure Go, no dependencies, by Russ Cox. Unmaintained since 2017 but the QR format (ISO/IEC 18004) is frozen and the package has no network, parsing of untrusted input or unsafe code; it only encodes our own URI. An in-house encoder would be ~400 lines of Reed–Solomon and placement code to own. |

New modules in the build graph (all pinned in `go.mod`/`go.sum`): `go-webauthn/webauthn` v0.18.2,
`go-webauthn/x` v0.3.1 (BSD-3), `fxamacker/cbor/v2` v2.9.4 (MIT), `x448/float16` v0.8.4 (MIT),
`golang-jwt/jwt/v5` v5.3.1 (MIT, metadata service code), `google/go-tpm` v0.9.8 (Apache-2.0, TPM attestation),
`go-viper/mapstructure/v2` v2.5.0 (MIT), `tinylib/msgp` v1.6.4 (MIT), `philhofer/fwd` v1.2.0 (MIT),
`rsc.io/qr` v0.2.0. Already present: `google/uuid`, `golang.org/x/crypto`. `go-tpm` is Apache-2.0 and is
listed in `NOTICE` with its license under `docs/licenses/` like `minio/sio`. `govulncheck` (CI's pinned
v1.9.0) must be clean. CONTRIBUTING reserves `go.mod` edits for the maintainer coordinating a change; this
change was requested by the maintainer and the PR says so.

## Tech stack

Go 1.27, chi, SQLite (`modernc.org/sqlite`), html/template, HTMX/Alpine admin, Playwright (`e2e/`).

## Commands

```sh
gofmt -l . && go vet ./...
go test -race ./internal/auth/... ./internal/admin/... ./pkg/cli/...
go test ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
python3 tools/docs-check.py
node tools/admin-icons/update.mjs --check
cd e2e && npx playwright test global.setup accounts passkeys
```

## Project structure

- `internal/auth/totp_qr.go` (+ test): QR SVG.
- `internal/auth/passkeys.go`: RP config, store (credentials, user handle, challenges).
- `internal/auth/passkeys_handlers.go`: registration, removal, second-factor endpoints.
- `internal/auth/migrations/0014_passkeys.sql`.
- `internal/auth/twofactor*.go`, `sessions.go`, `session_start.go`: two-factor state and reset.
- `web/admin/templates/pages/auth/two-factor-setup.html`, `two-factor.html`, `account.html` (passkey summary only).
- `web/admin/static/passkeys.js`, `account.css`.
- `internal/auth/catalog/{en,ar}.json`: strings.
- `e2e/tests/passkeys.spec.ts`: own server on `http://localhost:<port>` (the shared suite server is
  `127.0.0.1`, which cannot be an RP ID, and a passkey on the shared admin would change every later sign-in).

## Code style

Follows `internal/auth`: small store type over `*sql.DB`, errors wrapped with `auth:`, `db.InTx` for multi-row
changes, audit after commit, `deny`/`serverError` helpers, `Cache-Control: no-store` on secret-bearing pages.

## Testing strategy

Go unit/integration tests with real SQLite (`apitest`/`kernel.Boot` helpers already in the package) and a
minimal **software authenticator** in `passkeys_test.go` (ECDSA P-256 key, CBOR attestation object with `none`
format, assertions with an explicit counter) that drives the real HTTP handlers:

- registration end to end (stored row, name, transports, user handle random and stable);
- second-factor sign-in end to end (session started, counter written, `user.passkey_used`);
- wrong origin (header and `clientDataJSON`) refused; wrong RP ID hash refused;
- replayed challenge (same response twice) refused; expired challenge refused; challenge bound to another
  pending sign-in refused;
- sign-count regression refused and audited;
- removal without a recent password confirmation redirected to reauth; with it removed and audited;
- enforcement: a role-required user with only a passkey is not held on the enrolment page;
- admin reset and the CLI reset remove passkeys;
- RP derivation table (https, http localhost, http real host, IP, port);
- QR: SVG structure, module grid equals the encoder's bitmap, and a test-only decoder reads the URI back.

Playwright (Chromium virtual authenticator via CDP `WebAuthn.enable` + `WebAuthn.addVirtualAuthenticator`):
register a passkey from My account, sign out, sign in with password + passkey; QR visible on enrolment.

## Boundaries

- Always: server-side state, hashed single-use challenges, persistent limiter, audit without secrets, tests first.
- Ask first: passwordless sign-in, attestation allow-lists / FIDO MDS, changing role-enforcement semantics.
- Never: trust request headers for the RP origin, store the user id/email as the user handle, log credential
  or assertion bytes, add external requests or `data:` URIs for the QR.

## Success criteria

1. Enrolment page shows a scannable QR (decoded back to the URI in tests) plus the key and link.
2. A user can add, list (name, created, last used) and remove passkeys; removal needs recent password confirmation.
3. After the password, a user with a passkey can finish sign-in with it; all refusal cases above hold.
4. Role enforcement accepts a passkey; admin and CLI resets remove passkeys.
5. `go vet`, `go test ./...`, govulncheck, docs check, icon check and the affected Playwright specs pass.
6. Docs: LOGIN-SECURITY (passkeys, QR, recovery), SECURITY summary, CHANGELOG (Added + Upgrade notes listing the
   migration and dependencies).

## Review log

Doubt-driven review (fresh-context adversarial reviewer, single model; cross-model skipped: non-interactive run)
found 12 issues. Fixed: removal now asks for the password (was: recent session only); last-factor check made
atomic and limited to usable passkeys; default port and IDN origins; recovery-code renewal for passkey-only users;
required roles may switch from the app to a passkey; `user.2fa_disabled` only when something was removed;
first-passkey recovery codes never replace an existing set; duplicate credential checked before insert; bidi
control characters refused in names; expired pending sign-in in the browser returns to its explanation page.
Accepted as trade-offs: adding a passkey needs only a recent sign-in (same bar as starting TOTP enrolment);
passkeys of an old RP ID still count as "has two-factor" for the sign-in step, so a domain change never silently
drops the second step (they do not count for the last-factor check).

Second cycle (9 findings). Fixed: turning off the app for a required role is now guarded inside the DELETE (a
parallel passkey removal could have left no factor); recovery codes are issued whenever the user holds none after
adding a passkey (closes a register/remove race and gives passkey-only users without codes a way to get new
ones); with passkeys unavailable no passkey counts for the last-factor check; removing the last passkey also drops
pending sign-ins and open ceremonies; the app cannot be "disabled" (spending a code) when it is not enrolled; names
refuse all format characters (Cf: zero-width, bidi, BOM, tags) and noncharacters; ASCII hosts skip IDNA; ports are
serialized canonically; an Origin mismatch on the sign-in step is audited but no longer uses up the pending
sign-in's attempts. Accepted: renewing recovery codes with a passkey assertion (instead of a recovery code) is
deferred; a settings read error makes role enforcement fail open, as everywhere else `auth.two_factor_roles` is
read; enabling the app replaces a passkey-issued code set (the new set is shown at once, as before).
Stop condition: two cycles, remaining findings trivial or accepted.

## Open questions

- Passwordless sign-in (deferred above).
- Should administrators be able to remove a single passkey of another user (today: full reset only)?

## Sources

- W3C Web Authentication Level 3: [§4 Terminology (RP ID, user handle)][s-rpid], [§5 Web Authentication API][s-api],
  [§5.1.3 Create a new credential][s-create], [§6.1.1 Signature counter considerations][s-count],
  [§7.1 Registering a new credential][s-reg], [§7.2 Verifying an authentication assertion][s-assert],
  [§13.4.3 Cryptographic challenges][s-chal], [§14.6.1 User handle contents][s-uh].
- go-webauthn v0.18.2 package documentation (Storage, Origin Binding, `Config`, `User`, `Authenticator.UpdateCounter`):
  <https://pkg.go.dev/github.com/go-webauthn/webauthn@v0.18.2/webauthn>, README "Credential Record":
  <https://github.com/go-webauthn/webauthn/blob/v0.18.2/README.md#credential-record>.
- `rsc.io/qr` v0.2.0: <https://pkg.go.dev/rsc.io/qr@v0.2.0>.
- Chrome DevTools Protocol WebAuthn domain: <https://chromedevtools.github.io/devtools-protocol/tot/WebAuthn/>.

Quoted spec text above was checked against the published Level 3 document (§4, §5.1.3, §6.1.1, §7.1). §7.2,
§13.4.3 and §14.6.1 are cited by anchor; their requirements are applied through the library, whose source
comments quote §7.2 step 17 (sign counter).

[s-rpid]: https://www.w3.org/TR/webauthn-3/#terminology
[s-api]: https://www.w3.org/TR/webauthn-3/#sctn-api
[s-create]: https://www.w3.org/TR/webauthn-3/#sctn-createCredential
[s-count]: https://www.w3.org/TR/webauthn-3/#sctn-sign-counter
[s-reg]: https://www.w3.org/TR/webauthn-3/#sctn-registering-a-new-credential
[s-assert]: https://www.w3.org/TR/webauthn-3/#sctn-verifying-assertion
[s-chal]: https://www.w3.org/TR/webauthn-3/#sctn-cryptographic-challenges
[s-uh]: https://www.w3.org/TR/webauthn-3/#sctn-user-handle-privacy
