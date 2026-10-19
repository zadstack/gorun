#!/usr/bin/env python3
"""Exercise the published README commands with a clean HOME and no Go on PATH.

All credentials remain in memory. Output is limited to non-secret check results.
The release workflow runs this as nobody in a disposable Debian container.
"""
import argparse
import http.cookiejar
import os
from pathlib import Path
import queue
import re
import shutil
import signal
import subprocess
import tempfile
import threading
import urllib.parse
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--tag", default=os.environ.get("SMOKE_TAG", "v0.4.0"))
    args = parser.parse_args()
    if not re.fullmatch(r"v[0-9][A-Za-z0-9.+-]*", args.tag):
        parser.error("expected a release tag")
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    url = f"https://raw.githubusercontent.com/zadstack/gorun/{args.tag}/README.md"
    with opener.open(url, timeout=30) as response:
        readme = response.read().decode()
    match = re.search(r"```sh\n(.*?)\n```", readme, re.S)
    if not match or "install.sh" not in match[1]:
        raise RuntimeError("The published README does not begin with the installer block")
    block = match[1]
    with tempfile.TemporaryDirectory(prefix="gorun-clean-install-") as temp:
        root = Path(temp)
        home = root / "fresh home"
        home.mkdir()
        launchers = root / "launchers"
        launchers.mkdir()
        # Record the browser launch without opening a host GUI or requiring one.
        for name in ["open", "xdg-open"]:
            launcher = launchers / name
            launcher.write_text('#!/bin/sh\nprintf "%s\\n" "$1" > "$HOME/browser-request"\n')
            launcher.chmod(0o755)
        env = {"HOME": str(home), "PATH": f"{launchers}:/usr/bin:/bin", "SHELL": "/bin/sh",
               "TMPDIR": str(root), "LANG": "C.UTF-8"}
        if shutil.which("go", path=env["PATH"]) or shutil.which("gorun", path=env["PATH"]):
            raise RuntimeError("Smoke environment must not contain Go or GoRun")
        script = root / "readme-commands.sh"
        script.write_text(block + "\n")
        password = None
        for attempt in range(2):
            child = subprocess.Popen(["/bin/sh", str(script)], cwd=root, env=env,
                                     stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                     text=True, start_new_session=True)
            lines = queue.Queue()

            def collect(stream):
                for line in stream:
                    lines.put(line)
                lines.put(None)

            threading.Thread(target=collect, args=(child.stdout,), daemon=True).start()
            # Drain errors without ever reflecting child output or credentials.
            threading.Thread(target=lambda: child.stderr.read(), daemon=True).start()
            try:
                local = None
                while True:
                    line = lines.get(timeout=180)
                    if line is None:
                        raise RuntimeError("README block exited before the site became ready")
                    if line.startswith("Local: "):
                        local = line.removeprefix("Local: ").strip()
                    if line.startswith("Password (shown once; save it now): "):
                        if attempt:
                            raise RuntimeError("Restart displayed a new password")
                        password = line.split(": ", 1)[1].strip()
                        break
                    if line.startswith("Existing accounts preserved;"):
                        if not attempt:
                            raise RuntimeError("Fresh HOME did not create the first account")
                        break
                if not local or not password or len(password) != 43:
                    raise RuntimeError("Local URL or generated password is missing")
                with opener.open(local, timeout=15) as response:
                    if response.status != 200:
                        raise RuntimeError("Installed site did not serve the home page")
                cookies = http.cookiejar.CookieJar()
                browser = urllib.request.build_opener(urllib.request.ProxyHandler({}),
                                                       urllib.request.HTTPCookieProcessor(cookies))
                with browser.open(local + "/admin/login", timeout=15) as response:
                    form = response.read().decode()
                csrf = re.search(r'name="_csrf" value="([^"]+)"', form)
                if not csrf:
                    raise RuntimeError("Admin login form did not provide CSRF protection")
                values = urllib.parse.urlencode({"email": "admin", "password": password,
                                                 "_csrf": csrf[1]}).encode()
                with browser.open(local + "/admin/login", data=values, timeout=15) as response:
                    if urllib.parse.urlparse(response.url).path != "/admin/onboarding":
                        raise RuntimeError("Generated credentials did not reach first-login setup")
                if not (home / "browser-request").exists():
                    raise RuntimeError("Browser launcher was not invoked")
            finally:
                if child.poll() is None:
                    os.killpg(child.pid, signal.SIGINT)
                    try:
                        child.wait(timeout=30)
                    except subprocess.TimeoutExpired:
                        os.killpg(child.pid, signal.SIGKILL)
                        child.wait()
                        raise RuntimeError("Installed server did not stop cleanly")
        command = subprocess.run(["/bin/sh", "-c", '. "$HOME/.profile"; command -v gorun'],
                                 cwd=root, env=env, text=True, capture_output=True, timeout=10)
        if command.returncode or command.stdout.strip() != str(home / ".local/bin/gorun"):
            raise RuntimeError("Shell startup did not make gorun discoverable")
        print("Published README smoke passed: no Go or old installation; verified download;")
        print("site HTTP, admin sign-in, browser launch request, shell PATH and restart preservation.")


if __name__ == "__main__":
    main()
