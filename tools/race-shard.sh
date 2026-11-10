#!/bin/sh
# Runs one shard of the race suite, so CI can run the shards side by side.
#   tools/race-shard.sh rest <shard> <shards>        every package but the two below
#   tools/race-shard.sh admin <shard> <shards>       the internal/admin tests
#   tools/race-shard.sh wpmigrate <shard> <shards>   the internal/wpmigrate tests
# internal/admin and internal/wpmigrate each take longer under race than any
# other package, so their tests are dealt round robin across their own shards.
# The other packages are dealt the same way, slowest first, so no shard gets
# two of the heavy ones.
set -eu

module=github.com/zadstack/gorun
admin=$module/internal/admin
wpmigrate=$module/internal/wpmigrate
# Slowest packages under race, slowest first (seconds on a CI runner, 2026-11).
heavy="pkg/cli internal/commerce internal/theme internal/site internal/builder"

kind=${1:-}
shard=${2:-}
shards=${3:-}
case "$kind:$shard:$shards" in
rest:[1-9]*:[1-9]* | admin:[1-9]*:[1-9]* | wpmigrate:[1-9]*:[1-9]*) ;;
*)
	echo "usage: tools/race-shard.sh rest|admin|wpmigrate <shard> <shards>" >&2
	exit 2
	;;
esac

if [ "$kind" = rest ]; then
	all=$(go list ./... | grep -vx -e "$admin" -e "$wpmigrate")
	ordered=$(
		for p in $heavy; do echo "$all" | grep -x "$module/$p" || true; done
		echo "$all" | grep -vxF "$(for p in $heavy; do echo "$module/$p"; done)"
	)
	pkgs=$(echo "$ordered" | awk -v n="$shard" -v m="$shards" '(NR - 1) % m == n - 1')
	if [ -z "$pkgs" ]; then
		echo "race-shard: rest shard $shard of $shards selected no packages" >&2
		exit 1
	fi
	echo "race-shard: rest shard $shard of $shards runs $(echo "$pkgs" | wc -l | tr -d ' ') packages"
	exec go test -timeout=20m -race $pkgs
fi

if [ "$kind" = admin ]; then pkg=$admin; else pkg=$wpmigrate; fi
# -list builds the same race binary the run below reuses from the cache.
names=$(go test -race -list '.*' "$pkg" | grep -E '^(Test|Example|Fuzz)' |
	awk -v n="$shard" -v m="$shards" '(NR - 1) % m == n - 1')
if [ -z "$names" ]; then
	echo "race-shard: $kind shard $shard of $shards selected no tests" >&2
	exit 1
fi
echo "race-shard: $kind shard $shard of $shards runs $(echo "$names" | wc -l | tr -d ' ') tests"
exec go test -timeout=20m -race -run "^($(echo "$names" | paste -sd'|' -))\$" "$pkg"
