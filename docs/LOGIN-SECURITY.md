# Login audit and temporary IP restrictions

The native **Login security** screen at `/admin/security/login-attempts` is available to active administrator sessions with `security.manage`. Editors and API keys cannot manage restrictions. It shows the latest 50 failed attempts and active site-local restrictions. The submitted email is an attempted identity, not proof of who connected.

A recent, verified-source failure can be restricted for one hour, 24 hours or seven days. Only individual public IPv4/IPv6 addresses are accepted; CIDRs, local/private addresses, the administrator's current address and configured trusted proxy addresses are rejected. A shared address can represent several people. Expiry is automatic; unblock forms are bound to the displayed expiry so an old form cannot remove a newer restriction. App routes (public, administration, API and webhooks) enforce the restriction. Kernel health/readiness probes remain available. This is application-level protection, not a firewall or Cloudflare rule.

Failed-login audit metadata contains a bounded, normalized attempted email and reason, never the password. The resolved address is recorded in the audit IP field with provenance. Older rows without source provenance remain visible but cannot directly create a block. Repeated failure events are sampled at 30 per address/minute and 120 total/minute. Blocked-request events are sampled once per address/minute, at most 60 total/minute. Existing persistent credential throttling is unchanged.

## Mandatory proxy configuration before production rollout

Forwarded headers are no longer implicitly trusted from all private/loopback networks. Empty configuration trusts **no** forwarding headers. Configure exact operator-controlled ingress hops with `GORUN_TRUSTED_PROXIES` (comma-separated IPs/CIDRs), or `trusted_proxies = [...]` in gorun.toml. Invalid entries and all-address `/0` networks prevent startup. Prefer environment configuration during a reversible rollout: an older binary ignores the added environment variable, whereas its strict TOML parser rejects a new key.

The resolver begins with the TCP peer and walks `X-Forwarded-For` right to left only while the current hop belongs to a configured trusted network. It stops at the first untrusted hop. Malformed or oversized chains are ignored completely. `CF-Connecting-IP` and arbitrary provider headers are ignored. `X-Forwarded-Proto` is accepted only from a configured immediate proxy; that proxy must overwrite/sanitize it.

Before deploying each instance, independently verify the real Traefik/container/Cloudflare chain, firewall reachability and forwarded-header sanitation. Configure only verified ingress networks; never copy broad example private ranges. If Cloudflare's edge is an intermediate hop, its observed current ranges and edge-to-origin header behavior must be validated by the operator. Otherwise the address may identify a proxy, not a visitor. No production trust configuration was changed by this implementation.

The additive `auth/0007_security_ip_blocks.sql` migration preserves existing users, sessions and audit rows. An older binary ignores the new table and therefore does **not** enforce these restrictions; rollback requires explicitly accounting for that security behavior.

## Two-factor authentication (TOTP)

Staff can add a second sign-in step from **My account → Two-factor authentication** (`/admin/account/two-factor`). It uses time-based one-time codes (RFC 6238: HMAC-SHA-1, 6 digits, 30-second steps), so any authenticator app works (Aegis, 2FAS, Google or Microsoft Authenticator, 1Password). HOTP/TOTP are implemented in `internal/auth/totp.go` with the standard library; there is no new dependency.

**Enrolment.** Starting needs a recent sign-in or password confirmation (`/admin/reauth`, 10 minutes). GoRun creates a random 20-byte secret and shows it three ways: a **QR code** of the `otpauth://totp/…` URI to scan with a phone, the grouped base32 key for manual entry, and the link itself for an authenticator app on the same device. The QR code is drawn on the server as one inline SVG (`rsc.io/qr`, error correction level M, four-module quiet zone, black on white in both themes): no script, image request or `data:` URI, so the Content-Security-Policy is unchanged. The page is `Cache-Control: no-store` and kept out of HTMX history, because the code is the secret: anyone who sees or photographs it can create codes. The factor is turned on only after a valid code for the secret; then ten recovery codes are shown once (they replace any set created earlier for a passkey).

**Storage.** The secret is sealed with AES-256-GCM under `GORUN_SECRET_KEY` (`db.Encrypt`, the helper used for webhook, SMTP and extension secrets) with the additional data `auth.totp:<user id>`, so a database copy alone cannot generate codes and a ciphertext moved to another account does not decrypt. Rotating the secret key makes enrolled factors unreadable: app codes are then refused like wrong codes, recovery codes keep working, and the factor must be reset (see [Lockout recovery](#lockout-recovery)). Recovery codes are 10 characters of Crockford base32 (50 random bits), stored as SHA-256 of the user id and the normalized code and compared in constant time. A fast hash is enough for random server-generated codes (the same reasoning as session tokens and API keys); Argon2id would make every attempt cost ten password hashes because all unused codes are checked. Codes are single use (an atomic `used_at` update) and creating new codes deletes the old ones.

**Sign-in.** After a correct password (or a magic link, or the shop's account form) a user with two-factor gets no session. Instead GoRun stores a pending sign-in (32 random bytes in the `gp_2fa` cookie, `HttpOnly`, `SameSite=Strict`, path `/admin/login/2fa`; only its SHA-256 in `login_challenges`), valid for five minutes, and asks for a code at `/admin/login/2fa`. A code is accepted for the current step and one step on either side (clock drift). Every candidate is compared in constant time. The last accepted step is stored per user and only a later step is accepted (an atomic compare-and-set), so a code cannot be replayed, not even in parallel. A recovery code works instead of an app code. On success the pending sign-in is deleted (single use) and the normal session starts; the user lands where the sign-in began (the admin page asked for, or the shop account form's allowed destination, or `/account` for the launch form). That return path is stored with the pending sign-in and must be a same-site relative path; anything else (absolute or `//` URLs, backslashes, control characters) becomes `/admin`. Each attempt counts in the persistent login limiter: more than 5 codes per user, or 30 per IP address, within 15 minutes are refused; a pending sign-in dies after 5 wrong codes, so the password must be entered again. Codes never reach the audit log.

**Turning it off.** Disabling needs the current password and a current code (or recovery code). New recovery codes need a current code.

**Required roles.** On **Login security** an administrator selects roles whose members must use two-factor (setting `auth.two_factor_roles`). An authenticator app or at least one [passkey](#passkeys) satisfies it. Members of those roles may turn off the app only while a passkey usable on this site remains. Members of those roles without a factor are held on the enrolment page after sign-in, and on every later admin request, until they finish; sign-out, the language switch and password confirmation stay available, and session API calls answer `403 two_factor_enrolment_required`. They cannot turn the factor off.

**Lost phone or passkey.** An administrator with `users.manage` opens the user and uses **Reset two-factor** (needs a recent password confirmation). The authenticator app factor, every passkey, the recovery codes, pending sign-ins and open passkey requests are removed; the user's password and sessions are kept. When no administrator can sign in, see [Lockout recovery](#lockout-recovery).

**Not covered.** API keys and MCP agent keys are separate credentials and never ask for a code. The recent-password confirmation for sensitive actions (`/admin/reauth`) asks only for the password.

**Audit.** `user.2fa_enabled`, `user.2fa_disabled`, `user.2fa_reset` (actor is the administrator, or `system` with `{"via":"cli"}` for `gorun user two-factor-reset`; `"passkeys": n` when passkeys were removed), `user.2fa_recovery_used` (with the remaining count), `user.2fa_recovery_regenerated`, `user.2fa_failed` (reason `invalid` or `locked`) and `security.two_factor_roles`.

**Migration.** `auth/0012_two_factor.sql` only adds tables (`user_totp`, `user_recovery_codes`, `login_challenges`); existing users and sessions are unchanged. An older binary ignores the tables and therefore signs enrolled users in with the password only (see [UPGRADE.md](UPGRADE.md#022-upgrade-notes) before rolling back to 0.21.x).

## Passkeys

A passkey (WebAuthn) can replace the code at the second sign-in step: the device's screen lock, a phone or a security key confirms the sign-in after the password (or magic link, or the shop's account form). It is a **second factor only**; the password is always asked first. Design and sources: [specs/passkeys.md](specs/passkeys.md).

**Where.** **My account → Two-factor authentication → Passkeys** (`/admin/account/two-factor/passkeys`). Users name each passkey (up to 64 characters) and see when it was added and last used, and whether it syncs between devices. Adding one needs a recent sign-in or password confirmation (`/admin/reauth`, 10 minutes), like setting up the app. Removing one weakens the sign-in, so it asks for the current password every time (rate-limited like sign-in; accounts without a password need a recent sign-in), like turning off the app. A user whose role requires two-factor cannot remove their last usable factor (a passkey made for another site address does not count); the check and the removal are one database statement, so parallel requests cannot both pass it. Removing the last factor also deletes the recovery codes; the password alone then signs in again.

**Recovery codes.** When a user adds a passkey and holds no unused recovery codes (typically their first second factor), ten codes are shown once right after adding it (same format, hashing and single use as above). They work on the second step instead of the passkey. Users without the app renew them on the same page with one of their recovery codes. Turning off the authenticator app keeps them while passkeys remain; enabling the app replaces them with a new set.

**Site address.** WebAuthn binds every passkey to the site's domain (the *RP ID*) and only works in a secure context. GoRun derives both from `GORUN_BASE_URL` only, never from request headers or the `site.base_url` setting:

| `GORUN_BASE_URL` | RP ID | Passkeys |
|---|---|---|
| `https://cms.example.com` | `cms.example.com` | available, origin `https://cms.example.com` |
| `https://example.com:8443` | `example.com` | available, origin `https://example.com:8443` (`:443` is dropped, as browsers do) |
| `https://bücher.example` | `xn--bcher-kva.example` | available, punycode as browsers send it |
| `http://localhost:8080` | `localhost` | available (development) |
| `http://example.com`, `http://192.0.2.10`, `https://192.0.2.10` | – | unavailable: plain http on a real host, or an IP address |

Every passkey request must carry an `Origin` header equal to that origin (403 otherwise; on the sign-in step it is audited but does not count against the pending sign-in, so a foreign page cannot end someone's sign-in), and the library verifies the origin inside the signed client data and the RP ID hash in the authenticator data. Passkeys are stored with the RP ID they were created for and only those of the current RP ID are offered. **Changing the domain in `GORUN_BASE_URL` makes existing passkeys unusable** (they stay listed, marked, until removed); such users still need the second step and use the app, a recovery code, or an administrator's reset. Opening the admin through another address than `GORUN_BASE_URL` (for example the server's IP) cannot use passkeys.

**Sign-in.** On the second step, **Use a passkey** asks the browser for an assertion of one of the user's passkeys (user verification preferred). A sign-in passes only when the signature verifies against the stored public key, the origin and RP ID match, the challenge is the one issued to this pending sign-in, and the authenticator's signature counter has grown (or stays zero for authenticators without a counter). A counter that goes backwards or stands still can mean a cloned authenticator ([WebAuthn §6.1.1](https://www.w3.org/TR/webauthn-3/#sctn-sign-counter)): the sign-in is refused, the stored counter kept and `user.passkey_failed` recorded with reason `sign_count`. Attempts count in the same persistent limiter as codes (5 per user and 30 per IP address per 15 minutes, shared between codes and passkeys), and wrong attempts count towards the pending sign-in's limit of 5.

**Challenges.** Each registration or sign-in uses a fresh 32-byte random challenge. Only its SHA-256 is stored (`webauthn_challenges`), with the rest of the library's session data, for five minutes. A challenge is bound to the browser session that began a registration, or to the pending sign-in (`gp_2fa`) that began a passkey sign-in, and is deleted before the answer is verified, so it works once even under concurrent requests. Each session or pending sign-in has at most one open passkey request.

**Stored data.** `webauthn_users` holds one random 64-byte user handle per user (never the user id or email; the same for all their passkeys). `user_passkeys` holds the credential id, public key, signature counter, transports, AAGUID, flags (user verification, backup eligibility and state), attestation type and format, RP ID, name and dates. The public key is not secret; the private key never leaves the authenticator. Attestation is not requested (`none`), so GoRun does not restrict authenticator models.

**Browsers.** The admin script (`/admin/static/passkeys.js`, no framework) checks for WebAuthn support; browsers without it see a short explanation and keep using codes.

**Not covered.** Passwordless sign-in with a passkey alone is not offered yet (see the spec for why). API keys and MCP never use passkeys.

**Audit.** `user.passkey_added` and `user.passkey_removed` (`{"passkey_id","name"}`), `user.passkey_used` (`{"passkey_id"}`) and `user.passkey_failed` (`{"reason"}`: `invalid`, `expired`, `origin`, `locked` or `sign_count`). Never challenges, credential ids or signatures.

**Migration.** `auth/0014_passkeys.sql` only adds tables (`webauthn_users`, `user_passkeys`, `webauthn_challenges`). An older binary ignores them: users whose only factor is a passkey then sign in with the password alone. Sign out all sessions created while an older binary ran after rolling forward again.

### Lockout recovery

Use this when a user with two-factor can produce neither an app code, a passkey nor a recovery code and no other administrator can reset it in the admin, for example the only administrator lost their phone and recovery codes.

On the server, with the same data directory and environment as `serve` (the command works next to a running server):

```sh
gorun user two-factor-reset --email admin@example.com [--data /var/lib/gorun]
# Docker / Coolify: docker exec <container> /app/gorun user two-factor-reset --email admin@example.com
```

It removes that user's authenticator app factor, passkeys, recovery codes, pending sign-ins and open passkey requests, prints what it did (including how many passkeys) (or that the user had no factor) and records `user.2fa_reset` with the `system` actor and `{"via":"cli"}`. It works for any account, including the last administrator; the password and existing sessions are kept. The next sign-in asks for the password only, or for enrolment when the user's role requires two-factor. Access to the server's shell is the authority here, as for `gorun user password`. If the password is lost too, run `gorun user password --email … --password-stdin` as well (that also ends the user's sessions).

**After a `GORUN_SECRET_KEY` change.** Secrets sealed under the old key no longer decrypt. GoRun then refuses app codes like a wrong code (never a server error) and logs once per user and process `auth: two-factor secret cannot be decrypted; was GORUN_SECRET_KEY changed?` with the user id (no secret material). Recovery codes do not depend on the key and keep working. Either put the previous key back, or let each affected user sign in with a recovery code and then reset their factor (an administrator's **Reset two-factor**, or the command above) so they enrol again under the new key. Users without recovery codes need the reset before they can sign in.
