# Local start

Run a local CMS from an installed binary or from the repository:

```sh
gorun start
# Repository checkout:
go run ./cmd/gorun start
```

`start` uses the existing module/plugin configuration and CMS. It boots SQLite
and all enabled module migrations, uses the configured theme, and installs the
bundled default theme when missing. If no theme directory exists, it uses
`<data>/themes/default`.
An explicit `GORUN_THEMES` must point to an existing directory. Installed themes,
customizations, content and existing accounts are preserved.

On an empty database it atomically creates one administrator:

- Username: `admin` (case insensitive).
- Initial email: `admin@localhost.invalid`. Change this to your email in the admin UI.
- Password: 32 cryptographically random bytes encoded as 43 URL-safe characters
  (256 bits of randomness). Only its password hash is stored in SQLite.

The terminal prints the local URL, available private IPv4 LAN URLs, and the
new credentials **once**. Save the password before closing the terminal. The
browser opens the IPv4 loopback admin URL (`127.0.0.1`) after the listener and
application are ready, matching the IPv4 listener regardless of localhost DNS.
If opening fails, use the printed link. Terminal capture and scrollback can
contain the password: keep them private. Passwords are never embedded in URLs.

On subsequent starts no user is created or reset and no password is printed.
If any account already exists, `start` leaves it alone; sign in with its email.
The persistent `admin` alias belongs to the first admin created through `start`
or browser setup after this version; it survives email changes. Existing sites
are not assigned an alias by guessing which admin should own it. Email login,
status checks, password hashing and login throttling remain supported. Username
and email attempts share the same login limit.

```sh
gorun start --data ./my-site --addr :9090
gorun start --local-only             # bind only 127.0.0.1
gorun start --no-browser             # useful on headless computers
```

Default port: 8080. `--addr` selects the port; `start` chooses the binding host
(`0.0.0.0`, or `127.0.0.1` with `--local-only`) and sets the local base URL.
`--addr :0` chooses an available port and prints the actual port. Existing
configuration precedence applies to the other settings. Use `serve` with the
production configuration for deployment; `start` rejects production mode.
IPv6-only LANs are not advertised. If no private IPv4 interface is available,
the terminal says so and localhost still works. Firewalls may block LAN access.

## First sign-in

The generated administrator signs in with `admin` and the one-time terminal
password. Before the dashboard or authenticated API/MCP becomes available,
`/admin/onboarding` asks for a real email, a new password and confirmation, site
name, and Arabic or English for both the site and dashboard. The new password
must differ from the temporary password and follow the normal password policy.

Saving is atomic: identity, password, site title and language either all change
or all remain unchanged. Completion ends old sessions, revokes previously issued
API keys and magic links, then starts a fresh browser session and opens the
dashboard. A session older than ten minutes must confirm its password again.
Failed validation never echoes passwords. The `admin` alias continues to work
with the new password, as does the new email.

Regular existing accounts and browser-setup accounts skip this screen. An
unconfigured account from the preceding `start` release (the `admin` alias still
using `admin@localhost.invalid`) is recognized once when upgrading. Finishing the
screen survives restarts; it is never automatically offered again. A CLI password
reset does not skip unfinished site setup.

If the new password was saved but a fresh browser session could not be started,
sign in again with your new password. The account changes remain saved.

## Diagnose a local site

```sh
gorun doctor --data ./data
gorun doctor --data ./data --json
gorun doctor --addr :9090
```

`doctor` checks the existing configuration and secret, data-directory permissions,
SQLite integrity, migration drift, enabled/disabled module activation, an active
administrator, unfinished first-login setup, the active theme's filesystem
contract, and the configured port. Every failure or warning includes a next step.
JSON contains `ok` and a `checks` array with `id`, `status`, `message` and optional
`action`; statuses are `ok`, `warn`, and `fail`. Exit status is 0 for healthy sites
and warnings, 1 when a check fails.

It does not create a site or secret, run migrations, repair files, start workers,
reset accounts or take the worker lock. SQLite is opened in read-only/query-only
mode; normal SQLite WAL coordination may use its shared-memory sidecars. It can
run beside the server. Integrity/schema checks have a bounded timeout. Permission
checks inspect permission bits rather than writing test files; ACLs and the actual
server account may impose additional restrictions.

The port check briefly attempts a local bind. If the port is occupied, it probes
only that local address's `/healthz` with redirects and environment proxies
disabled. A responding health endpoint is a warning: it may be the running site
or another service, so verify ownership. It never probes a remote configured
address. This is a diagnostic snapshot; another process can take the port later.

A new or misconfigured site fails with instructions to run `gorun start` or
review its existing configuration. Diagnostics suppress configuration values,
credentials and database contents. `doctor` is separate from `gorun theme doctor`,
which provides detailed theme-only checks.

## LAN security

The LAN URL is an unauthenticated, read-only **public preview**, served over
unencrypted HTTP. Use it only on a trusted network. Private-network clients can
request public GET/HEAD pages; other methods, admin/setup/login, accounts, magic
login links, REST API, MCP, cart/checkout, webhooks, metrics, private intake
(`/reports`), public forms (`/forms`) and customer `/start` are blocked, including
the same paths under a locale prefix (`/en/…`, `/ar/…`). Incoming LAN
cookies and Authorization headers are discarded. Only requests from a loopback
peer **and** the localhost/127.0.0.1 host may access privileged routes. Host and
port validation rejects DNS rebinding and forged localhost hosts from LAN peers;
forwarded peer headers are not trusted. Public-address peers are rejected.

Admin users must open the printed localhost URL on the server computer. For
full remote administration, deploy with `serve`, HTTPS and the normal production
security configuration. Do not forward the local preview port to the Internet
or proxy external traffic through loopback. Plugins should follow HTTP semantics:
GET/HEAD public routes must not mutate state or expose private data. Use
`--local-only` to disable LAN access entirely.

## Password recovery

If the first-run terminal output was lost, reset the password locally (the
plaintext original cannot be recovered):

```sh
gorun user password --data ./data --email admin@localhost.invalid --password-stdin
```

Enter a new password on stdin. If you changed the account email, use that email.
This resets its password and ends its sessions. Never delete the database to
recover a password: that also deletes site data.
