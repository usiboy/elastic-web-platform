#!/usr/bin/env bash
# Builder 试点全容器时限：覆盖解包、npm ci、Vite 与归档；超时清理具名容器。
set -euo pipefail
export DOCKER_HOST=unix:///run/ewp-builder-20261007/docker.sock
case "${1:-}:${2:-}" in
  testA:100|testB:100|probe:4) project=$1 duration=$2 ;;
  *) printf '仅支持固定 A/B 作业或 4 秒超时探针\n' >&2; exit 2 ;;
esac
shift 2
test "$#" -gt 0
name=ewp-builder-deadline-${project}-20261007
test "$(systemctl show ewp-builder-dockerd-20261007.service -p PrivateNetwork --value)" = yes
test "$(systemctl is-active ewp-builder-dockerd-20261007.service)" = active
existing=$(timeout -k 2s 5s docker ps -aq --filter "name=^/${name}$")
if [ -n "$existing" ]; then
  printf 'builder_stale_container=%s: manual investigation required\n' "$name" >&2
  exit 1
fi

result=0
timeout -k 5s "${duration}s" docker run --name "$name" "$@" || result=$?
# --rm 在正常退出时自动回收；超时可能只杀掉客户端，必须检查并清理 daemon 内残留。
remaining=$(timeout -k 2s 5s docker ps -aq --filter "name=^/${name}$")
if [ -n "$remaining" ]; then
  timeout -k 2s 10s docker rm -f "$name" >/dev/null || {
    printf 'builder_cleanup_failed=%s\n' "$name" >&2
    exit 1
  }
fi
remaining=$(timeout -k 2s 5s docker ps -aq --filter "name=^/${name}$")
if [ -n "$remaining" ]; then
  printf 'builder_container_still_present=%s\n' "$name" >&2
  exit 1
fi
if [ "$result" -ne 0 ]; then
  printf 'builder_container_failed=%s exit=%s\n' "$name" "$result" >&2
fi
exit "$result"
