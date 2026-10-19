#!/bin/sh
# Install a verified release for the current user. No Go toolchain or sudo.
set -eu

fail() { printf 'GoRun: %s\n' "$*" >&2; exit 1; }
start=0
case "${1:-}" in
  '') ;;
  --start) start=1; shift ;;
  --help) printf 'Usage: sh install.sh [--start]\nOptional: GORUN_VERSION=0.3.1 GORUN_INSTALL_DIR=/path/to/bin\n'; exit 0 ;;
  *) fail 'Expected --start or --help.' ;;
esac
[ "$#" -eq 0 ] || fail 'Unexpected arguments.'
[ -n "${HOME:-}" ] || fail 'HOME must be set.'
for tool in curl tar uname mktemp awk grep; do
  command -v "$tool" >/dev/null 2>&1 || fail "Required tool is missing: $tool"
done
if command -v sha256sum >/dev/null 2>&1; then
  checksum=sha256sum
elif command -v shasum >/dev/null 2>&1; then
  checksum=shasum
else
  fail 'A SHA-256 verifier (sha256sum or shasum) is required.'
fi
case "$(uname -s)" in
  Darwin) platform=darwin ;;
  Linux) platform=linux ;;
  *) fail 'Supported systems: macOS and Linux. See the installation documentation.' ;;
esac
case "$(uname -m)" in
  arm64|aarch64) arch=arm64 ;;
  x86_64|amd64) arch=amd64 ;;
  *) fail 'Supported architectures: x86_64 and arm64.' ;;
esac
repo=https://github.com/zadstack/gorun
version=${GORUN_VERSION:-}
if [ -z "$version" ]; then
  release=$(curl --proto '=https' --tlsv1.2 -fsSL -o /dev/null -w '%{url_effective}' "$repo/releases/latest") || fail 'Could not find the latest release.'
  case "$release" in
    "$repo"/releases/tag/v*) version=${release##*/v} ;;
    *) fail 'Unexpected release redirect.' ;;
  esac
fi
version=${version#v}
case "$version" in ''|*[!0-9A-Za-z.+-]*) fail 'Invalid release version.' ;; esac
case "$version" in [0-9]*) ;; *) fail 'Release version must start with a digit.' ;; esac
archive=gorun-$version-$platform-$arch.tar.gz
work=$(mktemp -d "${TMPDIR:-/tmp}/gorun-install.XXXXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM
base=$repo/releases/download/v$version
printf 'Downloading GoRun %s for %s/%s…\n' "$version" "$platform" "$arch"
curl --proto '=https' --tlsv1.2 -fsSL "$base/$archive" -o "$work/$archive" || fail 'Release download failed; no installation was changed.'
curl --proto '=https' --tlsv1.2 -fsSL "$base/SHA256SUMS-$version.txt" -o "$work/checksums" || fail 'Checksum download failed.'
expected=$(awk -v name="$archive" '$2 == name { print $1; n++ } END { if (n != 1) exit 1 }' "$work/checksums") || fail 'Archive checksum is missing or duplicated.'
[ "${#expected}" -eq 64 ] || fail 'Invalid SHA-256 checksum.'
case "$expected" in *[!0-9a-fA-F]*) fail 'Invalid SHA-256 checksum.' ;; esac
if [ "$checksum" = sha256sum ]; then
  actual=$(sha256sum "$work/$archive")
else
  actual=$(shasum -a 256 "$work/$archive")
fi
actual=${actual%% *}
[ "$actual" = "$expected" ] || fail 'Checksum mismatch; no installation was changed.'
# Extract only the regular executable, never archive paths, links or themes.
member=gorun-$version-$platform-$arch/gorun
tar -xOf "$work/$archive" "$member" > "$work/gorun" || fail 'Release executable is missing.'
chmod 755 "$work/gorun"
"$work/gorun" help > "$work/help" || fail 'The release executable cannot run on this system.'
awk '$1 == "start" { found=1 } END { exit !found }' "$work/help" || fail 'This release predates gorun start. Install a newer release when available; your existing installation was preserved.'
bin=${GORUN_INSTALL_DIR:-$HOME/.local/bin}
mkdir -p "$bin"
# Stage on the destination filesystem so upgrades replace the binary atomically.
staged=$(mktemp "$bin/.gorun.XXXXXXXX")
if ! cp "$work/gorun" "$staged" || ! chmod 755 "$staged" || ! mv -f "$staged" "$bin/gorun"; then
  rm -f "$staged"
  fail 'Could not install GoRun; check destination permissions.'
fi
# Make the default executable discoverable in future interactive terminals.
# The README also exports PATH in the calling terminal, which a child cannot do.
if [ "$bin" = "$HOME/.local/bin" ]; then
  profile=
  login_profile=
  line='export PATH="$HOME/.local/bin:$PATH"'
  case "${SHELL:-sh}" in
    */*) shell_name=${SHELL##*/} ;;
    *) shell_name=${SHELL:-sh} ;;
  esac
  case "$shell_name" in
    zsh) profile=${ZDOTDIR:-$HOME}/.zshrc ;;
    bash)
      profile=$HOME/.bashrc
      if [ -f "$HOME/.bash_profile" ]; then login_profile=$HOME/.bash_profile
      elif [ -f "$HOME/.bash_login" ]; then login_profile=$HOME/.bash_login
      else login_profile=$HOME/.profile
      fi
      ;;
    fish)
      profile=${XDG_CONFIG_HOME:-$HOME/.config}/fish/conf.d/gorun.fish
      line='contains -- "$HOME/.local/bin" $PATH; or set -gx PATH "$HOME/.local/bin" $PATH'
      ;;
    sh|'') profile=$HOME/.profile ;;
  esac
  for profile in "$profile" "$login_profile"; do
    [ -n "$profile" ] || continue
    mkdir -p "$(dirname "$profile")"
    if ! grep -Fq "$line" "$profile" 2>/dev/null; then
      printf '\n# GoRun user-local executable\n%s\n' "$line" >> "$profile" || fail 'Installed binary, but could not configure the shell PATH.'
    fi
  done
fi
printf 'Installed: %s/gorun\n' "$bin"
if [ "$start" -eq 1 ]; then
  # Use the installed path directly: the calling shell need not know PATH yet.
  # A stable default data path preserves the site across working directories.
  "$bin/gorun" start --data "${GORUN_DATA:-$HOME/.local/share/gorun/site}"
else
  printf 'Start with: "%s/gorun" start\n' "$bin"
fi
