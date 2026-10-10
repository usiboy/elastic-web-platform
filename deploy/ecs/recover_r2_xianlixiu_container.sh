#!/usr/bin/env bash
# Reuse only the verified synthetic original application and its private SQLite.
set -euo pipefail
umask 077
export DOCKER_HOST=unix:///run/ewp-builder-20261007/docker.sock
root=/data/ewp-builder-20261007
archive="$root/state/ewp-r2-xianlixiu-synthetic-v2-20261008.tar.gz"
app="$root/state/r2-xianlixiu-synthetic-app"
data="$root/state/r2-xianlixiu-synthetic-data"
name=ewp-r2-xianlixiu-synthetic-20261008
network=ewp-r2-next15-loop-20261007
image=sha256:e894118949319b479bfc3e16e545cb2f85b7a2194aa86ea9cd4e29538f2b13c3
test "$(sha256sum "$archive" | cut -d' ' -f1)" = afaf12b90deb1c89fe9ee7cbeea6ba15462bebefa3e4df32aae5797088d1eb25
test "$(sha256sum "$app/app/server/index.mjs" | cut -d' ' -f1)" = 2f4513e140da1460c7d880f9d4512490d810f8416dd96d07c56e09424cb68e37
test "$(stat -c '%a:%u' "$data")" = 700:1000
test -f "$data/synthetic.sqlite" && test ! -L "$data/synthetic.sqlite"
test "$(systemctl show ewp-builder-dockerd-20261007.service -p PrivateNetwork --value)" = yes
pid=$(systemctl show ewp-builder-dockerd-20261007.service -p MainPID --value)
test "$pid" -gt 1 && test "$(stat -Lc %i /proc/1/ns/net)" != "$(stat -Lc %i "/proc/$pid/ns/net")"
test "$(docker network inspect "$network" --format '{{.Internal}}')" = true
test "$(docker network inspect "$network" --format '{{range .IPAM.Config}}{{.Subnet}}{{end}}')" = 172.29.77.0/24
test "$(docker image inspect "$image" --format '{{.Id}}')" = "$image"
old_bridge=$(< /sys/class/net/docker0/ifindex)
old_db=$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' ewp-postgres-preview-20261006)
old_site=$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)
container=$(docker ps -aq --filter "name=^/${name}$")
if [ -n "$container" ]; then
  test "$(docker inspect "$name" --format '{{.Config.Image}}')" = "$image"
  test "$(docker inspect "$name" --format '{{.HostConfig.NetworkMode}}')" = "$network"
  test "$(docker inspect "$name" --format '{{.HostConfig.Runtime}}')" = runsc
  docker inspect "$name" --format '{{range .Config.Env}}{{println .}}{{end}}' | python3 -c 'import sys; assert "EWP_SYNTHETIC_ONLY=1" in [x.strip() for x in sys.stdin]'
  if [ "$(docker inspect "$name" --format '{{.State.Running}}')" != true ]; then
    test ! -e "$data/synthetic.sqlite.writer.lock" # 崩溃残锁必须人工核查，不自动抢占写者。
    docker start "$name" >/dev/null
  fi
else
  test ! -e "$data/synthetic.sqlite.writer.lock"
  docker run -d --pull never --name "$name" --log-driver local --log-opt max-size=1m --log-opt max-file=2 --log-opt compress=false \
    --runtime runsc --network "$network" --ip 172.29.77.4 --read-only --cap-drop ALL --security-opt no-new-privileges \
    --user 1000:1000 --pids-limit 64 --memory 1g --memory-swap 1g --cpus 1 \
    --tmpfs /tmp:rw,nosuid,nodev,noexec,size=32m \
    --mount "type=bind,src=$app,dst=/app,readonly" --mount "type=bind,src=$data,dst=/state" --workdir /app \
    --env HOST=0.0.0.0 --env PORT=3067 --env DATABASE_PATH=/state/synthetic.sqlite \
    --env SESSION_ROOT=/state/sessions --env EWP_SYNTHETIC_ONLY=1 --env TRUST_PROXY=1 --env NODE_ENV=production \
    --entrypoint node "$image" app/server/index.mjs >/dev/null
fi
test "$(docker inspect "$name" --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}')" = 172.29.77.4
ready=false
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
  if nsenter -t "$pid" -n curl --noproxy '*' --silent --fail --max-time 2 http://172.29.77.4:3067/api/health >/dev/null 2>&1; then ready=true; break; fi
  sleep 0.3
done
test "$ready" = true
test "$(< /sys/class/net/docker0/ifindex)" = "$old_bridge"
test "$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' ewp-postgres-preview-20261006)" = "$old_db"
test "$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)" = "$old_site"
printf 'r2_xianlixiu_synthetic_recovered=healthy database=existing legacy=unchanged\n'
