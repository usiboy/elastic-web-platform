#!/usr/bin/env bash
# R2 无凭据短时探针：真实 runsc Node HTTP/API/流式响应与超时后杀停。
# 只操作独立 Builder daemon 上的具名探针容器，不路由旧站、不加载业务秘密。
set -euo pipefail
umask 077
export DOCKER_HOST=unix:///run/ewp-builder-20261007/docker.sock
unit=ewp-builder-dockerd-20261007.service
name=ewp-r2-node-probe-20261007
image=sha256:e894118949319b479bfc3e16e545cb2f85b7a2194aa86ea9cd4e29538f2b13c3
test "$(systemctl show "$unit" -p PrivateNetwork --value)" = yes
test "$(systemctl is-active "$unit")" = active
test "$(docker image inspect "$image" --format '{{.Id}}')" = "$image"
existing=$(timeout -k 2s 5s docker ps -aq --filter "name=^/${name}$")
test -z "$existing" || { printf 'R2 probe container already exists; investigate manually\n' >&2; exit 1; }

bridge=$(< /sys/class/net/docker0/ifindex)
old_db=$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' ewp-postgres-preview-20261006)
old_site=$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)
started=false
cleanup() {
  if [ "$started" = true ]; then timeout -k 2s 10s docker rm -f "$name" >/dev/null 2>&1 || true; fi
}
trap cleanup EXIT

docker run -d --rm --pull never --name "$name" --log-driver none --runtime runsc \
  --network none --read-only --cap-drop ALL --security-opt no-new-privileges \
  --user 1000:1000 --pids-limit 32 --memory 256m --memory-swap 256m --cpus 0.5 \
  --tmpfs /tmp:rw,nosuid,nodev,noexec,size=16m \
  --entrypoint node "$image" -e '
const http = require("node:http");
http.createServer((request, response) => {
  if (request.url === "/api/hello") {
    response.setHeader("content-type", "application/json");
    response.end(JSON.stringify({service: "r2-synthetic", node: process.versions.node}));
  } else if (request.url === "/stream") {
    response.setHeader("content-type", "text/plain");
    response.write("start\n");
    setTimeout(() => response.end("done\n"), 120);
  } else if (request.url === "/hang") {
    response.write("start\n");
    setTimeout(() => response.end("late\n"), 60000);
  } else {
    response.writeHead(404);
    response.end("not found");
  }
}).listen(3000, "127.0.0.1");
' >/dev/null
started=true

ready=false
for attempt in 1 2 3 4 5 6 7 8 9 10; do
  if timeout -k 2s 4s docker exec "$name" node -e 'fetch("http://127.0.0.1:3000/api/hello").then(r => { if (r.status !== 200) process.exitCode = 1 }, () => process.exitCode = 1)' >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 0.2
done
test "$ready" = true
timeout -k 2s 8s docker exec "$name" node -e '
const assert = require("node:assert/strict");
(async () => {
  const base = "http://127.0.0.1:3000";
  const api = await fetch(base + "/api/hello");
  assert.equal(api.status, 200);
  assert.deepEqual(await api.json(), {service: "r2-synthetic", node: "22.22.0"});
  const stream = await fetch(base + "/stream");
  assert.equal(stream.status, 200);
  const reader = stream.body.getReader();
  const decoder = new TextDecoder();
  assert.equal(decoder.decode((await reader.read()).value), "start\n");
  assert.equal(decoder.decode((await reader.read()).value), "done\n");
  assert.equal((await fetch(base + "/missing")).status, 404);
  console.log("r2_node_api=200 stream=two_chunks missing=404 node=22.22.0");
})().catch(error => { console.error(error); process.exitCode = 1 });
'

result=0
timeout -k 2s 2s docker exec "$name" node -e 'fetch("http://127.0.0.1:3000/hang").then(r => r.text())' >/dev/null 2>&1 || result=$?
test "$result" -eq 124
timeout -k 2s 10s docker rm -f "$name" >/dev/null
started=false
test -z "$(timeout -k 2s 5s docker ps -aq --filter "name=^/${name}$")"
test "$(< /sys/class/net/docker0/ifindex)" = "$bridge"
test "$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' ewp-postgres-preview-20261006)" = "$old_db"
test "$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)" = "$old_site"
printf 'r2_node_timeout=verified exit=124 sandbox_removed=1 docker0_unchanged=1 legacy_containers_unchanged=1\n'
