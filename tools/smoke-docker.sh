#!/bin/sh
# Runs the documented Docker paths against a local image, so a change that
# breaks a paste-ready command fails before anything is published.
#   tools/smoke-docker.sh <tag of ghcr.io/zadstack/gorun>
set -eu

tag=${1:?usage: tools/smoke-docker.sh <image tag>}
image=ghcr.io/zadstack/gorun:$tag
repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/gorun-smoke.XXXXXXXX")
run=gorun-smoke-$$
trial=$run-trial
real=$run-real
port=${GORUN_SMOKE_PORT:-18180}
export GORUN_VERSION=$tag
# Coolify generates these; the values below have the same shape.
export SERVICE_URL_GORUN_8080=https://smoke.example.com
SERVICE_REALBASE64_32_GORUNKEY=$(openssl rand -base64 32)
SERVICE_HEX_32_GORUNSETUP=$(openssl rand -hex 16)
export SERVICE_REALBASE64_32_GORUNKEY SERVICE_HEX_32_GORUNSETUP

cleanup() {
  docker rm -fv "$trial" "$real" >/dev/null 2>&1 || true
  docker volume rm "$run" >/dev/null 2>&1 || true
  (cd "$work" && docker compose -p "$run-server" -f compose.server.yml down -v >/dev/null 2>&1) || true
  docker compose -p "$run-coolify" -f "$repo/deploy/coolify-compose.yml" down -v >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT HUP INT TERM

fail() {
  printf 'smoke: %s\n' "$1" >&2
  exit 1
}

# Container output holds generated credentials; never print it.
logs() { docker logs "$1" 2>&1; }

wait_ok() {
  i=0
  until [ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port$1" || true)" = 200 ]; do
    i=$((i + 1))
    [ "$i" -lt 40 ] || fail "$1 did not answer 200 on port $port"
    sleep 1
  done
}

echo 'smoke: a bare run explains the trial'
if out=$(docker run --rm "$image" 2>&1); then fail 'a bare run must stop'; fi
case "$out" in *GORUN_TRIAL=1*) ;; *) fail 'a bare run must mention GORUN_TRIAL=1' ;; esac

echo 'smoke: docker trial'
docker run -d --name "$trial" -p "$port:8080" -v "$run:/data" -e GORUN_TRIAL=1 "$image" >/dev/null
wait_ok /admin/login
logs "$trial" | grep -q 'Password (shown once' || fail 'the trial printed no password'
logs "$trial" | grep -q 'ship-with directory missing' && fail 'the image lost its bundled patterns'

echo 'smoke: trial to production on the same volume'
key=$(docker exec "$trial" /app/gorun secret-key)
docker rm -f "$trial" >/dev/null
docker run -d --name "$real" -p "$port:8080" -v "$run:/data" \
  -e GORUN_BASE_URL=https://smoke.example.com -e GORUN_SECRET_KEY="$key" "$image" >/dev/null
wait_ok /admin/login
logs "$real" | grep -q 'First-run setup token' && fail 'an existing site must not print a setup token'
docker rm -f "$real" >/dev/null

echo 'smoke: compose.server.yml with a first-run token'
sed "s/\"8080:8080\"/\"$port:8080\"/" "$repo/deploy/compose.server.yml" > "$work/compose.server.yml"
sed -i.bak 's/container_name: gorun/container_name: '"$run"'-server/' "$work/compose.server.yml"
printf 'GORUN_BASE_URL=http://192.0.2.10:8080\nGORUN_SECRET_KEY=%s\n' "$(openssl rand -base64 32)" > "$work/.env"
(cd "$work" && docker compose -p "$run-server" -f compose.server.yml up -d >/dev/null 2>&1)
wait_ok /admin/setup
logs "$run-server" | grep -q 'First-run setup token' || fail 'no first-run setup token in the logs'
curl -s "http://127.0.0.1:$port/admin/setup" | grep -q 'name="setup_token"' || fail 'setup must ask for the token'
(cd "$work" && docker compose -p "$run-server" -f compose.server.yml down -v >/dev/null 2>&1)

echo 'smoke: coolify-compose.yml with generated values'
docker compose -p "$run-coolify" -f "$repo/deploy/coolify-compose.yml" up -d >/dev/null 2>&1
container=$(docker compose -p "$run-coolify" -f "$repo/deploy/coolify-compose.yml" ps -q gorun)
i=0
until docker exec "$container" /app/gorun healthcheck >/dev/null 2>&1; do
  i=$((i + 1))
  [ "$i" -lt 40 ] || fail 'the Coolify service did not become healthy'
  sleep 1
done
[ -z "$(docker port "$container")" ] || fail 'the Coolify service must publish no port'

echo 'smoke: ok'
