#!/usr/bin/env bash
# 给同机 Builder daemon 增加启动前 netns 断言，避免配置漂移再次删宿主 docker0。
# 只重启新平台 Builder service；失败恢复此前已验的 PrivateNetwork 单元。
set -euo pipefail

unit=ewp-builder-dockerd-20261007.service
source=/data/ewp-builder-20261007/state/$unit
backup=/data/ewp-builder-20261007/state/$unit.pre-startpre
test "$(sha256sum "$source" | cut -d' ' -f1)" = f979678a8cd302346ac9084624eae11dee27ed16b7928d3b2864f2a6787d2cfe
test "$(sha256sum "$backup" | cut -d' ' -f1)" = d033d186cb5e253bd00075756d54a6cdb5d0f5cc6ca312822b7c21911980ebbd
test "$(sha256sum "/etc/systemd/system/$unit" | cut -d' ' -f1)" = d033d186cb5e253bd00075756d54a6cdb5d0f5cc6ca312822b7c21911980ebbd
bridge=$(< /sys/class/net/docker0/ifindex)
old_db=$(docker inspect --format '{{.Id}}' ewp-postgres-preview-20261006)
old_app=$(docker inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)

committed=false
restore() {
  if [ "$committed" = false ]; then
    systemctl stop "$unit" || true
    install -m 0644 "$backup" "/etc/systemd/system/$unit"
    systemctl daemon-reload
    systemctl start "$unit" || true
  fi
}
trap restore EXIT

install -m 0644 "$source" "/etc/systemd/system/$unit"
systemctl daemon-reload
systemctl restart "$unit"
test "$(systemctl show "$unit" -p PrivateNetwork --value)" = yes
test "$(systemctl is-active "$unit")" = active
test "$(< /sys/class/net/docker0/ifindex)" = "$bridge"
test "$(docker inspect --format '{{.Id}}' ewp-postgres-preview-20261006)" = "$old_db"
test "$(docker inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)" = "$old_app"
secret=''
IFS= read -r secret < /data/ewp-preview-20261006/private/owner-b.token
status=$(printf 'header = "Authorization: Bearer %s"\n' "$secret" | curl --config - --noproxy '*' --silent --show-error --max-time 5 \
  --cacert /data/ewp-preview-20261006/private/ca.crt --resolve console.ecp.lingtong.com:443:127.0.0.1 \
  --output /dev/null --write-out '%{http_code}' https://console.ecp.lingtong.com/v1/me)
secret=''
test "$status" = 200
test "$(curl --noproxy '*' --silent --show-error --max-time 5 -o /dev/null -w '%{http_code}' https://114.55.237.62/)" = 401
committed=true
printf 'builder_startpre=host_network_rejected isolated_restart=passed old_containers=unchanged owner_b=200 legacy=401\n'
