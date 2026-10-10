#!/usr/bin/env bash
# Simulate only the synthetic runtime/relay/Gateway reboot path; never reboot ECS.
set -euo pipefail
export DOCKER_HOST=unix:///run/ewp-builder-20261007/docker.sock
runtime=ewp-r2-next-runtime-20261008.service
relay=ewp-r2-relay-persistent-20261008.service
site=ewp-r2-site-gateway-20261008.service
name=ewp-r2-next15-loop-20261007
socket=/run/ewp-r2-20261007/runtime.sock
for unit in "$runtime" "$relay" "$site" ewp-preview-api-20261006.service; do
  test "$(systemctl is-active "$unit")" = active
  test "$(systemctl is-enabled "$unit")" = enabled
done
test "$(docker --host unix:///var/run/docker.sock inspect ewp-postgres-preview-20261006 --format '{{.HostConfig.RestartPolicy.Name}}')" = unless-stopped
test "$(docker inspect "$name" --format '{{.State.Running}}')" = true
id=$(docker inspect "$name" --format '{{.Id}}')
bridge=$(< /sys/class/net/docker0/ifindex)
old_db=$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' ewp-postgres-preview-20261006)
old_site=$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)
set -a
source /data/ewp-preview-20261006/private/site.env
set +a
before=$(psql "$DATABASE_URL" -Atc "SELECT deployment_id||':'||generation FROM site_hosts WHERE hostname='r2-stage.preview.ecp.lingtong.com'")
test "$before" = dep_be15038c6bc73cd168e2b2ed967c6f11:6
accepted=false
restore() {
  if [ "$accepted" = true ]; then return; fi
  systemctl restart "$runtime" || true
  systemctl start "$relay" || true
  systemctl start "$site" || true
}
trap restore EXIT
systemctl stop "$site"
systemctl stop "$relay"
docker stop -t 5 "$name" >/dev/null
test "$(docker inspect "$name" --format '{{.State.Running}}')" = false
test "$(docker network inspect ewp-r2-next15-loop-20261007 --format '{{.Internal}}')" = true
systemctl restart "$runtime"
test "$(docker inspect "$name" --format '{{.Id}}')" = "$id"
test "$(docker inspect "$name" --format '{{.State.Running}}')" = true
systemctl start "$relay"
relay_ready=false
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
  if curl --unix-socket "$socket" --silent --fail --max-time 2 http://runtime.internal/api/health >/dev/null 2>&1; then relay_ready=true; break; fi
  sleep 0.2
done
test "$relay_ready" = true
systemctl start "$site"
status=''
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
  status=$(curl --noproxy '*' --silent --max-time 3 --cacert /data/ewp-preview-20261006/private/ca.crt \
    --resolve r2-stage.preview.ecp.lingtong.com:18443:127.0.0.1 -o /dev/null -w '%{http_code}' \
    https://r2-stage.preview.ecp.lingtong.com:18443/api/hello) || status=''
  if [ "$status" = 200 ]; then break; fi
  sleep 0.2
done
test "$status" = 200
after=$(psql "$DATABASE_URL" -Atc "SELECT deployment_id||':'||generation FROM site_hosts WHERE hostname='r2-stage.preview.ecp.lingtong.com'")
test "$before" = "$after"
test "$(curl --noproxy '*' --silent --show-error --max-time 5 -o /dev/null -w '%{http_code}' https://114.55.237.62/)" = 401
test "$(< /sys/class/net/docker0/ifindex)" = "$bridge"
test "$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' ewp-postgres-preview-20261006)" = "$old_db"
test "$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)" = "$old_site"
accepted=true
printf 'r2_restart_rehearsal=passed stage_version_unchanged=%s sandbox_id_unchanged=1 legacy=401\n' "$after"
