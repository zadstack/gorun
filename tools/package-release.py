#!/usr/bin/env python3
"""Build self-contained GoRun binary archives without publishing them."""
import argparse
import hashlib
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    parser.add_argument("--target", action="append", choices=[
        "darwin/arm64", "darwin/amd64", "linux/amd64", "linux/arm64",
    ])
    parser.add_argument("--output", type=Path, default=Path("dist"))
    args = parser.parse_args()
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,79}", args.version):
        parser.error("version must be a short filename-safe identifier")
    root = Path(__file__).resolve().parent.parent
    targets = list(dict.fromkeys(args.target or ["/".join(
        subprocess.check_output(["go", "env", "GOOS", "GOARCH"], cwd=root, text=True).split()
    )]))
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    names = [f"gorun-{args.version}-{t.replace('/', '-')}.tar.gz" for t in targets]
    sums_path = output / f"SHA256SUMS-{args.version}.txt"
    for name in names + [sums_path.name]:
        if (output / name).exists():
            parser.error(f"refusing to overwrite {output / name}")
    checksums = []
    with tempfile.TemporaryDirectory(prefix="gorun-release-") as temporary:
        for target, name in zip(targets, names):
            goos, goarch = target.split("/")
            package = Path(temporary) / name.removesuffix(".tar.gz")
            package.mkdir()
            subprocess.run([
                "go", "build", "-trimpath", "-ldflags",
                f"-s -w -X main.version={args.version}", "-o", str(package / "gorun"), "./cmd/gorun",
            ], cwd=root, env={**os.environ, "CGO_ENABLED": "0", "GOOS": goos, "GOARCH": goarch}, check=True)
            shutil.copytree(root / "themes", package / "themes")
            for document in ["README.md", "LICENSE", "NOTICE", "SECURITY.md", "CHANGELOG.md"]:
                shutil.copy2(root / document, package / document)
            shutil.copytree(root / "docs", package / "docs")
            archive = Path(temporary) / name
            with tarfile.open(archive, "w:gz") as tar:
                tar.add(package, arcname=package.name)
            with archive.open("rb") as source:
                digest = hashlib.file_digest(source, "sha256").hexdigest()
            destination = output / name
            with destination.open("xb") as out, archive.open("rb") as source:
                shutil.copyfileobj(source, out)
            checksums.append(f"{digest}  {name}\n")
            print(destination)
    with sums_path.open("x") as sums:
        sums.writelines(checksums)
    print(sums_path)


if __name__ == "__main__":
    main()
