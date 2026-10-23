#!/usr/bin/env python3
"""Check the documentation entry points. Run from the repository root.

1. Every guide in docs/*.md is linked from docs/README.md, so nothing ships
   undiscoverable.
2. Every relative link in README.md, README.ar.md and docs/README.md points to
   a file or directory that exists (images included).
"""

import re
import sys
from pathlib import Path

ROOT = Path(".")
ENTRY_POINTS = [Path("README.md"), Path("README.ar.md"), Path("docs/README.md")]
# Markdown links and images, plus HTML src/href attributes.
LINK = re.compile(r"\]\(([^)\s]+)(?:\s+\"[^\"]*\")?\)|(?:src|href)=\"([^\"]+)\"")


def links(text):
    for m in LINK.finditer(text):
        yield m.group(1) or m.group(2)


def main():
    problems = []
    index = Path("docs/README.md")
    listed = {target.split("#", 1)[0] for target in links(index.read_text(encoding="utf-8"))}
    for guide in sorted(Path("docs").glob("*.md")):
        if guide.name != "README.md" and guide.name not in listed:
            problems.append(f"docs/README.md does not link {guide}")

    for page in ENTRY_POINTS:
        for target in links(page.read_text(encoding="utf-8")):
            if re.match(r"^[a-z][a-z0-9+.-]*:", target) or target.startswith("#"):
                continue
            path = target.split("#", 1)[0].split("?", 1)[0]
            if path and not (page.parent / path).exists():
                problems.append(f"{page}: broken link {target}")

    for p in problems:
        print(p)
    if problems:
        print(f"{len(problems)} documentation problem(s)")
        return 1
    print("documentation links ok")
    return 0


if __name__ == "__main__":
    sys.exit(main())
