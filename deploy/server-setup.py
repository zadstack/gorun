#!/usr/bin/env python3
"""Provision a new, single-host GoRun site; never replace an existing deployment."""
import argparse
import hashlib
import http.client
import ipaddress
import json
import os
from pathlib import Path
import re
import secrets
import shlex
import socket
import ssl
import subprocess
import sys
import time

DEFAULT_IMAGE = "ghcr.io/zadstack/gorun:0.4.0"
CADDY_IMAGE = "caddy:2.11.7-alpine"


def run(args, capture=True):
    result = subprocess.run(args, text=True, stdout=subprocess.PIPE if capture else None,
                            stderr=subprocess.PIPE if capture else None)
    if result.returncode:
        # Compose configuration and command errors may contain environment secrets.
        raise RuntimeError("Command failed: " + shlex.join(args[:3]) + "; inspect Docker locally")
    return result.stdout.strip() if capture else ""


def domain_name(value):
    value = value.lower().rstrip(".")
    if len(value) > 253 or not re.fullmatch(r"(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}", value):
        raise ValueError("Use a public DNS hostname, without scheme, path, port or wildcard")
    if value.endswith((".localhost", ".local", ".internal", ".test", ".example", ".invalid")):
        raise ValueError("Use a publicly registered hostname")
    return value


def public_ips(values):
    addresses = {ipaddress.ip_address(value) for value in values}
    if not addresses or any(not address.is_global for address in addresses):
        raise ValueError("Provide the server's public IPv4/IPv6 addresses")
    return {str(address) for address in addresses}


def check_dns(domain, expected):
    actual = {str(ipaddress.ip_address(item[4][0]))
              for item in socket.getaddrinfo(domain, 443, type=socket.SOCK_STREAM)}
    if actual != expected:
        raise ValueError("DNS A/AAAA mismatch: resolved " + ", ".join(sorted(actual)) +
                         "; expected " + ", ".join(sorted(expected)) +
                         ". Fix DNS, including stale AAAA records; disable CDN proxying for this direct-host setup")
    return sorted(actual)


def check_ports():
    # Check both wildcard listeners, including services outside Docker. Never stop them.
    for family, bind_address in ((socket.AF_INET, "0.0.0.0"), (socket.AF_INET6, "::")):
        for port in (80, 443):
            try:
                with socket.socket(family) as probe:
                    if family == socket.AF_INET6:
                        probe.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 1)
                    probe.bind((bind_address, port))
            except OSError as exc:
                if family == socket.AF_INET6 and exc.errno in (97, 99):
                    continue
                raise RuntimeError("Port " + str(port) + " is occupied or unavailable; use your existing reverse proxy instead") from exc


def compose_config(domain, image, network, proxy_ip):
    return {
        "services": {
            "gorun": {
                "image": image, "restart": "unless-stopped", "env_file": ["./secrets.env"],
                "environment": {"GORUN_ENV": "prod", "GORUN_DATA": "/data",
                                "GORUN_BASE_URL": "https://" + domain,
                                "GORUN_ADDR": ":8080", "GORUN_TRUSTED_PROXIES": proxy_ip},
                "volumes": [{"type": "bind", "source": "./data", "target": "/data"}],
                "networks": ["site"],
                "healthcheck": {"test": ["CMD", "/app/gorun", "healthcheck"],
                                "interval": "5s", "timeout": "3s", "retries": 20, "start_period": "10s"},
            },
            "proxy": {
                "image": CADDY_IMAGE, "restart": "unless-stopped",
                "ports": ["80:80", "443:443"],
                "volumes": [
                    {"type": "bind", "source": "./Caddyfile", "target": "/etc/caddy/Caddyfile", "read_only": True},
                    {"type": "bind", "source": "./tls-data", "target": "/data"},
                    {"type": "bind", "source": "./tls-config", "target": "/config"},
                ],
                "networks": {"site": {"ipv4_address": proxy_ip}},
                "depends_on": {"gorun": {"condition": "service_healthy"}},
            },
        },
        "networks": {"site": {"external": True, "name": network}},
    }


def write_private(path, content):
    # O_EXCL protects existing files and symlink destinations.
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w") as handle:
        handle.write(content)


def project_name(directory):
    return "gorun-" + hashlib.sha256(str(directory).encode()).hexdigest()[:12]


def load_state(directory, domain, image):
    manifest = directory / "deployment.json"
    if manifest.is_symlink():
        raise ValueError("Deployment manifest must not be a symlink")
    state = json.loads(manifest.read_text())
    if state.get("schema") != 1 or set(state.get("files", {})) != {"compose.json", "Caddyfile", "secrets.env"}:
        raise ValueError("Unrecognized deployment manifest")
    if state["domain"] != domain or state["image"] != image:
        raise ValueError("Existing deployment uses another domain/image; use the documented upgrade procedure")
    for name, expected in state["files"].items():
        file = directory / name
        if file.is_symlink() or hashlib.sha256(file.read_bytes()).hexdigest() != expected:
            raise ValueError("Existing deployment configuration changed; inspect it manually, without overwriting")
    return state


def network_address(network):
    info = json.loads(run(["docker", "network", "inspect", network]))[0]
    subnets = [ipaddress.ip_network(item["Subnet"]) for item in info["IPAM"]["Config"] if "Subnet" in item]
    ipv4 = next((subnet for subnet in subnets if subnet.version == 4 and subnet.num_addresses >= 8), None)
    if ipv4 is None:
        raise RuntimeError("Docker did not allocate an IPv4 bridge subnet")
    return str(ipv4.network_address + 2)


def create_deployment(directory, domain, image, name):
    if directory.exists():
        raise ValueError("Choose a new installation directory; existing paths are never adopted or overwritten")
    # Parents must already exist, so a typo cannot create an unexpected directory tree.
    directory.mkdir(mode=0o700)
    network = name + "-site"
    # Refuse an existing network rather than adopting resources belonging to another installation.
    existing = run(["docker", "network", "ls", "--format", "{{.Name}}"])
    if network in existing.splitlines():
        raise ValueError("Deployment network already exists; inspect it manually")
    run(["docker", "network", "create", "--label", "org.gorun.setup=" + name, network])
    proxy_ip = network_address(network)
    for child in ("data", "tls-data", "tls-config"):
        (directory / child).mkdir(mode=0o700)
    os.chown(directory / "data", 65532, 65532)
    files = {
        "secrets.env": "GORUN_SECRET_KEY=" + secrets.token_hex(32) +
                       "\nGORUN_SETUP_TOKEN=" + secrets.token_urlsafe(32) + "\n",
        "Caddyfile": domain + " {\n    reverse_proxy gorun:8080\n}\n",
        "compose.json": json.dumps(compose_config(domain, image, network, proxy_ip), indent=2) + "\n",
    }
    for filename, content in files.items():
        write_private(directory / filename, content)
    state = {"schema": 1, "domain": domain, "image": image, "network": network, "proxy_ip": proxy_ip,
             "files": {name: hashlib.sha256(content.encode()).hexdigest() for name, content in files.items()}}
    write_private(directory / "deployment.json", json.dumps(state, indent=2) + "\n")
    return state


def probe_https(domain, address, path):
    # Connect to every expected origin IP, validate a public certificate for the domain,
    # and send the real Host header. Never accept --insecure or a CDN's unrelated response.
    context = ssl.create_default_context()
    with socket.create_connection((address, 443), timeout=10) as raw:
        with context.wrap_socket(raw, server_hostname=domain) as tls:
            request = "GET " + path + " HTTP/1.1\r\nHost: " + domain + "\r\nConnection: close\r\n\r\n"
            tls.sendall(request.encode("ascii"))
            response = http.client.HTTPResponse(tls)
            response.begin()
            body = response.read(65536)
            if response.status != 200:
                raise RuntimeError("Public HTTPS check did not return 200")
            if not response.getheader("Strict-Transport-Security", "").startswith("max-age="):
                raise RuntimeError("HTTPS proxy trust/HSTS check failed")
            if path == "/healthz" and not body:
                raise RuntimeError("Empty health response")


def wait_https(domain, addresses, timeout):
    deadline = time.monotonic() + timeout
    while True:
        try:
            for address in addresses:
                for path in ("/healthz", "/readyz", "/"):
                    probe_https(domain, address, path)
            return
        except (OSError, http.client.HTTPException, RuntimeError):
            if time.monotonic() >= deadline:
                raise RuntimeError("HTTPS acceptance timed out. Containers/data remain in place; check firewall, A/AAAA, Caddy logs and certificate issuance, then --resume. No public deployment success is claimed")
            time.sleep(3)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--domain")
    parser.add_argument("--directory", type=Path)
    parser.add_argument("--server-ip", action="append", help="Repeat for every public A/AAAA address")
    parser.add_argument("--image", default=DEFAULT_IMAGE)
    parser.add_argument("--dry-run", action="store_true", help="Validate inputs/DNS and print a secret-free plan; no Docker or filesystem changes")
    parser.add_argument("--resume", action="store_true", help="Retry a deployment created by this script, preserving configuration and data")
    parser.add_argument("--https-timeout", type=int, default=180)
    args = parser.parse_args(argv)
    domain = domain_name(args.domain or input("Public domain (DNS already configured): ").strip())
    raw_directory = args.directory or Path(input("New absolute installation directory (data will be inside it): ").strip())
    if not raw_directory.is_absolute() or raw_directory == Path("/") or raw_directory.is_symlink():
        raise ValueError("Use an absolute, non-symlink installation directory other than /")
    directory = raw_directory.resolve()
    expected = public_ips(args.server_ip or input("Server public IPs (space separated; include IPv6 if AAAA exists): ").split())
    if not re.fullmatch(r"ghcr\.io/zadstack/gorun:[0-9]+\.[0-9]+\.[0-9]+", args.image):
        raise ValueError("Pin an official numeric GoRun release image; latest/custom images are not accepted")
    if not 1 <= args.https_timeout <= 1800:
        raise ValueError("HTTPS timeout must be between 1 and 1800 seconds")
    addresses = check_dns(domain, expected)
    name = project_name(directory)
    if args.resume:
        state = load_state(directory, domain, args.image)
    elif directory.exists():
        raise ValueError("Directory already exists; use a new directory or --resume for this script's deployment")
    if args.dry_run:
        print(json.dumps({"mode": "dry-run; nothing changed", "domain": domain,
                          "resolved_addresses": addresses, "installation": str(directory),
                          "data": str(directory / "data"), "image": args.image,
                          "proxy": CADDY_IMAGE, "ports": [80, 443],
                          "requirements": "Linux root, Docker Engine + Compose v2, externally reachable TCP 80/443"}, indent=2))
        return 0
    if sys.platform != "linux" or os.geteuid() != 0:
        raise RuntimeError("Deployment requires Linux root with Docker Engine and Compose v2; --dry-run works without root")
    if os.environ.get("DOCKER_HOST", "unix:///var/run/docker.sock").split(":", 1)[0] != "unix":
        raise RuntimeError("Use a local Docker Engine; remote Docker hosts are not supported")
    endpoint = json.loads(run(["docker", "context", "inspect"]))[0]["Endpoints"]["docker"]["Host"]
    if not endpoint.startswith("unix://"):
        raise RuntimeError("Use a local Docker Engine context")
    security = run(["docker", "info", "--format", "{{json .SecurityOptions}}"])
    if "rootless" in security or "userns" in security:
        raise RuntimeError("This bind-mounted data layout requires rootful Docker without user namespace remapping")
    if run(["docker", "info", "--format", "{{.OSType}}"]).strip() != "linux":
        raise RuntimeError("Docker Engine must run Linux containers")
    run(["docker", "compose", "version"])
    if not args.resume:
        check_ports()
        state = create_deployment(directory, domain, args.image, name)
    elif directory.stat().st_uid != 0 or directory.stat().st_mode & 0o077:
        raise ValueError("Deployment directory must be root-owned and private (0700)")
    elif any((directory / child).is_symlink() or not (directory / child).is_dir()
             for child in ("data", "tls-data", "tls-config")):
        raise ValueError("Deployment data directories must remain real directories")
    elif network_address(state["network"]) != state["proxy_ip"]:
        raise ValueError("Deployment network changed; inspect manually before starting")
    command = ["docker", "compose", "--project-directory", str(directory), "-p", name, "-f", str(directory / "compose.json")]
    run(command + ["config", "--quiet"])
    run(command + ["pull"])
    run(command + ["up", "-d", "--wait", "--wait-timeout", "120"])
    wait_https(domain, addresses, args.https_timeout)
    print("HTTPS and health checks passed for every configured origin address.")
    print("Site: https://" + domain)
    print("Admin setup: https://" + domain + "/admin/setup")
    print("Installation: " + str(directory) + "\nData: " + str(directory / "data"))
    print("Setup token is stored privately in " + str(directory / "secrets.env") + "; enter it directly in the browser, never in an assistant chat.")
    print("No admin account was recreated. Complete first admin setup in your browser; existing accounts are preserved.")
    print("Stop (preserves data): " + shlex.join(command + ["stop"]))
    print("Start: " + shlex.join(command + ["up", "-d", "--wait"]))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (ValueError, RuntimeError, OSError, KeyError) as error:
        print("Setup incomplete: " + str(error), file=sys.stderr)
        sys.exit(1)
