# Changelog

All notable changes to GoRun are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow semantic versioning.

## [Unreleased]

### Fixed

- A process that runs job workers (`gorun serve`, `gorun mcp --jobs`, `gorun reindex` without
  `--no-jobs`) now holds an exclusive OS lock on `<data>/gorun.lock`; a second one on the same data
  directory refuses to start and names the holder. The lock is released on shutdown or if the
  process dies.
- Startup no longer requeues every running job. Workers refresh `locked_at` every 30 seconds, and only
  running jobs whose heartbeat is older than 2 minutes are requeued, at startup and on every scheduler
  tick, so jobs still executing elsewhere are never run twice.
- Forwarding headers from a peer outside `GORUN_TRUSTED_PROXIES` are logged as a warning (first
  occurrence, then at most hourly), since behind an unlisted proxy every visitor shares its address.
- A data directory whose path contains `?`, `#` or `%` opened the wrong database file and silently
  lost the WAL, foreign-key and busy-timeout pragmas; the SQLite `file:` URI is now percent-encoded
  (backups too).
- The checkout result page loads the embedded HTMX instead of an unpinned jsDelivr copy.

### Changed

- Text responses (HTML, CSS, JS, JSON, XML, SVG) are gzip-compressed when the client accepts it.
  Admin pages stay uncompressed (they carry the CSRF token; BREACH).
- Version-named vendor files and `?v=` asset URLs are served with
  `Cache-Control: public, max-age=31536000, immutable`.
- **Public CSP:** public pages no longer allow scripts from `cdn.tailwindcss.com`,
  `cdn.jsdelivr.net` or `unpkg.com`, nor `'unsafe-eval'`; the bundled themes need none of them. A custom
  theme that does can be allowed with `GORUN_CSP_SCRIPT_SRC` / `csp_script_src`. `/api/docs` allows only
  the pinned Scalar bundle, now with SRI. The admin keeps `'unsafe-eval'` for Alpine.
- CI runs staticcheck (`staticcheck.conf`); unused code it found was removed.

### Documentation

- `deploy/docker-compose.yml` and `deploy/coolify.md` configure `GORUN_TRUSTED_PROXIES`.
- SECURITY.md and docs/PRODUCTION.md now state that no forwarding headers are trusted by default.

### First public release

- GoRun is published as open source under the GNU Affero General Public License v3.0 (see
  `LICENSE` and `NOTICE`).
- The command line lives in the importable package `pkg/cli`; `cmd/gorun` is a thin wrapper.
  A separate binary can add modules, commands, boot-order entries, error-inventory categories and
  a trusted site-policy directory through `cli.Option`s (see docs/ARCHITECTURE.md, "Building on
  the core"). Every core command and flag is unchanged.
- The trusted directory for `GORUN_SITE_POLICY` files defaults to `/var/lib/gorun-site-policies`;
  a downstream build can set another one with `cli.WithSitePolicyDirectory`.
- Hosting-platform add-ons, brand themes and the commands that served them (`tenant-init`,
  `addon-install`, `page-catalog`) are no longer part of the core.
