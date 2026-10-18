#!/usr/bin/env bash
# Checks that every non-merge commit in BASE..HEAD carries a DCO sign-off
# (`Signed-off-by: Name <email>`, as added by `git commit -s`) whose email
# matches the commit author. Commits authored by GitHub bot accounts
# (dependabot[bot], github-actions[bot], …) are skipped.
#
# Usage: .github/scripts/dco-check.sh BASE HEAD
set -euo pipefail

base=${1:?usage: dco-check.sh BASE HEAD}
head=${2:?usage: dco-check.sh BASE HEAD}

failed=0
checked=0
shas=$(git rev-list --no-merges "$base..$head")
for sha in $shas; do
  email=$(git log -1 --format='%ae' "$sha")
  subject=$(git log -1 --format='%s' "$sha")
  case "$email" in
    *'[bot]@users.noreply.github.com') echo "skip  ${sha:0:12} bot author $email"; continue ;;
  esac
  checked=$((checked + 1))
  want=$(printf '%s' "$email" | tr '[:upper:]' '[:lower:]')
  ok=0
  while IFS= read -r line; do
    signer=$(printf '%s' "$line" | sed -n 's/^Signed-off-by: .* <\(.*\)>$/\1/p' | tr '[:upper:]' '[:lower:]')
    if [ -n "$signer" ] && [ "$signer" = "$want" ]; then ok=1; fi
  done < <(git log -1 --format='%(trailers:key=Signed-off-by)' "$sha")
  if [ "$ok" = 1 ]; then
    echo "ok    ${sha:0:12} $subject"
  else
    echo "FAIL  ${sha:0:12} $subject (no 'Signed-off-by: … <$email>')"
    failed=$((failed + 1))
  fi
done

if [ "$failed" -gt 0 ]; then
  cat <<'MSG'

Some commits are missing a DCO sign-off matching their author email.
Sign off with `git commit -s`; for existing commits on your branch:
  git rebase --signoff <base-branch> && git push --force-with-lease
See CONTRIBUTING.md (Developer Certificate of Origin).
MSG
  exit 1
fi
echo "DCO: $checked commit(s) signed off."
