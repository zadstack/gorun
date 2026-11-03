#!/bin/sh
# Runs one shard of the race suite, so CI can run the shards side by side.
# internal/admin alone takes most of the time, so its tests are dealt round
# robin across the admin shards and every other package runs in "rest".
#   tools/race-shard.sh rest
#   tools/race-shard.sh admin <shard> <shards>    e.g. admin 1 2
set -eu

admin=github.com/zadstack/gorun/internal/admin

case "${1:-}" in
rest)
	exec go test -timeout=20m -race $(go list ./... | grep -vx "$admin")
	;;
admin)
	shard=$2
	shards=$3
	# -list builds the same race binary the run below reuses from the cache.
	names=$(go test -race -list '.*' "$admin" | grep -E '^(Test|Example|Fuzz)' |
		awk -v n="$shard" -v m="$shards" '(NR - 1) % m == n - 1')
	if [ -z "$names" ]; then
		echo "race-shard: admin shard $shard of $shards selected no tests" >&2
		exit 1
	fi
	echo "race-shard: admin shard $shard of $shards runs $(echo "$names" | wc -l | tr -d ' ') tests"
	exec go test -timeout=20m -race -run "^($(echo "$names" | paste -sd'|' -))\$" "$admin"
	;;
*)
	echo "usage: tools/race-shard.sh rest | admin <shard> <shards>" >&2
	exit 2
	;;
esac
