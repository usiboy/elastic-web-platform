#!/usr/bin/env bash
# Reuse the pinned, synthetic v2 container after a Builder daemon or host restart.
# Never touches the default Docker daemon, old site, or user data.
set -euo pipefail
umask 077
export DOCKER_HOST=unix:///run/ewp-builder-20261007/docker.sock
root=/data/ewp-builder-20261007
archive="$root/state/ewp-r2-next15-standalone-v2-20261007.tar.gz"
app="$root/state/r2-next15-standalone-v2"
image=sha256:e894118949319b479bfc3e16e545cb2f85b7a2194aa86ea9cd4e29538f2b13c3
name=ewp-r2-next15-loop-20261007
network=ewp-r2-next15-loop-20261007
test "$(sha256sum "$archive" | cut -d' ' -f1)" = a40af88a426cd65227352c1675425717f15393294e8685a6a62dbee8d42fe4c1
test "$(systemctl show ewp-builder-dockerd-20261007.service -p PrivateNetwork --value)" = yes
test "$(systemctl is-active ewp-builder-dockerd-20261007.service)" = active
pid=$(systemctl show ewp-builder-dockerd-20261007.service -p MainPID --value)
test "$pid" -gt 1 && test "$(stat -Lc %i /proc/1/ns/net)" != "$(stat -Lc %i "/proc/$pid/ns/net")"
test "$(docker image inspect "$image" --format '{{.Id}}')" = "$image"
old_docker_ready=false
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30; do
  if test -e /sys/class/net/docker0/ifindex &&
    timeout -k 2s 4s docker --host unix:///var/run/docker.sock inspect ewp-postgres-preview-20261006 xianlixiu-reviewed-empty-poc >/dev/null 2>&1; then
    old_docker_ready=true
    break
  fi
  sleep 0.5
done
test "$old_docker_ready" = true
old_bridge=$(< /sys/class/net/docker0/ifindex)
old_db=$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' ewp-postgres-preview-20261006)
old_site=$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)

if [ -e "$app" ]; then
  test -d "$app" && test ! -L "$app"
  test "$(sha256sum "$app/server.js" | cut -d' ' -f1)" = 1ba9eeabba471a125a2664d9d7adf6fd48a957d69b2dd14ee8383e59ec3e08e9
  test "$(sha256sum "$app/.next/BUILD_ID" | cut -d' ' -f1)" = f0a7858579573f9eea7ef0ef3ce6c57b75b0f6e9c39d78c8faaa2b07318d06f8
else
  mkdir -m 0700 "$app"
  tar -xzf "$archive" -C "$app" --no-same-owner
fi

netid=$(docker network ls -q --filter "name=^${network}$")
if [ -z "$netid" ]; then
  docker network create --internal --driver bridge --subnet 172.29.77.0/24 \
    --opt com.docker.network.bridge.name=br-ewp-r2-1007 "$network" >/dev/null
fi
test "$(docker network inspect "$network" --format '{{.Internal}}')" = true
test "$(docker network inspect "$network" --format '{{range .IPAM.Config}}{{.Subnet}}{{end}}')" = 172.29.77.0/24

container=$(docker ps -aq --filter "name=^/${name}$")
if [ -n "$container" ]; then
  test "$(docker inspect "$name" --format '{{.Config.Image}}')" = "$image"
  test "$(docker inspect "$name" --format '{{.HostConfig.NetworkMode}}')" = "$network"
  test "$(docker inspect "$name" --format '{{.HostConfig.Runtime}}')" = runsc
  if [ "$(docker inspect "$name" --format '{{.State.Running}}')" != true ]; then docker start "$name" >/dev/null; fi
else
  docker run -d --pull never --name "$name" --log-driver local --log-opt max-size=1m --log-opt max-file=2 --log-opt compress=false \
    --runtime runsc --network "$network" --ip 172.29.77.2 --read-only --cap-drop ALL --security-opt no-new-privileges \
    --user 1000:1000 --pids-limit 64 --memory 768m --memory-swap 768m --cpus 1 \
    --tmpfs /tmp:rw,nosuid,nodev,noexec,size=32m \
    --mount "type=bind,src=$app,dst=/app,readonly" --workdir /app \
    --env NODE_ENV=production --env PORT=3027 --env HOSTNAME=0.0.0.0 \
    --entrypoint node "$image" server.js >/dev/null
fi
test "$(docker inspect "$name" --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}')" = 172.29.77.2

ready=false
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
  if nsenter -t "$pid" -n curl --noproxy '*' --silent --fail --max-time 2 http://172.29.77.2:3027/api/health >/dev/null 2>&1; then ready=true; break; fi
  sleep 0.3
done
test "$ready" = true
test "$(< /sys/class/net/docker0/ifindex)" = "$old_bridge"
test "$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' ewp-postgres-preview-20261006)" = "$old_db"
test "$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)" = "$old_site"
printf 'r2_runtime_recovered=healthy old_containers=unchanged\n'
