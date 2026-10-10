#!/usr/bin/env bash
# 在固定镜像的真实 runsc 中 sleep，校验宿主截止时间后无残留容器。
set -euo pipefail
root=/data/ewp-builder-20261007
export DOCKER_HOST=unix:///run/ewp-builder-20261007/docker.sock
image=sha256:e894118949319b479bfc3e16e545cb2f85b7a2194aa86ea9cd4e29538f2b13c3
test "$(docker image inspect "$image" --format '{{.Id}}')" = "$image"
start=$(date +%s)
result=0
bash "$root/state/run_builder_deadline.sh" probe 4 \
  --rm --pull never --log-driver none --runtime runsc --network none --read-only --cap-drop ALL \
  --security-opt no-new-privileges --user 1000:1000 --pids-limit 64 \
  --memory 256m --memory-swap 256m --cpus 1 \
  --entrypoint sh "$image" -c 'sleep 60' || result=$?
elapsed=$(( $(date +%s) - start ))
test "$result" -eq 124 && test "$elapsed" -ge 4 && test "$elapsed" -lt 30
test -z "$(docker ps -aq --filter name=^/ewp-builder-deadline-probe-20261007$)"
printf 'runsc_whole_container_timeout=verified deadline=4s exit=%s elapsed=%ss stale=0\n' "$result" "$elapsed"
