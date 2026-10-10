#!/usr/bin/env bash
# 固定合成 Next 15 standalone 在真实 runsc 中做 SSR/API/流式响应探针。
# Builder service 的 PrivateNetwork=yes 下创建一次性 internal bridge，只供受控验证。
set -euo pipefail
umask 077
export DOCKER_HOST=unix:///run/ewp-builder-20261007/docker.sock
root=/data/ewp-builder-20261007
unit=ewp-builder-dockerd-20261007.service
archive="$root/state/ewp-r2-next15-standalone-20261007.tar.gz"
app="$root/state/r2-next15-standalone"
name=ewp-r2-next15-probe-20261007
network=ewp-r2-next15-internal-20261007
image=sha256:e894118949319b479bfc3e16e545cb2f85b7a2194aa86ea9cd4e29538f2b13c3
test "$(sha256sum "$archive" | cut -d' ' -f1)" = d35c46dc9b64df6fef0932cc1d96f4aac283c4ab1b1ccd9ae7f52212a356ec89
test "$(systemctl show "$unit" -p PrivateNetwork --value)" = yes
test "$(systemctl is-active "$unit")" = active
pid=$(systemctl show "$unit" -p MainPID --value)
test "$pid" -gt 1
test "$(stat -Lc %i /proc/1/ns/net)" != "$(stat -Lc %i "/proc/$pid/ns/net")"
test "$(docker image inspect "$image" --format '{{.Id}}')" = "$image"
test -z "$(docker ps -aq --filter "name=^/${name}$")"
test -z "$(docker network ls -q --filter "name=^${network}$")"
bridge=$(< /sys/class/net/docker0/ifindex)
old_db=$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' ewp-postgres-preview-20261006)
old_site=$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)

if [ ! -d "$app" ]; then
  mkdir -m 0700 "$app"
  tar -xzf "$archive" -C "$app" --no-same-owner
fi
test -f "$app/server.js" && test -f "$app/.next/BUILD_ID"
started=false
network_created=false
cleanup() {
  if [ "$started" = true ]; then timeout -k 2s 10s docker rm -f "$name" >/dev/null 2>&1 || true; fi
  if [ "$network_created" = true ]; then timeout -k 2s 10s docker network rm "$network" >/dev/null 2>&1 || true; fi
}
trap cleanup EXIT
docker network create --internal --driver bridge --subnet 172.29.77.0/24 \
  --opt com.docker.network.bridge.name=br-ewp-r2-1007 "$network" >/dev/null
network_created=true
started=true
docker run -d --pull never --name "$name" --log-driver local --log-opt max-size=1m --log-opt max-file=1 --log-opt compress=false --runtime runsc \
  --network "$network" --read-only --cap-drop ALL --security-opt no-new-privileges \
  --user 1000:1000 --pids-limit 64 --memory 768m --memory-swap 768m --cpus 1 \
  --tmpfs /tmp:rw,nosuid,nodev,noexec,size=32m \
  --mount "type=bind,src=$app,dst=/app,readonly" --workdir /app \
  --env NODE_ENV=production --env PORT=3027 --env HOSTNAME=0.0.0.0 \
  --entrypoint node "$image" server.js >/dev/null
test "$(docker inspect "$name" --format '{{.HostConfig.NetworkMode}}')" = "$network"
ip=$(docker inspect "$name" --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}')
[[ "$ip" == 172.29.77.* ]]
base="http://$ip:3027"
ready=false
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
  if nsenter -t "$pid" -n curl --noproxy '*' --silent --fail --max-time 2 "$base/api/hello" >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 0.3
done
if [ "$ready" != true ]; then
  docker inspect "$name" --format 'r2_next_container_state={{.State.Status}} exit={{.State.ExitCode}}' >&2 || true
  docker logs "$name" >&2 || true
  exit 1
fi
nsenter -t "$pid" -n curl --noproxy '*' --silent --show-error --fail --max-time 5 "$base/api/hello" |
  python3 -c 'import json,sys; item=json.load(sys.stdin); assert item=={"service":"r2-next-synthetic","node":"22.22.0"}; print("r2_next_api=200 node=22.22.0")'

page1=$(nsenter -t "$pid" -n curl --noproxy '*' --silent --show-error --fail --max-time 5 "$base/" |
  python3 -c 'import re,sys; page=sys.stdin.read(); assert "R2 合成 SSR" in page; match=re.search(r"data-request-id=\"([0-9a-f-]{36})\"",page); assert match; print(match.group(1))')
page2=$(nsenter -t "$pid" -n curl --noproxy '*' --silent --show-error --fail --max-time 5 "$base/" |
  python3 -c 'import re,sys; match=re.search(r"data-request-id=\"([0-9a-f-]{36})\"",sys.stdin.read()); assert match; print(match.group(1))')
test "$page1" != "$page2"
nsenter -t "$pid" -n python3 - "$base/api/stream" <<'PY'
import time, urllib.request
import sys
url=sys.argv[1]
with urllib.request.urlopen(url, timeout=5) as response:
    assert response.status == 200
    first=response.readline()
    started=time.monotonic()
    second=response.readline()
    assert first == b"start\n" and second == b"done\n"
    assert time.monotonic()-started >= 0.05
print("r2_next_ssr=per_request stream=two_timed_chunks")
PY
timeout -k 2s 10s docker rm -f "$name" >/dev/null
started=false
timeout -k 2s 10s docker network rm "$network" >/dev/null
network_created=false
test -z "$(docker ps -aq --filter "name=^/${name}$")"
test -z "$(docker network ls -q --filter "name=^${network}$")"
test "$(< /sys/class/net/docker0/ifindex)" = "$bridge"
test "$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' ewp-postgres-preview-20261006)" = "$old_db"
test "$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)" = "$old_site"
printf 'r2_next_runsc=verified sandbox_removed=1 docker0_unchanged=1 legacy_containers_unchanged=1\n'
