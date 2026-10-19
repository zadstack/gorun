# Set up a production server

This assistant-neutral setup path deploys a **new, single-host GoRun site** in
production mode using the released GoRun image, Docker Compose and Caddy. It does
not run `gorun start` on the internet. The script asks for a domain, a new absolute
installation directory and the server's public addresses, and reports the URL and
paths only after HTTPS and health checks pass.

## Before running

Use a Linux server with **root access, Python 3, curl, rootful Docker Engine and
Docker Compose v2** already installed. No Go toolchain is needed. Docker must use
the local Unix socket, without user namespace remapping. Install Docker using
[Docker's distribution-specific instructions](https://docs.docker.com/engine/install/).
This script does not install Docker, change a firewall, edit DNS, stop another
website or migrate an existing installation.

Point the domain's A records at this server's public IPv4 address. If AAAA records
exist, their IPv6 addresses must reach the same server too. Allow inbound TCP
**80 and 443** through the host and provider firewalls. Those ports must be free;
a server already managed by Coolify, nginx or another proxy should use the
[existing-proxy deployment guide](../deploy/coolify.md) instead. Disable CDN
proxying for this direct-host path. The script compares **all** addresses returned
by the host resolver against the public addresses you provide, then checks HTTPS
at every origin address. It does not modify DNS or claim global DNS propagation.

## Copy, inspect and run

Download from the official repository and review the script before giving it root
access. During an unreleased checkout, use that reviewed checkout's script; the
main-branch download becomes available when the corresponding PR is merged.

```sh
curl -fsSL https://raw.githubusercontent.com/zadstack/gorun/main/deploy/server-setup.py -o gorun-server-setup.py
python3 gorun-server-setup.py --dry-run
sudo python3 gorun-server-setup.py
```

The first invocation prints a secret-free plan and does not create files,
containers or credentials. The second prompts again and deploys. You can instead
supply explicit values (replace these examples with your actual domain, folder
and public IP):

```sh
sudo python3 gorun-server-setup.py \
  --domain cms.your-domain.com \
  --directory /opt/gorun-site \
  --server-ip 203.0.113.10
```

The documentation address above is intentionally non-routable and is rejected by
the script; use your real public address. Repeat `--server-ip` for every additional
A/AAAA address. The installation directory must not exist; its parent must exist.
The data directory is `<installation>/data`. Deployment/configuration files are
private to root; the new data directory belongs to the image's nonroot UID 65532.
No existing folder, database, account or configuration is adopted or overwritten.

## What it creates

- `compose.json`: GoRun in production mode, with no host port for the application,
  plus Caddy publishing TCP 80/443. Both services restart unless explicitly stopped.
- An isolated Docker bridge, allocated by Docker; only Caddy's exact address is
  trusted for forwarded headers. Caddy waits for the application's health check.
- `secrets.env` (0600): a random stable site secret and first-admin setup token.
  Neither value is printed or passed as a command-line argument.
- Persistent `data/`, `tls-data/` and `tls-config/` directories. Caddy renews TLS
  certificates using its persistent storage.
- `deployment.json`: metadata and configuration hashes used to detect accidental
  changes on resume. This is an integrity guard, not a signature against root.

The GoRun image is pinned to release `0.4.0` by default, and Caddy to `2.11.7-alpine`.
Only official numeric GoRun release tags are accepted with `--image`. Tags are not
immutable digests; review upstream image updates and follow the upgrade runbook.

Caddy requests a publicly trusted certificate for the domain and redirects HTTP
to HTTPS. GoRun's site URL is HTTPS, and the script verifies certificate trust,
HSTS and successful health/readiness/home responses at every supplied IP before
reporting success. See [Caddy's automatic HTTPS requirements](https://caddyserver.com/docs/automatic-https).

Open the reported `/admin/setup` URL and enter the setup token directly from the
root-private `secrets.env` file. Choose your admin email/password in the browser.
An assistant must not copy the token or password into a conversation or logs.
Once a user exists, the application closes first-admin setup; resume never creates
or replaces an account. This production path deliberately leaves password choice
to the browser rather than printing an admin password into automation logs.

## Resume, stop, upgrade and recover

If certificate issuance, firewall reachability or health verification fails, the
script exits with an error. The containers and private deployment files remain
available for inspection and retry; **it does not report a successful public
installation**. Inspect Caddy/container logs locally, fix the cause, then repeat
the same arguments with `--resume`. Existing domain, image, network and configuration
must match; credentials and data remain unchanged. An incomplete folder created
before the deployment manifest was written requires manual inspection; it is not
automatically adopted or deleted.

Successful output includes fully quoted stop/start commands. `stop` preserves all
data. `up -d --wait` starts the same deployment again after an intentional stop.
Enable the Docker service at boot using your operating system's service manager;
the restart policy alone cannot start an inactive Docker daemon.

Back up both the GoRun data and `secrets.env` securely outside this server. Normal
GoRun backups intentionally omit external environment secrets and Caddy keys.
Keep `tls-data/` private too. Use [UPGRADE.md](UPGRADE.md) for backup, stop, pinned
image replacement and restore; `--resume` is a retry tool, **not an upgrade or
migration command**. A changed image/domain/configuration is refused rather than
silently replacing a site. This is one GoRun process and one SQLite data directory,
not a multi-replica or multi-tenant deployment orchestrator.

## What is tested

The regression suite covers DNS/AAAA mismatch, invalid or injected input,
secret-free dry runs, safe file creation, existing-path refusal, configuration
integrity, resume preservation, private file permissions and HTTPS failure.
The Linux integration workflow starts the actual released image with Caddy using
an isolated **private test CA**, verifies TLS/hostname/HSTS and app routes, creates
a synthetic admin, stops/resumes and checks that its identity/password hash and
site secrets remain unchanged. DNS and certificate issuance are fixtures in that
test. It does not certify a real public domain, firewall, DNS propagation or
public ACME issuance; the actual-host acceptance checks are still required.
