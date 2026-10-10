#!/usr/bin/env bash
# 在双版本共存之前升级新平台网关；保护现有 Stage 和旧站。
set -euo pipefail
root=/data/ewp-preview-20261006
active="$root/bin/ewp-site-gateway-linux-amd64"
candidate="$root/bin/ewp-r2-site-gateway-v3-linux-amd64"
backup="$root/state/ewp-site-gateway-linux-amd64.pre-multiversion"
unit=ewp-preview-site-20261006.service
test "$(sha256sum "$candidate" | cut -d' ' -f1)" = 0949a16be3059e5ee8ed17cb9c9fdaa627807dbe5b68a16d763ac22955cc1829
test "$(sha256sum "$active" | cut -d' ' -f1)" = b2b60736f193c8d94a05a58e4c2a0ccb2f129254a7d01d94465261fead2ea98d
test "$(sha256sum "$root/state/pre-r2-multiversion-20261008.dump" | cut -d' ' -f1)" = bbcb6ced6d37d42c9ee074848943c6693b644dd7d57d71fcbfc40a45c423b65b
test ! -e "$backup"
cp -p "$active" "$backup"
bridge=$(< /sys/class/net/docker0/ifindex)
old_db=$(docker inspect --format '{{.Id}}' ewp-postgres-preview-20261006)
old_site=$(docker inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)
accepted=false
rollback() {
  if [ "$accepted" = false ]; then
    install -m 0700 "$backup" "$active"
    systemctl restart "$unit" || true
  fi
}
trap rollback EXIT
install -m 0700 "$candidate" "$active"
systemctl restart "$unit"
test "$(systemctl is-active "$unit")" = active
status=''
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
  status=$(curl --noproxy '*' --silent --max-time 2 --cacert "$root/private/ca.crt" \
    --resolve r2-stage.preview.ecp.lingtong.com:18443:127.0.0.1 -o /dev/null -w '%{http_code}' \
    https://r2-stage.preview.ecp.lingtong.com:18443/api/hello) || status=''
  if [ "$status" = 200 ]; then break; fi
  test "$(systemctl is-active "$unit")" = active
  sleep 0.2
done
test "$status" = 200
test "$(curl --noproxy '*' --silent --show-error --max-time 5 -o /dev/null -w '%{http_code}' https://114.55.237.62/)" = 401
test "$(< /sys/class/net/docker0/ifindex)" = "$bridge"
test "$(docker inspect --format '{{.Id}}' ewp-postgres-preview-20261006)" = "$old_db"
test "$(docker inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)" = "$old_site"
accepted=true
printf 'r2_multiversion_gateway=active stage_api=200 legacy=401 old_containers=unchanged\n'
