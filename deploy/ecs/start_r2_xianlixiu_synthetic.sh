#!/usr/bin/env bash
# Original Express app code with an empty synthetic SQLite and disabled credential login.
# The original local database, sessions, ECS reviewed probe and default Docker remain untouched.
set -euo pipefail
umask 077
export DOCKER_HOST=unix:///run/ewp-builder-20261007/docker.sock
root=/data/ewp-builder-20261007
archive="$root/state/ewp-r2-xianlixiu-synthetic-v2-20261008.tar.gz"
app="$root/state/r2-xianlixiu-synthetic-app"
data="$root/state/r2-xianlixiu-synthetic-data"
container=ewp-r2-xianlixiu-synthetic-20261008
unit=ewp-r2-xianlixiu-relay-20261008.service
network=ewp-r2-next15-loop-20261007
socket=/run/ewp-r2-20261007/xianlixiu.sock
image=sha256:e894118949319b479bfc3e16e545cb2f85b7a2194aa86ea9cd4e29538f2b13c3
test "$(sha256sum "$archive" | cut -d' ' -f1)" = afaf12b90deb1c89fe9ee7cbeea6ba15462bebefa3e4df32aae5797088d1eb25
test "$(sha256sum "$root/bin/ewp-r2-runtime-relay-xl-linux-amd64" | cut -d' ' -f1)" = 4ee5db3af9a154e61a65e0624388a039b2a92017520dd214083734e665279a6d
test "$(systemctl show ewp-builder-dockerd-20261007.service -p PrivateNetwork --value)" = yes
test "$(systemctl is-active ewp-r2-next-runtime-20261008.service)" = active
test "$(docker network inspect "$network" --format '{{.Internal}}')" = true
test "$(docker network inspect "$network" --format '{{range .IPAM.Config}}{{.Subnet}}{{end}}')" = 172.29.77.0/24
test -z "$(docker ps -aq --filter "name=^/${container}$")"
test ! -e "$socket"
if [ -e "$app" ]; then
  test -d "$app" && test ! -L "$app" && test "$(stat -c '%u' "$app")" = 0
  test "$(sha256sum "$app/app/server/index.mjs" | cut -d' ' -f1)" = 2f4513e140da1460c7d880f9d4512490d810f8416dd96d07c56e09424cb68e37
fi
if [ -e "$data" ]; then
  test -d "$data" && test ! -L "$data" && test "$(stat -c '%a:%u' "$data")" = 700:1000
fi
test "$(stat -c '%a:%u' /run/ewp-r2-20261007)" = 700:0
pid=$(systemctl show ewp-builder-dockerd-20261007.service -p MainPID --value)
test "$pid" -gt 1 && test "$(stat -Lc %i /proc/1/ns/net)" != "$(stat -Lc %i "/proc/$pid/ns/net")"
bridge=$(< /sys/class/net/docker0/ifindex)
old_db=$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' ewp-postgres-preview-20261006)
old_site=$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)
started=false
relay_started=false
accepted=false
cleanup() {
  if [ "$accepted" = true ]; then return; fi
  if [ "$relay_started" = true ]; then systemctl stop "$unit" || true; fi
  if [ "$started" = true ]; then docker rm -f "$container" >/dev/null 2>&1 || true; fi
}
trap cleanup EXIT
if [ ! -d "$app" ]; then
  mkdir -m 0700 "$app"
  tar -xzf "$archive" -C "$app" --no-same-owner
fi
test "$(sha256sum "$app/app/server/index.mjs" | cut -d' ' -f1)" = 2f4513e140da1460c7d880f9d4512490d810f8416dd96d07c56e09424cb68e37
for directory in "$app" "$app/app" "$app/app/server" "$app/app/scripts" "$app/dist"; do
  test -d "$directory" && test ! -L "$directory"
  chmod 0755 "$directory"
done
if [ ! -d "$data" ]; then
  mkdir -m 0700 "$data"
  chown 1000:1000 "$data"
fi
started=true
docker run -d --pull never --name "$container" --log-driver local --log-opt max-size=1m --log-opt max-file=2 --log-opt compress=false \
  --runtime runsc --network "$network" --ip 172.29.77.4 --read-only --cap-drop ALL --security-opt no-new-privileges \
  --user 1000:1000 --pids-limit 64 --memory 1g --memory-swap 1g --cpus 1 \
  --tmpfs /tmp:rw,nosuid,nodev,noexec,size=32m \
  --mount "type=bind,src=$app,dst=/app,readonly" --mount "type=bind,src=$data,dst=/state" --workdir /app \
  --env HOST=0.0.0.0 --env PORT=3067 --env DATABASE_PATH=/state/synthetic.sqlite \
  --env SESSION_ROOT=/state/sessions --env EWP_SYNTHETIC_ONLY=1 --env TRUST_PROXY=1 --env NODE_ENV=production \
  --entrypoint node "$image" app/server/index.mjs >/dev/null
ready=false
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
  if nsenter -t "$pid" -n curl --noproxy '*' --silent --fail --max-time 2 http://172.29.77.4:3067/api/health >/dev/null 2>&1; then ready=true; break; fi
  sleep 0.3
done
if [ "$ready" != true ]; then docker logs "$container" >&2 || true; exit 1; fi
systemd-run --unit=ewp-r2-xianlixiu-relay-20261008 --property=Restart=on-failure --property=RestartSec=2 \
  /usr/bin/nsenter -t "$pid" -n "$root/bin/ewp-r2-runtime-relay-xl-linux-amd64" \
  --socket "$socket" --upstream 172.29.77.4:3067 >/dev/null
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
printf 'r2_xianlixiu_synthetic=healthy login=disabled data=synthetic_only legacy=unchanged\n'
