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

**Enrolment.** Starting needs a recent sign-in or password confirmation (`/admin/reauth`, 10 minutes). GoRun creates a random 20-byte secret and shows it as a grouped base32 key plus an `otpauth://totp/…` link that authenticator apps on the same device open. No QR code is drawn: the repository has no QR encoder and none was added. The factor is turned on only after a valid code for the secret; then ten recovery codes are shown once.

**Storage.** The secret is sealed with AES-256-GCM under `GORUN_SECRET_KEY` (`db.Encrypt`, the helper used for webhook, SMTP and extension secrets) with the additional data `auth.totp:<user id>`, so a database copy alone cannot generate codes and a ciphertext moved to another account does not decrypt. Rotating the secret key makes enrolled factors unreadable: app codes are then refused like wrong codes, recovery codes keep working, and the factor must be reset (see [Lockout recovery](#lockout-recovery)). Recovery codes are 10 characters of Crockford base32 (50 random bits), stored as SHA-256 of the user id and the normalized code and compared in constant time. A fast hash is enough for random server-generated codes (the same reasoning as session tokens and API keys); Argon2id would make every attempt cost ten password hashes because all unused codes are checked. Codes are single use (an atomic `used_at` update) and creating new codes deletes the old ones.

**Sign-in.** After a correct password (or a magic link, or the shop's account form) a user with two-factor gets no session. Instead GoRun stores a pending sign-in (32 random bytes in the `gp_2fa` cookie, `HttpOnly`, `SameSite=Strict`, path `/admin/login/2fa`; only its SHA-256 in `login_challenges`), valid for five minutes, and asks for a code at `/admin/login/2fa`. A code is accepted for the current step and one step on either side (clock drift). Every candidate is compared in constant time. The last accepted step is stored per user and only a later step is accepted (an atomic compare-and-set), so a code cannot be replayed, not even in parallel. A recovery code works instead of an app code. On success the pending sign-in is deleted (single use) and the normal session starts; the user lands where the sign-in began (the admin page asked for, or the shop account form's allowed destination, or `/account` for the launch form). That return path is stored with the pending sign-in and must be a same-site relative path; anything else (absolute or `//` URLs, backslashes, control characters) becomes `/admin`. Each attempt counts in the persistent login limiter: more than 5 codes per user, or 30 per IP address, within 15 minutes are refused; a pending sign-in dies after 5 wrong codes, so the password must be entered again. Codes never reach the audit log.

**Turning it off.** Disabling needs the current password and a current code (or recovery code). New recovery codes need a current code.

**Required roles.** On **Login security** an administrator selects roles whose members must use two-factor (setting `auth.two_factor_roles`). Members of those roles without a factor are held on the enrolment page after sign-in, and on every later admin request, until they finish; sign-out, the language switch and password confirmation stay available, and session API calls answer `403 two_factor_enrolment_required`. They cannot turn the factor off.

**Lost phone.** An administrator with `users.manage` opens the user and uses **Reset two-factor** (needs a recent password confirmation). The factor, recovery codes and pending sign-ins are removed; the user's password and sessions are kept. When no administrator can sign in, see [Lockout recovery](#lockout-recovery).

**Not covered.** API keys and MCP agent keys are separate credentials and never ask for a code. The recent-password confirmation for sensitive actions (`/admin/reauth`) asks only for the password.

**Audit.** `user.2fa_enabled`, `user.2fa_disabled`, `user.2fa_reset` (actor is the administrator, or `system` with `{"via":"cli"}` for `gorun user two-factor-reset`), `user.2fa_recovery_used` (with the remaining count), `user.2fa_recovery_regenerated`, `user.2fa_failed` (reason `invalid` or `locked`) and `security.two_factor_roles`.

**Migration.** `auth/0012_two_factor.sql` only adds tables (`user_totp`, `user_recovery_codes`, `login_challenges`); existing users and sessions are unchanged. An older binary ignores the tables and therefore signs enrolled users in with the password only (see [UPGRADE.md](UPGRADE.md#022-upgrade-notes) before rolling back to 0.21.x).

### Lockout recovery

Use this when a user with two-factor can neither produce an app code nor a recovery code and no other administrator can reset it in the admin, for example the only administrator lost their phone and recovery codes.

On the server, with the same data directory and environment as `serve` (the command works next to a running server):

```sh
gorun user two-factor-reset --email admin@example.com [--data /var/lib/gorun]
# Docker / Coolify: docker exec <container> /app/gorun user two-factor-reset --email admin@example.com
```

It removes that user's factor, recovery codes and pending sign-ins, prints what it did (or that the user had no factor) and records `user.2fa_reset` with the `system` actor and `{"via":"cli"}`. It works for any account, including the last administrator; the password and existing sessions are kept. The next sign-in asks for the password only, or for enrolment when the user's role requires two-factor. Access to the server's shell is the authority here, as for `gorun user password`. If the password is lost too, run `gorun user password --email … --password-stdin` as well (that also ends the user's sessions).

**After a `GORUN_SECRET_KEY` change.** Secrets sealed under the old key no longer decrypt. GoRun then refuses app codes like a wrong code (never a server error) and logs once per user and process `auth: two-factor secret cannot be decrypted; was GORUN_SECRET_KEY changed?` with the user id (no secret material). Recovery codes do not depend on the key and keep working. Either put the previous key back, or let each affected user sign in with a recovery code and then reset their factor (an administrator's **Reset two-factor**, or the command above) so they enrol again under the new key. Users without recovery codes need the reset before they can sign in.
