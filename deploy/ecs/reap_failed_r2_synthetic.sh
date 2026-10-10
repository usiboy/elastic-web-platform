#!/usr/bin/env bash
# 同机合成动态版本失败后，仅回收固定 Next 探针和 relay；旧站/旧 Docker 不参与。
set -euo pipefail
root=/data/ewp-preview-20261006
set -a
source "$root/private/site.env"
set +a
test -n "$DATABASE_URL"
state=$(psql "$DATABASE_URL" -Atc "SELECT rt.state FROM site_runtime_pilots rt JOIN site_deployments d ON d.id=rt.deployment_id WHERE rt.team_id='t_f9cf963718a72612c48940af8a8296e4' AND rt.project_id='p_34401f5c05af1079010cb95ee3ec1275' AND rt.upstream_socket='/run/ewp-r2-20261007/runtime.sock' ORDER BY d.created_at DESC,d.id DESC LIMIT 1")
if [ "$state" = ready ]; then exit 0; fi
test "$state" = failed
export DOCKER_HOST=unix:///run/ewp-builder-20261007/docker.sock
name=ewp-r2-next15-loop-20261007
network=ewp-r2-next15-loop-20261007
container=$(timeout -k 2s 5s docker ps -aq --filter "name=^/${name}$")
if [ -n "$container" ]; then
  test "$(docker inspect "$name" --format '{{.Config.Image}}')" = sha256:e894118949319b479bfc3e16e545cb2f85b7a2194aa86ea9cd4e29538f2b13c3
  test "$(docker inspect "$name" --format '{{.HostConfig.NetworkMode}}')" = "$network"
  timeout -k 2s 10s docker rm -f "$name" >/dev/null
fi
systemctl stop ewp-r2-runtime-relay-20261007.service || true
present=$(timeout -k 2s 5s docker network ls -q --filter "name=^${network}$")
if [ -n "$present" ]; then timeout -k 2s 10s docker network rm "$network" >/dev/null; fi
test -z "$(docker ps -aq --filter "name=^/${name}$")"
test -z "$(docker network ls -q --filter "name=^${network}$")"
printf 'r2_failed_runtime=terminated fallback=previous_static\n'
