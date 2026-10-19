#!/usr/bin/env python3
"""Disposable Linux fixture: real released GoRun + Caddy, private test CA, no public DNS/ACME claim."""
import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import secrets
import sqlite3
import subprocess
import tempfile
import time
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("server_setup", Path(__file__).resolve().parents[1] / "server-setup.py")
setup = importlib.util.module_from_spec(spec)
spec.loader.exec_module(setup)


def main():
    if os.geteuid() != 0:
        raise RuntimeError("Disposable integration test requires Linux root")
    with tempfile.TemporaryDirectory(prefix="gorun-production-fixture-") as temp:
        directory = Path(temp) / "site with spaces"
        domain = "gorun-fixture.test"
        name = setup.project_name(directory)
        command = ["docker", "compose", "--project-directory", str(directory), "-p", name,
                   "-f", str(directory / "compose.json")]
        state = None
        try:
            state = setup.create_deployment(directory, domain, setup.DEFAULT_IMAGE, name)
            # Only the test uses Caddy's private CA. Production always uses automatic public TLS.
            (directory / "Caddyfile").write_text("{\n    skip_install_trust\n}\n" + domain + " {\n    tls internal\n    reverse_proxy gorun:8080\n}\n")
            state["files"]["Caddyfile"] = setup.hashlib.sha256((directory / "Caddyfile").read_bytes()).hexdigest()
            (directory / "deployment.json").write_text(json.dumps(state))
            setup.run(command + ["config", "--quiet"])
            setup.run(command + ["pull"])
            setup.run(command + ["up", "-d", "--wait", "--wait-timeout", "120"])
            root = directory / "tls-data/caddy/pki/authorities/local/root.crt"
            deadline = time.monotonic() + 60
            while not root.exists():
                if time.monotonic() > deadline:
                    raise RuntimeError("Test CA was not created")
                time.sleep(1)
            with patch.dict(os.environ, {"SSL_CERT_FILE": str(root)}):
                setup.wait_https(domain, ["127.0.0.1"], 60)
                setup.probe_https(domain, "127.0.0.1", "/admin/setup")
                original_secret = (directory / "secrets.env").read_bytes()
                password = secrets.token_urlsafe(32)
                result = subprocess.run(command + ["run", "--rm", "-T", "--entrypoint", "/app/gorun", "gorun", "user", "create", "--email", "fixture@example.test", "--admin", "--password-stdin"],
                                        input=password + "\n", text=True, capture_output=True)
                if result.returncode:
                    raise RuntimeError("Fixture admin creation failed (output withheld)")
                setup.run(command + ["stop"])
                with sqlite3.connect(directory / "data/gorun.db") as database:
                    before = database.execute("SELECT id,password_hash FROM users WHERE email=?", ("fixture@example.test",)).fetchone()
                    if before is None:
                        raise RuntimeError("Fixture account missing")
                # The real resume flow runs against existing containers/data. DNS alone is a fixture;
                # TLS is a real certificate/hostname check trusting only this test's private CA.
                output = io.StringIO()
                with patch.object(setup, "check_dns", return_value=["127.0.0.1"]), contextlib.redirect_stdout(output):
                    setup.main(["--domain", domain, "--directory", str(directory), "--server-ip", "8.8.8.8", "--resume", "--https-timeout", "60"])
                if "HTTPS and health checks passed" not in output.getvalue():
                    raise RuntimeError("Resume acceptance did not complete")
                if password in output.getvalue() or "GORUN_SECRET_KEY=" in output.getvalue():
                    raise RuntimeError("Secret leaked in setup output")
                if original_secret != (directory / "secrets.env").read_bytes():
                    raise RuntimeError("Resume regenerated secrets")
                setup.run(command + ["stop"])
                with sqlite3.connect(directory / "data/gorun.db") as database:
                    after = database.execute("SELECT id,password_hash FROM users WHERE email=?", ("fixture@example.test",)).fetchone()
                    count = database.execute("SELECT COUNT(*) FROM users").fetchone()[0]
                if after != before or count != 1:
                    raise RuntimeError("Resume changed or recreated existing account")
            print("PASS: real released image, Compose validation/start, Caddy private-CA HTTPS and HSTS, public app routes, restart/resume, stable secrets and one unchanged admin.")
            print("BOUNDARY: test DNS and private CA are fixtures; public DNS, firewall and ACME issuance require actual-host acceptance.")
        finally:
            if (directory / "compose.json").exists():
                subprocess.run(command + ["down"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            if state:
                subprocess.run(["docker", "network", "rm", state["network"]], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


if __name__ == "__main__":
    main()
