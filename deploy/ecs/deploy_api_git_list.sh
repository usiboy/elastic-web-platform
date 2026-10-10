#!/usr/bin/env bash
# 仅更新新平台专用控制 API：016 项目 Git 状态索引与列表；保留旧 binary。
set -euo pipefail

root=/data/ewp-preview-20261006
bin="$root/bin"
old="$bin/ewp-api-linux-amd64"
backup="$bin/ewp-api-linux-amd64.pre-git-list"
incoming="$bin/ewp-api-git-list-linux-amd64"
test -x "$old"
test -x "$incoming"
test ! -e "$backup"
test "$(sha256sum "$incoming" | cut -d' ' -f1)" = e9eaec4dac02b37889944de78d50b308b7bf48a87170cc3f214323509326ea48
test -s "$root/state/pre-migration16.dump"
cp -p "$old" "$backup"

committed=false
restore() {
  if [ "$committed" = false ]; then
    cp -p "$backup" "$bin/.ewp-api-restore-git-list"
    mv -f "$bin/.ewp-api-restore-git-list" "$old"
    systemctl restart ewp-preview-api-20261006.service
  fi
}
trap restore EXIT

cp -p "$incoming" "$bin/.ewp-api-next-git-list"
mv -f "$bin/.ewp-api-next-git-list" "$old"
systemctl restart ewp-preview-api-20261006.service

deadline=$((SECONDS + 15))
status=unavailable
while (( SECONDS < deadline )); do
  status=$(curl --noproxy '*' --silent --cacert "$root/private/ca.crt" --resolve console.ecp.lingtong.com:443:127.0.0.1 -o /dev/null -w '%{http_code}' https://console.ecp.lingtong.com/v1/me) || status=unavailable
  if [ "$status" = 401 ]; then break; fi
  sleep 0.2
done
test "$status" = 401
version=$(docker exec ewp-postgres-preview-20261006 psql -U postgres -Atc 'SELECT count(*) FROM schema_migrations')
test "$version" = 16
committed=true
printf 'new_platform_api_git_status anonymous=%s migrations=%s\n' "$status" "$version"
