#!/usr/bin/env bash
# 第二 daemon 必须在独立 network namespace 中；宿主 docker0/旧站不变才保留运行。
# 失败立即停止本服务。若宿主 bridge 已损坏，操作者须先按实际 veth 归属恢复，不继续构建。
set -euo pipefail

unit=ewp-builder-dockerd-20261007.service
test "$(systemctl show "$unit" -p PrivateNetwork --value)" = yes
unit_state=$(systemctl show "$unit" -p UnitFileState --value)
test "$unit_state" = enabled || test "$unit_state" = disabled
test -e /sys/class/net/docker0/ifindex
old_bridge=$(< /sys/class/net/docker0/ifindex)
old_db=$(docker inspect --format '{{.Id}}' ewp-postgres-preview-20261006)
old_app=$(docker inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)

accepted=false
stop_on_failure() {
  if [ "$accepted" = false ]; then systemctl stop "$unit" || true; fi
}
trap stop_on_failure EXIT
systemctl start "$unit"
pid=$(systemctl show "$unit" -p MainPID --value)
test "$pid" -gt 1
test "$(stat -Lc %i /proc/1/ns/net)" != "$(stat -Lc %i "/proc/$pid/ns/net")"
test -e /sys/class/net/docker0/ifindex
test "$(< /sys/class/net/docker0/ifindex)" = "$old_bridge"
test "$(docker inspect --format '{{.Id}}' ewp-postgres-preview-20261006)" = "$old_db"
test "$(docker inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)" = "$old_app"
test "$(curl --noproxy '*' --silent --show-error --max-time 5 -o /dev/null -w '%{http_code}' https://114.55.237.62/)" = 401
export DOCKER_HOST=unix:///run/ewp-builder-20261007/docker.sock
test "$(docker info --format '{{.CgroupDriver}}')" = cgroupfs
docker info --format '{{json .Runtimes}}' | python3 -c 'import json,sys; assert json.load(sys.stdin).get("runsc",{}).get("path")=="/usr/local/bin/runsc"'

owner=/data/ewp-preview-20261006/private/owner-b.token
IFS= read -r secret < "$owner"
status=$(printf 'header = "Authorization: Bearer %s"\n' "$secret" | curl --config - --noproxy '*' --silent --show-error --max-time 5 \
  --cacert /data/ewp-preview-20261006/private/ca.crt --resolve console.ecp.lingtong.com:443:127.0.0.1 \
  --output /dev/null --write-out '%{http_code}' https://console.ecp.lingtong.com/v1/me)
secret=""
test "$status" = 200
accepted=true
printf 'builder_network_namespace=verified docker0=unchanged owner_b=200 legacy=401\n'
