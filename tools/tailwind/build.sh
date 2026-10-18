#!/usr/bin/env bash
# Builds themes/default/assets/tailwind.css with the pinned Tailwind standalone
# CLI (no Node or npm). The CLI is downloaded once into a cache directory and
# verified against the SHA-256 checksums of the upstream release below.
#
#   tools/tailwind/build.sh          rebuild the committed CSS
#   tools/tailwind/build.sh --check  fail if the committed CSS is stale (CI)
#
# TAILWIND_CACHE overrides the cache directory; TAILWIND_BIN uses an existing,
# already verified binary of the same version.
set -euo pipefail

VERSION=v3.4.17
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
out="$root/themes/default/assets/tailwind.css"

case "$(uname -s)-$(uname -m)" in
  Linux-x86_64)          asset=tailwindcss-linux-x64;   sum=7d24f7fa191d2193b78cd5f5a42a6093e14409521908529f42d80b11fde1f1d4 ;;
  Linux-aarch64|Linux-arm64) asset=tailwindcss-linux-arm64; sum=69b1378b8133192d7d2feb12a116fa12d035594f58db3eff215879e4ad8cf39b ;;
  Darwin-x86_64)         asset=tailwindcss-macos-x64;   sum=6cbdad74be776c087ffa5e9a057512c54898f9fe8828d3362212dfe32fc933a3 ;;
  Darwin-arm64)          asset=tailwindcss-macos-arm64; sum=a1d0c7985759accca0bf12e51ac1dcbf0f6cf2fffb62e6e0f62d091c477a10a3 ;;
  *) echo "tailwind: no pinned standalone CLI for $(uname -s)-$(uname -m)" >&2; exit 1 ;;
esac

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
  else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

bin=${TAILWIND_BIN:-}
if [ -z "$bin" ]; then
  cache=${TAILWIND_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/gorun-tailwind}/$VERSION
  bin="$cache/$asset"
  if [ ! -x "$bin" ] || [ "$(sha256 "$bin")" != "$sum" ]; then
    mkdir -p "$cache"
    tmp="$bin.download"
    echo "tailwind: downloading $asset $VERSION" >&2
    curl -fsSL --retry 3 -o "$tmp" "https://github.com/tailwindlabs/tailwindcss/releases/download/$VERSION/$asset"
    got=$(sha256 "$tmp")
    if [ "$got" != "$sum" ]; then
      rm -f "$tmp"
      echo "tailwind: checksum mismatch for $asset: got $got, want $sum" >&2
      exit 1
    fi
    chmod +x "$tmp"
    mv "$tmp" "$bin"
  fi
fi

target=$out
if [ "${1:-}" = "--check" ]; then
  target=$(mktemp "${TMPDIR:-/tmp}/tailwind.XXXXXX.css")
  trap 'rm -f "$target"' EXIT
fi

(cd "$here" && "$bin" -c tailwind.config.js -i input.css -o "$target" --minify 2>/dev/null) || {
  (cd "$here" && "$bin" -c tailwind.config.js -i input.css -o "$target" --minify)
  exit 1
}

if [ "${1:-}" = "--check" ]; then
  if ! cmp -s "$target" "$out"; then
    echo "tailwind: themes/default/assets/tailwind.css is out of date." >&2
    echo "Run tools/tailwind/build.sh (or make css) and commit the result." >&2
    exit 1
  fi
  echo "tailwind: themes/default/assets/tailwind.css is up to date."
else
  echo "tailwind: wrote ${out#"$root"/} ($(wc -c <"$out" | tr -d ' ') bytes)"
fi
