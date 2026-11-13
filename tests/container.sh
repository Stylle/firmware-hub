#!/bin/sh
# Run against a disposable Docker engine test scope; never use production data.
set -eu
if [ "$(id -u)" != 0 ]; then
 echo 'Run this disposable ownership test as root (sudo sh tests/container.sh IMAGE).'
 exit 2
fi
image=${1:-firmware-hub:local}
work=$(mktemp -d /tmp/firmware-hub-container-XXXXXX)
cleanup() {
  docker rm -f firmware-hub-bind-check firmware-hub-link-check firmware-hub-nested-check >/dev/null 2>&1 || true
  resolved=$(realpath "$work")
  if [ "$(dirname "$resolved")" = /tmp ]; then
    case "$(basename "$resolved")" in firmware-hub-container-*) rm -rf -- "$resolved" ;; esac
  fi
}
trap cleanup EXIT
mkdir -m 700 "$work/data" "$work/outside" "$work/nested-data"
touch "$work/outside/unchanged"
before=$(stat -c '%u:%g:%a' "$work/outside/unchanged")
docker run -d --name firmware-hub-bind-check -e USER=admin -e PASSWORD=Temporary-Test-Only-Password --mount "type=bind,src=$work/data,dst=/data" "$image" >/dev/null
healthy() {
 for n in $(seq 1 30); do
  if docker exec firmware-hub-bind-check wget -q -O /dev/null http://127.0.0.1:8080/healthz; then return 0; fi
  sleep 1
 done
 docker logs firmware-hub-bind-check
 return 1
}
healthy
test "$(docker exec firmware-hub-bind-check awk '/^Uid:/ {print $2}' /proc/1/status)" = 10001
test "$(stat -c '%u:%g:%a' "$work/data")" = '10001:10001:700'
test "$(stat -c '%a' "$work/data/master.key")" = 600
key=$(sha256sum "$work/data/master.key")
docker restart firmware-hub-bind-check >/dev/null
healthy
test "$(sha256sum "$work/data/master.key")" = "$key"
docker rm -f firmware-hub-bind-check >/dev/null
ln -s /outside "$work/data/link"
if docker run --name firmware-hub-link-check -e USER=admin -e PASSWORD=Temporary-Test-Only-Password --mount "type=bind,src=$work/data,dst=/data" "$image" >/dev/null 2>&1; then
 echo 'FAIL: symlink accepted'; exit 1
fi
if docker run --name firmware-hub-nested-check -e USER=admin -e PASSWORD=Temporary-Test-Only-Password --mount "type=bind,src=$work/nested-data,dst=/data" --mount "type=bind,src=$work/outside,dst=/data/nested" "$image" >/dev/null 2>&1; then
 echo 'FAIL: nested bind mount accepted'; exit 1
fi
test "$(stat -c '%u:%g:%a' "$work/outside/unchanged")" = "$before"
echo 'PASS root-owned NAS bind initializes, non-root service, restart persistence, symlink/nested-mount rejection, outside data unchanged'
