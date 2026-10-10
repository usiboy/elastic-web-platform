#!/usr/bin/env bash
# New named persistent Gateway replaces the transient-only service after relay is healthy.
set -euo pipefail
root=/data/ewp-builder-20261007
old_site=ewp-preview-site-20261006.service
new_site=ewp-r2-site-gateway-20261008.service
old_relay=ewp-r2-runtime-relay-20261007.service
new_relay=ewp-r2-relay-persistent-20261008.service
new_file=/etc/systemd/system/ewp-r2-site-gateway-20261008.service
socket=/run/ewp-r2-20261007/runtime.sock
test "$(sha256sum /data/ewp-preview-20261006/state/ewp-r2-site-gateway-20261008.service | cut -d' ' -f1)" = bbecdeac028ffe4486c9a0fdea1c43b34609f82dabc85e0aad3e59def1a7de9e
test "$(systemctl is-active "$old_site")" = active
test "$(systemctl is-active "$old_relay")" = active
test "$(systemctl is-active ewp-r2-next-runtime-20261008.service)" = active
test "$(systemctl is-active ewp-preview-api-20261006.service)" = active
test ! -e "$new_file"
pid=$(systemctl show ewp-builder-dockerd-20261007.service -p MainPID --value)
test "$pid" -gt 1
bridge=$(< /sys/class/net/docker0/ifindex)
legacy_db=$(docker inspect --format '{{.Id}}' ewp-postgres-preview-20261006)
legacy_site=$(docker inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)
accepted=false
old_relay_stopped=false
rollback() {
  if [ "$accepted" = true ]; then return; fi
  systemctl disable --now "$new_site" || true
  systemctl disable --now "$new_relay" || true
  if [ -e "$new_file" ]; then rm -f "$new_file"; systemctl daemon-reload; fi
  if [ "$old_relay_stopped" = true ]; then
    if [ -S "$socket" ]; then rm "$socket"; fi
    systemd-run --unit=ewp-r2-runtime-relay-20261007 --property=Restart=on-failure --property=RestartSec=2 \
      /usr/bin/nsenter -t "$pid" -n "$root/bin/ewp-r2-runtime-relay-linux-amd64" \
      --socket "$socket" --upstream 172.29.77.2:3027 || true
  fi
  systemctl start "$old_site" || true
}
trap rollback EXIT
install -m 0644 /data/ewp-preview-20261006/state/ewp-r2-site-gateway-20261008.service "$new_file"
systemctl daemon-reload
systemd-analyze verify "$new_file" /etc/systemd/system/ewp-r2-relay-persistent-20261008.service 2>&1
systemctl enable "$new_site"
systemctl stop "$old_site"
systemctl stop "$old_relay"
old_relay_stopped=true
test ! -e "$socket"
systemctl enable --now "$new_relay"
ready=false
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
  if curl --unix-socket "$socket" --silent --fail --max-time 2 http://runtime.internal/api/health >/dev/null 2>&1; then ready=true; break; fi
  sleep 0.2
done
test "$ready" = true
systemctl start "$new_site"
test "$(systemctl show "$new_site" -p FragmentPath --value)" = "$new_file"
status=''
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
  status=$(curl --noproxy '*' --silent --max-time 2 --cacert /data/ewp-preview-20261006/private/ca.crt \
    --resolve r2-stage.preview.ecp.lingtong.com:18443:127.0.0.1 -o /dev/null -w '%{http_code}' \
    https://r2-stage.preview.ecp.lingtong.com:18443/api/hello) || status=''
  if [ "$status" = 200 ]; then break; fi
  sleep 0.2
done
test "$status" = 200
test "$(curl --noproxy '*' --silent --show-error --max-time 5 -o /dev/null -w '%{http_code}' https://114.55.237.62/)" = 401
test "$(< /sys/class/net/docker0/ifindex)" = "$bridge"
test "$(docker inspect --format '{{.Id}}' ewp-postgres-preview-20261006)" = "$legacy_db"
test "$(docker inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)" = "$legacy_site"
accepted=true
printf 'persistent_site=active persistent_relay=active stage_dynamic=200 legacy=401\n'
