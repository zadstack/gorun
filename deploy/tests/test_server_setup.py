import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import socket
import tarfile
import tempfile
import unittest
from unittest.mock import MagicMock, patch

spec = importlib.util.spec_from_file_location("server_setup", Path(__file__).resolve().parents[1] / "server-setup.py")
setup = importlib.util.module_from_spec(spec)
spec.loader.exec_module(setup)


class ServerSetupTests(unittest.TestCase):
    def test_domain_and_ip_inputs_reject_injection_and_nonpublic_addresses(self):
        for value in ("x; touch /tmp/pwn", "https://site.com", "site.com:443", "*.site.com", "foo.localhost", "foo.test", "a..com", "-x.com", "127.0.0.1"):
            with self.subTest(value=value), self.assertRaises(ValueError):
                setup.domain_name(value)
        self.assertEqual(setup.domain_name("Shop.Example.COM."), "shop.example.com")
        for value in ("127.0.0.1", "10.0.0.1", "::1", "169.254.169.254", "224.0.0.1", "ff02::1"):
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
            with patch.object(setup, "check_dns", return_value=["8.8.8.8"]), patch.object(setup, "resolve_latest_version", return_value="0.5.0"), patch.object(setup, "run", side_effect=AssertionError("Docker called")), patch.object(setup.secrets, "token_bytes", side_effect=AssertionError("secret generated")), contextlib.redirect_stdout(output):
                self.assertEqual(setup.main(["--domain", "site.com", "--directory", str(directory), "--server-ip", "8.8.8.8", "--dry-run"]), 0)
            self.assertFalse(directory.exists())
            self.assertEqual(json.loads(output.getvalue())["data"], str(directory.resolve() / "data"))

    def api_response(self, payload, status=200, url=setup.LATEST_RELEASE_API):
        response = MagicMock()
        response.__enter__.return_value = response
        response.status, response.url = status, url
        response.read.return_value = payload if isinstance(payload, bytes) else json.dumps(payload).encode()
        return response

    def test_latest_stable_is_numeric_bounded_https_and_unauthenticated(self):
        response = self.api_response({"tag_name": "v0.5.0", "draft": False, "prerelease": False})
        opener = MagicMock()
        opener.open.return_value = response
        with patch.object(setup.urllib.request, "build_opener", return_value=opener):
            self.assertEqual(setup.resolve_latest_version(), "0.5.0")
        request = opener.open.call_args.args[0]
        self.assertEqual(request.full_url, setup.LATEST_RELEASE_API)
        self.assertIsNone(request.get_header("Authorization"))
        self.assertEqual(opener.open.call_args.kwargs["timeout"], 15)
        response.read.assert_called_once_with(setup.LATEST_RELEASE_LIMIT + 1)

    def test_latest_rejects_prereleases_drafts_old_tags_invalid_or_excessive_json(self):
        stable = {"tag_name": "v0.5.0", "draft": False, "prerelease": False}
        payloads = [dict(stable, prerelease=True), dict(stable, draft=True),
                    {"tag_name": "v0.5.0"}, [], b"not JSON", b" " * (setup.LATEST_RELEASE_LIMIT + 1)]
        for tag in ("v0.5.0-rc.1", "v0.5.0+custom", "0.5.0", "v0.3.9", "v00.5.0", "vlatest"):
            payloads.append(dict(stable, tag_name=tag))
        for payload in payloads:
            opener = MagicMock()
            opener.open.return_value = self.api_response(payload)
            with self.subTest(payload=str(payload)[:80]), patch.object(setup.urllib.request, "build_opener", return_value=opener):
                with self.assertRaisesRegex(RuntimeError, "no older release was selected"):
                    setup.resolve_latest_version()

    def test_latest_api_errors_and_redirects_fail_clearly(self):
        opener = MagicMock()
        opener.open.side_effect = setup.urllib.error.URLError("synthetic unavailable")
        with patch.object(setup.urllib.request, "build_opener", return_value=opener):
            with self.assertRaisesRegex(RuntimeError, "pass --version X.Y.Z"):
                setup.resolve_latest_version()
        opener.open.side_effect = setup.http.client.IncompleteRead(b"partial")
        with patch.object(setup.urllib.request, "build_opener", return_value=opener):
            with self.assertRaisesRegex(RuntimeError, "no older release was selected"):
                setup.resolve_latest_version()
        for url in ("http://api.github.com/redirect", "https://other.example/redirect"):
            with self.assertRaises(setup.urllib.error.HTTPError):
                setup.NoReleaseRedirect().redirect_request(
                    setup.urllib.request.Request(setup.LATEST_RELEASE_API), None, 302, "redirect", {}, url)
        for response in (self.api_response({}, status=403), self.api_response({}, url="https://other.example/")):
            opener = MagicMock()
            opener.open.return_value = response
            with patch.object(setup.urllib.request, "build_opener", return_value=opener):
                with self.assertRaisesRegex(RuntimeError, "Could not resolve"):
                    setup.resolve_latest_version()

    def test_default_new_install_resolves_once_and_explicit_version_never_resolves(self):
        for explicit in (False, True):
            with tempfile.TemporaryDirectory() as temp:
                directory = Path(temp) / "new site"
                output = io.StringIO()
                with patch.object(setup, "check_dns", return_value=["8.8.8.8"]), patch.object(setup, "resolve_latest_version", return_value="0.5.0") as latest, patch.object(setup, "run", side_effect=AssertionError("Docker called")), contextlib.redirect_stdout(output):
                    args = ["--domain", "site.com", "--directory", str(directory), "--server-ip", "8.8.8.8", "--dry-run"]
                    self.assertEqual(setup.main(args + (["--version", "0.4.0"] if explicit else [])), 0)
                if explicit:
                    latest.assert_not_called()
                else:
                    latest.assert_called_once_with()
                self.assertEqual(json.loads(output.getvalue())["release"], "0.4.0" if explicit else "0.5.0")
                self.assertFalse(directory.exists())

    def test_resume_keeps_manifest_version_when_latest_moves_without_network(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp) / "existing site"
            with patch.object(setup, "run", return_value=""), patch.object(setup, "network_address", return_value="172.31.0.2"), patch.object(setup.os, "chown"), patch.object(setup, "create_network"), patch.object(setup, "build_runtime", return_value=("sha256:" + "b" * 64, {"release": "0.4.0"})), patch.object(setup, "resolve_image", side_effect=lambda image: image.rsplit(":", 1)[0] + "@sha256:" + "a" * 64):
                setup.create_deployment(directory, "site.com", "0.4.0", "test-project")
            output = io.StringIO()
            with patch.object(setup, "check_dns", return_value=["8.8.8.8"]), patch.object(setup, "resolve_latest_version", return_value="0.5.0") as latest, patch.object(setup.urllib.request, "build_opener", side_effect=AssertionError("latest network call on resume")), patch.object(setup, "run", side_effect=AssertionError("Docker called")), contextlib.redirect_stdout(output):
                args = ["--domain", "site.com", "--directory", str(directory), "--server-ip", "8.8.8.8", "--dry-run", "--resume"]
                self.assertEqual(setup.main(args), 0)
                with self.assertRaisesRegex(ValueError, "another domain/version"):
                    setup.main(args + ["--version", "0.5.0"])
            latest.assert_not_called()
            self.assertEqual(json.loads(output.getvalue())["release"], "0.4.0")
            self.assertEqual(setup.load_state(directory, "site.com")["version"], "0.4.0")

    def test_latest_failure_never_changes_installation_or_falls_back(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp) / "new site"
            opener = MagicMock()
            opener.open.side_effect = setup.urllib.error.URLError("synthetic rate limit")
            with patch.object(setup, "check_dns", return_value=["8.8.8.8"]), patch.object(setup.urllib.request, "build_opener", return_value=opener), patch.object(setup, "run", side_effect=AssertionError("Docker called")):
                with self.assertRaisesRegex(RuntimeError, "no older release was selected"):
                    setup.main(["--domain", "site.com", "--directory", str(directory), "--server-ip", "8.8.8.8", "--dry-run"])
            self.assertFalse(directory.exists())

    def test_no_host_app_port_and_exact_proxy_trust(self):
        config = setup.compose_config("site.com", "0.4.0", "site-network", "172.31.0.2")
        app = config["services"]["gorun"]
        self.assertNotIn("ports", app)
        self.assertEqual(app["environment"]["GORUN_ENV"], "prod")
        self.assertEqual(app["environment"]["GORUN_TRUSTED_PROXIES"], "172.31.0.2")
        self.assertEqual(app["restart"], "unless-stopped")
        self.assertEqual(app["networks"]["site"]["ipv4_address"], "172.31.0.3")
        self.assertEqual(config["services"]["proxy"]["depends_on"]["gorun"]["condition"], "service_healthy")

    def test_preserves_secrets_and_data_on_resume_and_detects_modified_config(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp) / "new site with spaces"
            with patch.object(setup, "run", return_value=""), patch.object(setup, "network_address", return_value="172.31.0.2"), patch.object(setup.os, "chown"), patch.object(setup, "create_network"), patch.object(setup, "build_runtime", return_value=("sha256:" + "b" * 64, {"release": "0.4.0"})), patch.object(setup, "resolve_image", side_effect=lambda image: image.rsplit(":", 1)[0] + "@sha256:" + "a" * 64):
                state = setup.create_deployment(directory, "site.com", "0.4.0", "test-project")
            secrets = (directory / "secrets.env").read_bytes()
            key = secrets.decode().splitlines()[0].split("=", 1)[1]
            self.assertEqual(len(setup.base64.b64decode(key, validate=True)), 32)
            (directory / "data" / "existing.db").write_bytes(b"existing account")
            self.assertEqual(setup.load_state(directory, "site.com", "0.4.0"), state)
            compose = json.loads((directory / "compose.json").read_text())
            self.assertEqual(compose["services"]["gorun"]["image"], "sha256:" + "b" * 64)
            self.assertIn("@sha256:", compose["services"]["proxy"]["image"])
            self.assertEqual((directory / "secrets.env").read_bytes(), secrets)
            self.assertEqual((directory / "data" / "existing.db").read_bytes(), b"existing account")
            self.assertEqual((directory / "secrets.env").stat().st_mode & 0o777, 0o600)
            self.assertEqual(directory.stat().st_mode & 0o777, 0o700)
            with self.assertRaises(ValueError):
                setup.load_state(directory, "other.com", "0.4.0")
            (directory / "Caddyfile").write_text("changed")
            with self.assertRaisesRegex(ValueError, "changed"):
                setup.load_state(directory, "site.com", "0.4.0")

    def test_existing_directory_never_overwritten_even_if_empty(self):
        with tempfile.TemporaryDirectory() as temp:
            with self.assertRaisesRegex(ValueError, "existing"):
                setup.create_deployment(Path(temp), "site.com", "0.4.0", "test-project")
            file = Path(temp) / "existing"
            file.write_text("keep")
            with self.assertRaises(FileExistsError):
                setup.write_private(file, "replace")
            self.assertEqual(file.read_text(), "keep")

    def test_https_failure_never_reports_deployment_success(self):
        with patch.object(setup, "probe_https", side_effect=OSError("no certificate")), patch.object(setup.time, "monotonic", side_effect=[0, 2]):
            with self.assertRaisesRegex(RuntimeError, "No public deployment success"):
                setup.wait_https("site.com", ["8.8.8.8"], 1)

    def test_network_uses_docker_allocated_subnet_explicitly_for_stable_endpoints(self):
        calls = []
        def run(args):
            calls.append(args)
            if "inspect" in args:
                return json.dumps([{"IPAM": {"Config": [{"Subnet": "172.31.96.0/20"}]}}])
            return ""
        with patch.object(setup, "run", side_effect=run):
            setup.create_network("fixture-site")
        self.assertEqual(calls[-1], ["docker", "network", "create", "--subnet", "172.31.96.0/20", "--label", "org.gorun.setup=fixture-site", "fixture-site"])
        self.assertIn(["docker", "network", "rm", "fixture-site-allocation"], calls)

    def test_verified_release_build_rejects_bad_or_duplicate_checksums_before_build(self):
        for sums in ("0" * 64 + "  gorun-0.4.0-linux-amd64.tar.gz\n",
                     "a" * 64 + "  gorun-0.4.0-linux-amd64.tar.gz\n" + "a" * 64 + "  gorun-0.4.0-linux-amd64.tar.gz\n"):
            calls = []
            def download(url, destination, limit):
                destination.write_bytes(b"archive" if url.endswith("tar.gz") else sums.encode())
            with patch.object(setup.platform, "machine", return_value="x86_64"), patch.object(setup, "resolve_image", return_value="base@sha256:" + "a" * 64), patch.object(setup, "download", side_effect=download), patch.object(setup, "run", side_effect=lambda args: calls.append(args)):
                with self.assertRaisesRegex(ValueError, "checksum"):
                    setup.build_runtime("0.4.0", "fixture")
            self.assertEqual(calls, [])

    def test_archive_extraction_rejects_links_and_parent_traversal(self):
        for suffix, kind in (("themes/../outside", tarfile.REGTYPE), ("themes/link", tarfile.SYMTYPE)):
            with tempfile.TemporaryDirectory() as temporary:
                folder = Path(temporary)
                archive = folder / "archive.tar.gz"
                root = "gorun-0.4.0-linux-amd64"
                with tarfile.open(archive, "w:gz") as package:
                    member = tarfile.TarInfo(root + "/" + suffix)
                    member.type = kind
                    member.linkname = "/etc/passwd" if kind == tarfile.SYMTYPE else ""
                    package.addfile(member, io.BytesIO())
                target = folder / "out"
                target.mkdir()
                with self.assertRaisesRegex(ValueError, "Unsafe"):
                    setup.extract_verified_release(archive, target, root)
                self.assertFalse((folder / "outside").exists())

    def test_subprocess_failure_redacts_stderr_and_environment(self):
        import subprocess
        result = subprocess.CompletedProcess(["docker"], 1, "secret env", "secret key")
        with patch.object(setup.subprocess, "run", return_value=result):
            with self.assertRaisesRegex(RuntimeError, "Command failed") as caught:
                setup.run(["docker", "compose", "config"])
            self.assertNotIn("secret", str(caught.exception))


if __name__ == "__main__":
    unittest.main()
