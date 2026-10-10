#!/usr/bin/env bash
# 新平台专用控制 API r3：备份旧 binary，启动时应用迁移 015；失败时只回滚 binary。
# 不触碰旧 IP 服务、旧数据库或其他 ECS 仓库。
set -euo pipefail

root=/data/ewp-preview-20261006
bin="$root/bin"
old="$bin/ewp-api-linux-amd64"
backup="$bin/ewp-api-linux-amd64.pre-r3"
incoming="$bin/ewp-api-r3-linux-amd64"
test -x "$old"
test -x "$incoming"
test ! -e "$backup"
cp -p "$old" "$backup"

committed=false
restore() {
  if [ "$committed" = false ]; then
    cp -p "$backup" "$bin/.ewp-api-restore-r3"
    mv -f "$bin/.ewp-api-restore-r3" "$old"
    systemctl restart ewp-preview-api-20261006.service
  fi
}
trap restore EXIT

cp -p "$incoming" "$bin/.ewp-api-next-r3"
mv -f "$bin/.ewp-api-next-r3" "$old"
systemctl restart ewp-preview-api-20261006.service

deadline=$((SECONDS + 12))
status=unavailable
while (( SECONDS < deadline )); do
  status=$(curl --noproxy '*' --silent --cacert "$root/private/ca.crt" --resolve console.ecp.lingtong.com:443:127.0.0.1 -o /dev/null -w '%{http_code}' https://console.ecp.lingtong.com/v1/me) || status=unavailable
  if [ "$status" = 401 ]; then
    break
  fi
  sleep 0.2
done
test "$status" = 401
version=$(docker exec ewp-postgres-preview-20261006 psql -U postgres -Atc "SELECT count(*) FROM schema_migrations WHERE version='015_project_resource_reservations.sql'")
test "$version" = 1
committed=true
printf 'new_platform_api_updated anonymous=%s migration015=%s\n' "$status" "$version"
