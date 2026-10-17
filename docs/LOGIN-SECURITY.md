# Login audit and temporary IP restrictions

The native **Login security** screen at `/admin/security/login-attempts` is available to active administrator sessions with `security.manage`. Editors and API keys cannot manage restrictions. It shows the latest 50 failed attempts and active site-local restrictions. The submitted email is an attempted identity, not proof of who connected.

A recent, verified-source failure can be restricted for one hour, 24 hours or seven days. Only individual public IPv4/IPv6 addresses are accepted; CIDRs, local/private addresses, the administrator's current address and configured trusted proxy addresses are rejected. A shared address can represent several people. Expiry is automatic; unblock forms are bound to the displayed expiry so an old form cannot remove a newer restriction. App routes (public, administration, API and webhooks) enforce the restriction. Kernel health/readiness probes remain available. This is application-level protection, not a firewall or Cloudflare rule.

Failed-login audit metadata contains a bounded, normalized attempted email and reason, never the password. The resolved address is recorded in the audit IP field with provenance. Older rows without source provenance remain visible but cannot directly create a block. Repeated failure events are sampled at 30 per address/minute and 120 total/minute. Blocked-request events are sampled once per address/minute, at most 60 total/minute. Existing persistent credential throttling is unchanged.

## Mandatory proxy configuration before production rollout

Forwarded headers are no longer implicitly trusted from all private/loopback networks. Empty configuration trusts **no** forwarding headers. Configure exact operator-controlled ingress hops with `GORUN_TRUSTED_PROXIES` (comma-separated IPs/CIDRs), or `trusted_proxies = [...]` in gorun.toml. Invalid entries and all-address `/0` networks prevent startup. Prefer environment configuration during a reversible rollout: an older binary ignores the added environment variable, whereas its strict TOML parser rejects a new key.

The resolver begins with the TCP peer and walks `X-Forwarded-For` right to left only while the current hop belongs to a configured trusted network. It stops at the first untrusted hop. Malformed or oversized chains are ignored completely. `CF-Connecting-IP` and arbitrary provider headers are ignored. `X-Forwarded-Proto` is accepted only from a configured immediate proxy; that proxy must overwrite/sanitize it.

Before deploying each instance, independently verify the real Traefik/container/Cloudflare chain, firewall reachability and forwarded-header sanitation. Configure only verified ingress networks; never copy broad example private ranges. If Cloudflare's edge is an intermediate hop, its observed current ranges and edge-to-origin header behavior must be validated by the operator. Otherwise the address may identify a proxy, not a visitor. No production trust configuration was changed by this implementation.

The additive `auth/0007_security_ip_blocks.sql` migration preserves existing users, sessions and audit rows. An older binary ignores the new table and therefore does **not** enforce these restrictions; rollback requires explicitly accounting for that security behavior.
