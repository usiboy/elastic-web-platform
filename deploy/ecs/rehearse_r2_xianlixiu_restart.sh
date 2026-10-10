#!/usr/bin/env bash
# Stop/start only the original-app synthetic sandbox, preserve its private SQLite.
set -euo pipefail
export DOCKER_HOST=unix:///run/ewp-builder-20261007/docker.sock
runtime=ewp-r2-xianlixiu-synthetic-20261008.service
relay=ewp-r2-xianlixiu-relay-persistent-20261008.service
name=ewp-r2-xianlixiu-synthetic-20261008
data=/data/ewp-builder-20261007/state/r2-xianlixiu-synthetic-data
socket=/run/ewp-r2-20261007/xianlixiu.sock
test "$(systemctl is-enabled "$runtime")" = enabled && test "$(systemctl is-enabled "$relay")" = enabled
test "$(systemctl is-active "$runtime")" = active && test "$(systemctl is-active "$relay")" = active
id=$(docker inspect "$name" --format '{{.Id}}')
test "$(docker inspect "$name" --format '{{.State.Running}}')" = true
test "$(sha256sum "$data/backups/product-pallet-943a6bed-0d46-4b24-8c5a-7879e5d51134.sqlite" | cut -d' ' -f1)" = a74e3d7ac5808f1c604fd40ede1e50a1b7ad5271de598cc53a2188fc1d52a255
bridge=$(< /sys/class/net/docker0/ifindex)
old_site=$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)
accepted=false
restore() {
  if [ "$accepted" = true ]; then return; fi
  systemctl restart "$runtime" || true
  systemctl start "$relay" || true
}
trap restore EXIT
systemctl stop "$relay"
docker stop -t 5 "$name" >/dev/null
test "$(docker inspect "$name" --format '{{.State.Running}}')" = false
test ! -e "$data/synthetic.sqlite.writer.lock"
systemctl restart "$runtime"
test "$(docker inspect "$name" --format '{{.Id}}')" = "$id"
test "$(docker inspect "$name" --format '{{.State.Running}}')" = true
systemctl start "$relay"
ready=false
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
  if curl --unix-socket "$socket" --silent --fail --max-time 2 http://runtime.internal/api/health >/dev/null 2>&1; then ready=true; break; fi
  sleep 0.2
done
test "$ready" = true
test "$(sha256sum "$data/backups/product-pallet-943a6bed-0d46-4b24-8c5a-7879e5d51134.sqlite" | cut -d' ' -f1)" = a74e3d7ac5808f1c604fd40ede1e50a1b7ad5271de598cc53a2188fc1d52a255
test -f "$data/synthetic.sqlite.writer.lock"
test "$(< /sys/class/net/docker0/ifindex)" = "$bridge"
test "$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)" = "$old_site"
accepted=true
printf 'synthetic_original_app_restart=passed container_id=unchanged sqlite_backup=unchanged old_probe=unchanged\n'
