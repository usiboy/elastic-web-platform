#!/usr/bin/env bash
# 受控人工恢复入口：使用持久 systemd unit，不再创建旧 transient relay。
set -euo pipefail
runtime=ewp-r2-next-runtime-20261008.service
relay=ewp-r2-relay-persistent-20261008.service
socket=/run/ewp-r2-20261007/runtime.sock
test "$(systemctl is-enabled "$runtime")" = enabled
test "$(systemctl is-enabled "$relay")" = enabled
test "$(systemctl show ewp-builder-dockerd-20261007.service -p PrivateNetwork --value)" = yes
set -a
source /data/ewp-preview-20261006/private/site.env
set +a
state=$(psql "$DATABASE_URL" -Atc "SELECT rt.state FROM site_runtime_pilots rt JOIN site_deployments d ON d.id=rt.deployment_id WHERE rt.team_id='t_f9cf963718a72612c48940af8a8296e4' AND rt.project_id='p_34401f5c05af1079010cb95ee3ec1275' AND rt.upstream_socket='/run/ewp-r2-20261007/runtime.sock' ORDER BY d.created_at DESC,d.id DESC LIMIT 1")
test "$state" = ready # 已标记失败的旧版本不得靠重启冒充新部署。
bridge=$(< /sys/class/net/docker0/ifindex)
old_db=$(docker inspect --format '{{.Id}}' ewp-postgres-preview-20261006)
old_site=$(docker inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)
if ! curl --unix-socket "$socket" --silent --fail --max-time 2 http://runtime.internal/api/health >/dev/null 2>&1; then
  systemctl restart "$runtime"
  systemctl restart "$relay"
fi
ready=false
for attempt in 1 2 3 4 5 6 7 8 9 10; do
  if curl --unix-socket "$socket" --silent --fail --max-time 2 http://runtime.internal/api/health >/dev/null 2>&1; then ready=true; break; fi
  sleep 0.2
done
test "$ready" = true
test "$(< /sys/class/net/docker0/ifindex)" = "$bridge"
test "$(docker inspect --format '{{.Id}}' ewp-postgres-preview-20261006)" = "$old_db"
test "$(docker inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)" = "$old_site"
printf 'r2_synthetic_runtime=healthy via=persistent_units legacy=unchanged\n'
