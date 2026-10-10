#!/usr/bin/env bash
# 只回收失败的 v3 探针，绝不停止仍健康的 v2 动态实例或共享 private bridge。
set -euo pipefail
set -a
source /data/ewp-preview-20261006/private/site.env
set +a
state=$(psql "$DATABASE_URL" -Atc "SELECT rt.state FROM site_runtime_pilots rt JOIN site_deployments d ON d.id=rt.deployment_id WHERE rt.team_id='t_f9cf963718a72612c48940af8a8296e4' AND rt.project_id='p_34401f5c05af1079010cb95ee3ec1275' AND rt.upstream_socket='/run/ewp-r2-20261007/v3.sock' AND d.bundle_digest='8668dfa8e229e7305b286b85122513651107631447125d78ddf9b307b80c6e78' ORDER BY d.created_at DESC,d.id DESC LIMIT 1")
if [ "$state" = ready ]; then exit 0; fi
test "$state" = failed
export DOCKER_HOST=unix:///run/ewp-builder-20261007/docker.sock
name=ewp-r2-next15-v3-20261008
container=$(timeout -k 2s 5s docker ps -aq --filter "name=^/${name}$")
if [ -n "$container" ]; then
  test "$(docker inspect "$name" --format '{{.Config.Image}}')" = sha256:e894118949319b479bfc3e16e545cb2f85b7a2194aa86ea9cd4e29538f2b13c3
  test "$(docker inspect "$name" --format '{{.HostConfig.NetworkMode}}')" = ewp-r2-next15-loop-20261007
  timeout -k 2s 10s docker rm -f "$name" >/dev/null
fi
systemctl stop ewp-r2-runtime-relay-v3-20261008.service || true
test -z "$(docker ps -aq --filter "name=^/${name}$")"
test "$(docker inspect ewp-r2-next15-loop-20261007 --format '{{.State.Running}}')" = true
printf 'r2_v3_failed=terminated v2_runtime=retained\n'
