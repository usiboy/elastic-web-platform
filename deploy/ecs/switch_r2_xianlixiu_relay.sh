#!/usr/bin/env bash
# Swap only the synthetic original-app relay to a persistent systemd unit.
set -euo pipefail
root=/data/ewp-builder-20261007
old=ewp-r2-xianlixiu-relay-20261008.service
new=ewp-r2-xianlixiu-relay-persistent-20261008.service
socket=/run/ewp-r2-20261007/xianlixiu.sock
test "$(systemctl is-active "$old")" = active
test "$(systemctl is-active ewp-r2-xianlixiu-synthetic-20261008.service)" = active
test "$(sha256sum "$root/bin/ewp-r2-runtime-relay-xl-linux-amd64" | cut -d' ' -f1)" = 4ee5db3af9a154e61a65e0624388a039b2a92017520dd214083734e665279a6d
pid=$(systemctl show ewp-builder-dockerd-20261007.service -p MainPID --value)
test "$pid" -gt 1
bridge=$(< /sys/class/net/docker0/ifindex)
old_db=$(docker inspect --format '{{.Id}}' ewp-postgres-preview-20261006)
old_site=$(docker inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)
accepted=false
stopped=false
rollback() {
  if [ "$accepted" = true ]; then return; fi
  systemctl disable --now "$new" || true
  if [ "$stopped" = true ]; then
    if [ -S "$socket" ]; then rm "$socket"; fi
    systemd-run --unit=ewp-r2-xianlixiu-relay-20261008 --property=Restart=on-failure --property=RestartSec=2 \
      /usr/bin/nsenter -t "$pid" -n "$root/bin/ewp-r2-runtime-relay-xl-linux-amd64" \
      --socket "$socket" --upstream 172.29.77.4:3067 || true
  fi
}
trap rollback EXIT
systemctl stop "$old"
stopped=true
test ! -e "$socket"
systemctl enable --now "$new"
ready=false
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
  if curl --unix-socket "$socket" --silent --fail --max-time 2 http://runtime.internal/api/health >/dev/null 2>&1; then ready=true; break; fi
  sleep 0.2
done
test "$ready" = true
test "$(systemctl show "$new" -p FragmentPath --value)" = /etc/systemd/system/ewp-r2-xianlixiu-relay-persistent-20261008.service
test "$(< /sys/class/net/docker0/ifindex)" = "$bridge"
test "$(docker inspect --format '{{.Id}}' ewp-postgres-preview-20261006)" = "$old_db"
test "$(docker inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)" = "$old_site"
accepted=true
printf 'r2_xianlixiu_persistent_relay=active old_containers=unchanged\n'
