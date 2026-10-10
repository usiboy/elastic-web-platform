#!/usr/bin/env bash
# 固定试点专用：将新的输出卷挂到 64 MiB tmpfs，绝不覆盖已有无界产物卷。
set -euo pipefail
umask 077
export DOCKER_HOST=unix:///run/ewp-builder-20261007/docker.sock
root=/data/ewp-builder-20261007
case "${1:-}" in
  testA) volume=ewp-builder-a-output-bounded-20261007 ;;
  testB) volume=ewp-builder-b-output-bounded-20261007 ;;
  *) printf '仅支持固定的 testA/testB 输出卷\n' >&2; exit 2 ;;
esac

test "$(systemctl show ewp-builder-dockerd-20261007.service -p PrivateNetwork --value)" = yes
test "$(systemctl is-active ewp-builder-dockerd-20261007.service)" = active
docker volume create "$volume" >/dev/null
test "$(docker volume inspect "$volume" --format '{{.Driver}}')" = local
test "$(docker volume inspect "$volume" --format '{{json .Options}}')" = null
path=$(docker volume inspect "$volume" --format '{{.Mountpoint}}')
test "$path" = "$root/docker/volumes/$volume/_data" && test -d "$path" && test ! -L "$path"

created=false
ready=false
rollback() {
  if [ "$created" = true ] && [ "$ready" = false ]; then umount "$path"; fi
}
trap rollback EXIT
if ! mountpoint -q "$path"; then
  # 绝不在残留旧文件之上盖新挂载点；重启后 tmpfs 自然丢失，须重新准备。
  test -z "$(ls -A "$path")"
  mount -t tmpfs -o size=64m,nosuid,nodev,noexec,mode=0700,uid=1000,gid=1000 tmpfs "$path"
  created=true
fi
test "$(findmnt -n -o FSTYPE -M "$path")" = tmpfs
test "$(findmnt -n -o SIZE -M "$path" | tr -d ' ')" = 64M
options=$(findmnt -n -o OPTIONS -M "$path")
for required in rw nosuid nodev noexec; do
  [[ ",$options," == *",$required,"* ]]
done
test "$(stat -c '%u:%g' "$path")" = 1000:1000
ready=true
printf 'bounded_builder_output=%s size=64MiB mount=tmpfs\n' "$volume"
