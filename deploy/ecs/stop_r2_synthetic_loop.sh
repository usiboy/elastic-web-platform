#!/usr/bin/env bash
# 仅停止本轮合成 Next 试点，不涉及原站、控制 API 或默认 Docker daemon。
set -euo pipefail
export DOCKER_HOST=unix:///run/ewp-builder-20261007/docker.sock
systemctl stop ewp-r2-relay-persistent-20261008.service || true
systemctl stop ewp-r2-runtime-relay-20261007.service || true
systemctl stop ewp-r2-next-runtime-20261008.service || true
docker rm -f ewp-r2-next15-loop-20261007 >/dev/null 2>&1 || true
docker network rm ewp-r2-next15-loop-20261007 >/dev/null 2>&1 || true
test -z "$(docker ps -aq --filter name=^/ewp-r2-next15-loop-20261007$)"
test -z "$(docker network ls -q --filter name=^ewp-r2-next15-loop-20261007$)"
printf 'r2_synthetic_loop=stopped\n'
