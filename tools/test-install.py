#!/usr/bin/env python3
"""Offline regression checks for the release installer; no network or user files."""
import hashlib
import io
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent


class InstallTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="gorun-installer-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.home = self.root / "home with spaces"
        self.home.mkdir()
        self.fixture = self.root / "releases"
        self.fixture.mkdir()
        self.mocks = self.root / "mocks"
        self.mocks.mkdir()
        self.env = dict(os.environ, HOME=str(self.home), GORUN_VERSION="9.9.9",
                        PATH=str(self.mocks) + os.pathsep + os.environ["PATH"],
                        TEST_RELEASES=str(self.fixture), TEST_OS="Linux", TEST_ARCH="x86_64")
        self.env.pop("GORUN_INSTALL_DIR", None)
        self.env.pop("GORUN_DATA", None)
        self.mock("uname", '#!/bin/sh\ncase "$1" in -s) echo "$TEST_OS";; -m) echo "$TEST_ARCH";; esac\n')
        self.mock("curl", '''#!/usr/bin/env python3
import os, pathlib, shutil, sys
args = sys.argv[1:]
if '%{url_effective}' in args:
    print('https://github.com/zadstack/gorun/releases/tag/v9.9.9', end='')
    sys.exit(0)
url = next(a for a in args if a.startswith('https://'))
source = pathlib.Path(os.environ['TEST_RELEASES']) / url.rsplit('/', 1)[1]
if not source.is_file(): sys.exit(22)
shutil.copyfile(source, args[args.index('-o') + 1])
''')
        self.make_release()

    def mock(self, name, text):
        path = self.mocks / name
        path.write_text(text)
        path.chmod(0o755)

    def make_release(self, platform="linux", arch="amd64", modern=True):
        name = f"gorun-9.9.9-{platform}-{arch}"
        archive = self.fixture / (name + ".tar.gz")
        binary = ("#!/bin/sh\nif [ \"$1\" = help ]; then\n  echo '  " +
                  ("start" if modern else "serve") + " run server'\nelse\n"
                  "  printf '%s\\n' \"$@\" > \"$HOME/start-args\"\nfi\n").encode()
        with tarfile.open(archive, "w:gz") as tar:
            info = tarfile.TarInfo(name + "/gorun")
            info.mode, info.size = 0o755, len(binary)
            tar.addfile(info, io.BytesIO(binary))
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        (self.fixture / "SHA256SUMS-9.9.9.txt").write_text(f"{digest}  {archive.name}\n")
        return archive

    def run_installer(self, *args):
        return subprocess.run(["sh", str(ROOT / "install.sh"), *args], env=self.env,
                              text=True, capture_output=True, timeout=20)

    @property
    def installed(self):
        return self.home / ".local/bin/gorun"

    def test_install_and_start_with_spaces_and_stable_data(self):
        result = self.run_installer("--start")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(self.installed.is_file())
        self.assertEqual((self.home / "start-args").read_text().splitlines(),
                         ["start", "--data", str(self.home / ".local/share/gorun/site")])
        self.assertEqual(list(self.installed.parent.glob(".gorun.*")), [])

    def test_latest_macos_arm(self):
        self.env.pop("GORUN_VERSION")
        self.env.update(TEST_OS="Darwin", TEST_ARCH="arm64")
        self.make_release("darwin", "arm64")
        result = self.run_installer()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(self.installed.is_file())

    def preserve_existing(self):
        self.installed.parent.mkdir(parents=True)
        self.installed.write_text("existing binary")

    def test_bad_checksum_preserves_existing(self):
        self.preserve_existing()
        archive = next(self.fixture.glob("*.tar.gz"))
        archive.write_bytes(archive.read_bytes() + b"tampered")
        result = self.run_installer()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Checksum mismatch", result.stderr)
        self.assertEqual(self.installed.read_text(), "existing binary")

    def test_old_release_preserves_existing(self):
        self.preserve_existing()
        self.make_release(modern=False)
        result = self.run_installer()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("predates gorun start", result.stderr)
        self.assertEqual(self.installed.read_text(), "existing binary")

    def test_duplicate_or_missing_checksum(self):
        sums = self.fixture / "SHA256SUMS-9.9.9.txt"
        valid = sums.read_text()
        for content in ["", valid + valid]:
            with self.subTest(content=bool(content)):
                sums.write_text(content)
                result = self.run_installer()
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(self.installed.exists())

    def test_failed_download_preserves_existing(self):
        self.preserve_existing()
        next(self.fixture.glob("*.tar.gz")).unlink()
        result = self.run_installer()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.installed.read_text(), "existing binary")

    def test_unsupported_platform_and_invalid_version(self):
        self.env["TEST_OS"] = "Windows"
        result = self.run_installer()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.installed.exists())
        self.env.update(TEST_OS="Linux", GORUN_VERSION="../../elsewhere")
        result = self.run_installer()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.installed.exists())


if __name__ == "__main__":
    unittest.main()
