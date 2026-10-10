#!/usr/bin/env bash
# 第二合成动态版本与 v2 并行；仅使用既有 Builder 私有 internal bridge。
set -euo pipefail
umask 077
export DOCKER_HOST=unix:///run/ewp-builder-20261007/docker.sock
root=/data/ewp-builder-20261007
archive="$root/state/ewp-r2-next15-standalone-v3-20261008.tar.gz"
app="$root/state/r2-next15-standalone-v3"
name=ewp-r2-next15-v3-20261008
network=ewp-r2-next15-loop-20261007
unit=ewp-r2-runtime-relay-v3-20261008.service
socket=/run/ewp-r2-20261007/v3.sock
image=sha256:e894118949319b479bfc3e16e545cb2f85b7a2194aa86ea9cd4e29538f2b13c3
test "$(sha256sum "$archive" | cut -d' ' -f1)" = 8668dfa8e229e7305b286b85122513651107631447125d78ddf9b307b80c6e78
test "$(sha256sum "$root/bin/ewp-r2-runtime-relay-v3-linux-amd64" | cut -d' ' -f1)" = 3773b4bc36d66647acd1ca94abbf182121f22e46884da986880dad112135e4c3
test "$(systemctl show ewp-builder-dockerd-20261007.service -p PrivateNetwork --value)" = yes
test "$(systemctl is-active ewp-r2-runtime-relay-20261007.service)" = active
pid=$(systemctl show ewp-builder-dockerd-20261007.service -p MainPID --value)
test "$pid" -gt 1 && test "$(stat -Lc %i /proc/1/ns/net)" != "$(stat -Lc %i "/proc/$pid/ns/net")"
test "$(docker network inspect "$network" --format '{{.Internal}}')" = true
test "$(docker inspect ewp-r2-next15-loop-20261007 --format '{{.State.Running}}')" = true
test -z "$(docker ps -aq --filter "name=^/${name}$")"
test ! -e "$app" && test ! -e "$socket"
test "$(stat -c '%a:%u' /run/ewp-r2-20261007)" = 700:0
bridge=$(< /sys/class/net/docker0/ifindex)
old_db=$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' ewp-postgres-preview-20261006)
old_site=$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)
created=false
relay_started=false
accepted=false
cleanup() {
  if [ "$accepted" = true ]; then return; fi
  if [ "$relay_started" = true ]; then systemctl stop "$unit" || true; fi
  if [ "$created" = true ]; then docker rm -f "$name" >/dev/null 2>&1 || true; fi
}
trap cleanup EXIT
mkdir -m 0700 "$app"
tar -xzf "$archive" -C "$app" --no-same-owner
test -f "$app/server.js" && test -f "$app/.next/BUILD_ID"
created=true
docker run -d --pull never --name "$name" --log-driver local --log-opt max-size=1m --log-opt max-file=2 --log-opt compress=false \
  --runtime runsc --network "$network" --ip 172.29.77.3 --read-only --cap-drop ALL --security-opt no-new-privileges \
  --user 1000:1000 --pids-limit 64 --memory 768m --memory-swap 768m --cpus 1 \
  --tmpfs /tmp:rw,nosuid,nodev,noexec,size=32m \
  --mount "type=bind,src=$app,dst=/app,readonly" --workdir /app \
  --env NODE_ENV=production --env PORT=3047 --env HOSTNAME=0.0.0.0 --env EWP_SYNTHETIC_VARIANT=v3 \
  --entrypoint node "$image" server.js >/dev/null
ready=false
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
  if nsenter -t "$pid" -n curl --noproxy '*' --silent --fail --max-time 2 http://172.29.77.3:3047/api/health >/dev/null 2>&1; then ready=true; break; fi
  sleep 0.3
done
test "$ready" = true
systemd-run --unit=ewp-r2-runtime-relay-v3-20261008 --property=Restart=on-failure --property=RestartSec=2 \
  /usr/bin/nsenter -t "$pid" -n "$root/bin/ewp-r2-runtime-relay-v3-linux-amd64" \
  --socket "$socket" --upstream 172.29.77.3:3047 >/dev/null
relay_started=true
relay_ready=false
for attempt in 1 2 3 4 5 6 7 8 9 10; do
  if curl --unix-socket "$socket" --silent --fail --max-time 2 http://runtime.internal/api/health >/dev/null 2>&1; then relay_ready=true; break; fi
  sleep 0.2
done
test "$relay_ready" = true
test "$(< /sys/class/net/docker0/ifindex)" = "$bridge"
test "$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' ewp-postgres-preview-20261006)" = "$old_db"
test "$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)" = "$old_site"
accepted=true
printf 'r2_v3_synthetic=ready old_dynamic=preserved relay_socket=ready\n'
