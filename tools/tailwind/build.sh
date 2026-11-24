#!/usr/bin/env bash
# Builds the committed Tailwind stylesheets with pinned Tailwind standalone
# CLIs (no Node or npm). Each CLI is downloaded once into a cache directory
# and verified against the SHA-256 checksums of its upstream release.
#
#   tools/tailwind/build.sh          rebuild the committed CSS
#   tools/tailwind/build.sh --check  fail if any committed CSS is stale (CI)
#
# Tailwind v3 (tailwind.config.js + input.css, core.config.js + core.css):
#   themes/default/assets/tailwind.css, the default theme's stylesheet, and
#   the first part of web/site/core.css, the core site stylesheet every theme
#   loads (shared-template utilities scoped under .gp-core, no reset, and
#   zero-specificity element defaults).
# Tailwind v4 (admin.css, which imports the vendored Basecoat source bundle):
#   web/admin/static/admin-tailwind.css, the admin's utilities and components.
# Tailwind v4 (core-basecoat.css + the vendored Basecoat component sources):
#   the second part of web/site/core.css, Basecoat's components for the
#   shared functional pages, nested under .gp-core.
#
# TAILWIND_CACHE overrides the cache directory; TAILWIND_BIN and TAILWIND4_BIN
# use existing, already verified binaries of the pinned versions.
set -euo pipefail

VERSION3=v3.4.17
VERSION4=v4.3.5
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)

case "$(uname -s)-$(uname -m)" in
  Linux-x86_64)
    asset=tailwindcss-linux-x64
    sum3=7d24f7fa191d2193b78cd5f5a42a6093e14409521908529f42d80b11fde1f1d4
    sum4=e47695cee3f0d90623a91f3939b08bdee610eb5ad99340adff9d4145b284d6cc ;;
  Linux-aarch64|Linux-arm64)
    asset=tailwindcss-linux-arm64
    sum3=69b1378b8133192d7d2feb12a116fa12d035594f58db3eff215879e4ad8cf39b
    sum4=ffcd0d21dbd7c605dc12743ff0c0b47debc963c86b399674084c1c8f2c96ecad ;;
  Darwin-x86_64)
    asset=tailwindcss-macos-x64
    sum3=6cbdad74be776c087ffa5e9a057512c54898f9fe8828d3362212dfe32fc933a3
    sum4=a63702686a3ed048b3135d84a73be49ea9d47f880c9b20e54b3ef60934ad8cdf ;;
  Darwin-arm64)
    asset=tailwindcss-macos-arm64
    sum3=a1d0c7985759accca0bf12e51ac1dcbf0f6cf2fffb62e6e0f62d091c477a10a3
    sum4=f3f4f5d1727e2b693b8f1c9a1492b5dd87008c86ea286bee1589cb458e6235fc ;;
  *) echo "tailwind: no pinned standalone CLI for $(uname -s)-$(uname -m)" >&2; exit 1 ;;
esac

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
  else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

# fetch VERSION SHA256 prints the path of the verified CLI for that release,
# downloading it into the cache first when it is missing or does not verify.
fetch() {
  local version=$1 sum=$2 cache bin tmp got
  cache=${TAILWIND_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/gorun-tailwind}/$version
  bin="$cache/$asset"
  if [ ! -x "$bin" ] || [ "$(sha256 "$bin")" != "$sum" ]; then
    mkdir -p "$cache"
    tmp="$bin.download"
    echo "tailwind: downloading $asset $version" >&2
    curl -fsSL --retry 3 -o "$tmp" "https://github.com/tailwindlabs/tailwindcss/releases/download/$version/$asset"
    got=$(sha256 "$tmp")
    if [ "$got" != "$sum" ]; then
      rm -f "$tmp"
      echo "tailwind: checksum mismatch for $asset $version: got $got, want $sum" >&2
      exit 1
    fi
    chmod +x "$tmp"
    mv "$tmp" "$bin"
  fi
  printf '%s\n' "$bin"
}

bin3=${TAILWIND_BIN:-}
[ -n "$bin3" ] || bin3=$(fetch "$VERSION3" "$sum3")
bin4=${TAILWIND4_BIN:-}
[ -n "$bin4" ] || bin4=$(fetch "$VERSION4" "$sum4")

# go_classes writes admin-go-classes.txt, the values of class="..." attributes
# in Go code that renders admin markup, one class per line (template actions
# removed). admin.css lists it as a @source: Tailwind v4 has no per-language
# extractor, and scanning the .go files whole would turn ordinary words in
# code and comments into CSS. Generated on every build, not committed.
go_classes() {
  local files
  files=$(cd "$root" && ls internal/admin/*.go internal/httpx/*.go internal/content/admin_content.go internal/analytics/chart.go | grep -v '_test\.go$')
  # shellcheck disable=SC2086
  # Only complete literals count: an attribute assembled from Go expressions
  # (class="` + cls + `") has a backtick inside the quotes and is skipped.
  (cd "$root" && grep -ohE 'class=\\?"[^"\\`]*\\?"' $files || true) \
    | sed -E 's/^class=\\?"//; s/\\?"$//; s/\{\{[^}]*\}\}/ /g' \
    | tr -s ' \t' '\n' | awk 'NF' | LC_ALL=C sort -u > "$here/admin-go-classes.txt"
}

# CORE_BASECOAT_COMPONENTS are the Basecoat components the shared functional
# pages use (account, cart, checkout, shop, product, intake, form result):
# a file under vendor/basecoat-1.3.0/components and a section of the same
# name in vendor/basecoat-1.3.0/styles/vega.css.
CORE_BASECOAT_COMPONENTS="button label input textarea native-select checkbox radio field card alert badge table empty"

# core_basecoat writes core-basecoat.gen.css: the vendored sources of those
# components nested under `.gp-core { }` with their `@layer components`
# wrappers removed (see core-basecoat.css for why). Generated on every build,
# not committed.
core_basecoat() {
  local vendor="$here/vendor/basecoat-1.3.0" out="$here/core-basecoat.gen.css" name
  {
    echo "/* Generated by tools/tailwind/build.sh from vendor/basecoat-1.3.0; do not edit. */"
    echo ".gp-core {"
    for name in $CORE_BASECOAT_COMPONENTS; do
      # Structure: the whole file minus its "@layer components {" line and
      # its closing brace (the last line).
      awk 'NR==1 && /^\/\*/ {print; next} /^@layer components \{$/ {next} {lines[++n]=$0} END {for (i=1;i<n;i++) print lines[i]; if (lines[n] !~ /^\}$/) print lines[n]}' "$vendor/components/$name.css"
    done
    for name in $CORE_BASECOAT_COMPONENTS; do
      # Look: the section of styles/vega.css that opens with "  /* Name */"
      # (names are capitalised words: button -> Button, native-select ->
      # Native Select) up to the next section marker or the layer's end.
      awk -v want="$(printf '%s' "$name" | awk -F- -v OFS=' ' '{for (i = 1; i <= NF; i++) $i = toupper(substr($i, 1, 1)) substr($i, 2); print}')" '
        /^  \/\* .* \*\/$/ { inside = ($0 == "  /* " want " */") }
        /^\}$/ { inside = 0 }
        inside { print }' "$vendor/styles/vega.css"
    done
    echo "}"
  } > "$out"
}

build() { # bin output cli-args...
  local bin=$1 out=$2 target=$2
  shift 2
  if [ "$check" = 1 ]; then
    target=$(mktemp "${TMPDIR:-/tmp}/tailwind.XXXXXX.css")
  fi
  (cd "$here" && "$bin" "$@" -o "$target" 2>/dev/null) || {
    (cd "$here" && "$bin" "$@" -o "$target")
    exit 1
  }
  if [ "$check" = 1 ]; then
    if ! cmp -s "$target" "$out"; then
      rm -f "$target"
      echo "tailwind: ${out#"$root"/} is out of date." >&2
      echo "Run tools/tailwind/build.sh (or make css) and commit the result." >&2
      exit 1
    fi
    rm -f "$target"
    echo "tailwind: ${out#"$root"/} is up to date."
  elif [ "${quiet:-0}" != 1 ]; then
    echo "tailwind: wrote ${out#"$root"/} ($(wc -c <"$out" | tr -d ' ') bytes)"
  fi
}

check=0
if [ "${1:-}" = "--check" ]; then check=1; fi
build "$bin3" "$root/themes/default/assets/tailwind.css" -c tailwind.config.js -i input.css --minify
# core.css is two builds in one committed file: the v3 utilities and element
# defaults, then the v4 Basecoat components (core-basecoat.css), appended.
core_basecoat
core_part1=$(mktemp "${TMPDIR:-/tmp}/core-v3.XXXXXX.css")
core_part2=$(mktemp "${TMPDIR:-/tmp}/core-v4.XXXXXX.css")
check_saved=$check; check=0; quiet=1
build "$bin3" "$core_part1" -c core.config.js -i core.css --minify
# Not minified, like the admin build: Lightning CSS flattens the nested rules
# when minifying, which makes the file larger, not smaller.
build "$bin4" "$core_part2" -i core-basecoat.css
check=$check_saved; quiet=0
core_target="$root/web/site/core.css"
if [ "$check" = 1 ]; then core_target=$(mktemp "${TMPDIR:-/tmp}/core.XXXXXX.css"); fi
{ cat "$core_part1"; echo; cat "$core_part2"; } > "$core_target"
rm -f "$core_part1" "$core_part2"
if [ "$check" = 1 ]; then
  if ! cmp -s "$core_target" "$root/web/site/core.css"; then
    rm -f "$core_target"
    echo "tailwind: web/site/core.css is out of date." >&2
    echo "Run tools/tailwind/build.sh (or make css) and commit the result." >&2
    exit 1
  fi
  rm -f "$core_target"
  echo "tailwind: web/site/core.css is up to date."
else
  echo "tailwind: wrote web/site/core.css ($(wc -c <"$core_target" | tr -d ' ') bytes)"
fi
go_classes
# Not minified: with v4.3.5, --minify drops one Basecoat rule (the sidebar
# label truncation, [&>span:last-child]:truncate) in roughly one build out of
# a hundred, which would make --check flap in CI; 200 unminified builds were
# byte-identical. The server compresses responses, so the difference on the
# wire is small.
build "$bin4" "$root/web/admin/static/admin-tailwind.css" -i admin.css
