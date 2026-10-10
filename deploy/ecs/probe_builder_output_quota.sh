#!/usr/bin/env bash
# 独立探测卷：证明宿主 tmpfs 硬额会阻断 runsc 内写入，并在退出时清理。
set -euo pipefail
export DOCKER_HOST=unix:///run/ewp-builder-20261007/docker.sock
name=ewp-builder-quota-probe-20261007
image=sha256:e894118949319b479bfc3e16e545cb2f85b7a2194aa86ea9cd4e29538f2b13c3
test "$(systemctl show ewp-builder-dockerd-20261007.service -p PrivateNetwork --value)" = yes
test ! "$(docker volume ls -q --filter "name=^${name}$")"
path=''
mounted=false
cleanup() {
  if [ "$mounted" = true ]; then umount "$path" || return 1; fi
  docker volume rm "$name" >/dev/null
}
docker volume create "$name" >/dev/null
trap cleanup EXIT
path=$(docker volume inspect "$name" --format '{{.Mountpoint}}')
test "$path" = "/data/ewp-builder-20261007/docker/volumes/$name/_data"
test ! -L "$path" && test -d "$path" && ! mountpoint -q "$path"
mount -t tmpfs -o size=8m,nosuid,nodev,noexec,mode=0700,uid=1000,gid=1000 tmpfs "$path"
mounted=true
test "$(findmnt -n -o FSTYPE -M "$path")" = tmpfs
test "$(findmnt -n -o SIZE -M "$path" | tr -d ' ')" = 8M
docker run --rm --pull never --runtime runsc --network none --read-only --cap-drop ALL \
  --security-opt no-new-privileges --user 1000:1000 --pids-limit 64 \
  --memory 256m --memory-swap 256m \
  --mount "type=volume,src=$name,dst=/out" --entrypoint sh "$image" -c \
  'test "$(df -k /out | tail -1 | awk "{print \$2}")" -le 8192 && if dd if=/dev/zero of=/out/oversize bs=1048576 count=16 status=none 2>/dev/null; then exit 99; fi; test "$(stat -c %s /out/oversize)" -le 8388608'
printf 'runsc_output_quota=verified limit=8MiB oversize=ENOSPC\n'
