#!/usr/bin/env bash
# 仅替换新网站平台 Gateway；有界失败恢复旧二进制，旧站和默认 Docker 不动。
set -euo pipefail
root=/data/ewp-preview-20261006
active="$root/bin/ewp-site-gateway-linux-amd64"
candidate="$root/bin/ewp-r2-site-gateway-linux-amd64"
backup="$root/state/ewp-site-gateway-linux-amd64.pre-r2-loop"
unit=ewp-preview-site-20261006.service
test "$(sha256sum "$candidate" | cut -d' ' -f1)" = b2b60736f193c8d94a05a58e4c2a0ccb2f129254a7d01d94465261fead2ea98d
test "$(sha256sum "$active" | cut -d' ' -f1)" = e4d68c61f23fcec1269f8995c09020c247aa14a30c446ab2a9d2bd9e00bfad42
if [ -e "$backup" ]; then
  test "$(sha256sum "$backup" | cut -d' ' -f1)" = e4d68c61f23fcec1269f8995c09020c247aa14a30c446ab2a9d2bd9e00bfad42
else
  cp -p "$active" "$backup"
fi
test "$(sha256sum "$root/state/pre-migration20-r2-loop.dump" | cut -d' ' -f1)" = c3435880f2dc3aa2cd2c6b0be651c8cfbd1efa222d9d718969d768599991a645
old_db=$(docker inspect --format '{{.Id}}' ewp-postgres-preview-20261006)
old_site=$(docker inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)
bridge=$(< /sys/class/net/docker0/ifindex)
committed=false
rollback() {
  if [ "$committed" = false ]; then
    install -m 0700 "$backup" "$active"
    systemctl restart "$unit" || true
  fi
}
trap rollback EXIT
install -m 0700 "$candidate" "$active"
systemctl restart "$unit"
test "$(systemctl is-active "$unit")" = active
set -a
source "$root/private/site.env"
set +a
test "$SITE_LISTEN_ADDR" = 127.0.0.1:18443
test "$(psql "$DATABASE_URL" -Atc 'SELECT count(*) FROM schema_migrations')" = 20
status=''
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
  status=$(curl --noproxy '*' --silent --max-time 2 --cacert "$root/private/ca.crt" \
    --resolve b.preview.ecp.lingtong.com:18443:127.0.0.1 --output /dev/null --write-out '%{http_code}' \
    https://b.preview.ecp.lingtong.com:18443/) || status=''
  if [ "$status" = 401 ]; then break; fi
  test "$(systemctl is-active "$unit")" = active
  sleep 0.2
done
test "$status" = 401
test "$(curl --noproxy '*' --silent --show-error --max-time 5 -o /dev/null -w '%{http_code}' https://114.55.237.62/)" = 401
test "$(< /sys/class/net/docker0/ifindex)" = "$bridge"
test "$(docker inspect --format '{{.Id}}' ewp-postgres-preview-20261006)" = "$old_db"
test "$(docker inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)" = "$old_site"
committed=true
printf 'gateway_r2=active migrations=20 old_preview=401 old_site=401 legacy_containers=unchanged\n'
