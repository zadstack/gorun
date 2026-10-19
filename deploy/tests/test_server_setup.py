import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import socket
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("server_setup", Path(__file__).resolve().parents[1] / "server-setup.py")
setup = importlib.util.module_from_spec(spec)
spec.loader.exec_module(setup)


class ServerSetupTests(unittest.TestCase):
    def test_domain_and_ip_inputs_reject_injection_and_nonpublic_addresses(self):
        for value in ("x; touch /tmp/pwn", "https://site.com", "site.com:443", "*.site.com", "foo.localhost", "foo.test", "a..com", "-x.com", "127.0.0.1"):
            with self.subTest(value=value), self.assertRaises(ValueError):
                setup.domain_name(value)
        self.assertEqual(setup.domain_name("Shop.Example.COM."), "shop.example.com")
        for value in ("127.0.0.1", "10.0.0.1", "::1", "169.254.169.254"):
            with self.assertRaises(ValueError):
                setup.public_ips([value])

    def test_dns_requires_exact_a_and_aaaa_match(self):
        records = [(socket.AF_INET, 1, 6, "", ("8.8.8.8", 443)),
                   (socket.AF_INET6, 1, 6, "", ("2606:4700:4700::1111", 443, 0, 0))]
        with patch.object(setup.socket, "getaddrinfo", return_value=records):
            with self.assertRaisesRegex(ValueError, "AAAA"):
                setup.check_dns("site.com", {"8.8.8.8"})
            self.assertEqual(len(setup.check_dns("site.com", {"8.8.8.8", "2606:4700:4700::1111"})), 2)

    def test_dry_run_never_mutates_or_calls_docker_or_generates_secrets(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp) / "new site"
            output = io.StringIO()
            with patch.object(setup, "check_dns", return_value=["8.8.8.8"]), patch.object(setup, "run", side_effect=AssertionError("Docker called")), patch.object(setup.secrets, "token_hex", side_effect=AssertionError("secret generated")), contextlib.redirect_stdout(output):
                self.assertEqual(setup.main(["--domain", "site.com", "--directory", str(directory), "--server-ip", "8.8.8.8", "--dry-run"]), 0)
            self.assertFalse(directory.exists())
            self.assertEqual(json.loads(output.getvalue())["data"], str(directory.resolve() / "data"))

    def test_no_host_app_port_and_exact_proxy_trust(self):
        config = setup.compose_config("site.com", setup.DEFAULT_IMAGE, "site-network", "172.31.0.2")
        app = config["services"]["gorun"]
        self.assertNotIn("ports", app)
        self.assertEqual(app["environment"]["GORUN_ENV"], "prod")
        self.assertEqual(app["environment"]["GORUN_TRUSTED_PROXIES"], "172.31.0.2")
        self.assertEqual(app["restart"], "unless-stopped")
        self.assertEqual(config["services"]["proxy"]["depends_on"]["gorun"]["condition"], "service_healthy")

    def test_preserves_secrets_and_data_on_resume_and_detects_modified_config(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp) / "new site with spaces"
            with patch.object(setup, "run", return_value=""), patch.object(setup, "network_address", return_value="172.31.0.2"), patch.object(setup.os, "chown"):
                state = setup.create_deployment(directory, "site.com", setup.DEFAULT_IMAGE, "test-project")
            secrets = (directory / "secrets.env").read_bytes()
            (directory / "data" / "existing.db").write_bytes(b"existing account")
            self.assertEqual(setup.load_state(directory, "site.com", setup.DEFAULT_IMAGE), state)
            self.assertEqual((directory / "secrets.env").read_bytes(), secrets)
            self.assertEqual((directory / "data" / "existing.db").read_bytes(), b"existing account")
            self.assertEqual((directory / "secrets.env").stat().st_mode & 0o777, 0o600)
            self.assertEqual(directory.stat().st_mode & 0o777, 0o700)
            with self.assertRaises(ValueError):
                setup.load_state(directory, "other.com", setup.DEFAULT_IMAGE)
            (directory / "Caddyfile").write_text("changed")
            with self.assertRaisesRegex(ValueError, "changed"):
                setup.load_state(directory, "site.com", setup.DEFAULT_IMAGE)

    def test_existing_directory_never_overwritten_even_if_empty(self):
        with tempfile.TemporaryDirectory() as temp:
            with self.assertRaisesRegex(ValueError, "existing"):
                setup.create_deployment(Path(temp), "site.com", setup.DEFAULT_IMAGE, "test-project")
            file = Path(temp) / "existing"
            file.write_text("keep")
            with self.assertRaises(FileExistsError):
                setup.write_private(file, "replace")
            self.assertEqual(file.read_text(), "keep")

    def test_https_failure_never_reports_deployment_success(self):
        with patch.object(setup, "probe_https", side_effect=OSError("no certificate")), patch.object(setup.time, "monotonic", side_effect=[0, 2]):
            with self.assertRaisesRegex(RuntimeError, "No public deployment success"):
                setup.wait_https("site.com", ["8.8.8.8"], 1)

    def test_subprocess_failure_redacts_stderr_and_environment(self):
        import subprocess
        result = subprocess.CompletedProcess(["docker"], 1, "secret env", "secret key")
        with patch.object(setup.subprocess, "run", return_value=result):
            with self.assertRaisesRegex(RuntimeError, "Command failed") as caught:
                setup.run(["docker", "compose", "config"])
            self.assertNotIn("secret", str(caught.exception))


if __name__ == "__main__":
    unittest.main()
