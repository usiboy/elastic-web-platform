#!/usr/bin/env bash
# Only the Builder daemon's private netns can be joined; listener is a 0600 Unix socket.
set -euo pipefail
export DOCKER_HOST=unix:///run/ewp-builder-20261007/docker.sock
unit=ewp-builder-dockerd-20261007.service
root=/data/ewp-builder-20261007
socket_dir=/run/ewp-r2-20261007
socket="$socket_dir/runtime.sock"
test "$(systemctl show "$unit" -p PrivateNetwork --value)" = yes
test "$(systemctl is-active ewp-r2-next-runtime-20261008.service)" = active
pid=$(systemctl show "$unit" -p MainPID --value)
test "$pid" -gt 1 && test "$(stat -Lc %i /proc/1/ns/net)" != "$(stat -Lc %i "/proc/$pid/ns/net")"
test "$(sha256sum "$root/bin/ewp-r2-runtime-relay-linux-amd64" | cut -d' ' -f1)" = 70a8f85e023e1f647446107b4e2f6ff6d31fefcdef45871d60decdfe09ceff72
if [ -e "$socket_dir" ]; then
  test -d "$socket_dir" && test ! -L "$socket_dir" && test "$(stat -c '%a:%u' "$socket_dir")" = 700:0
else
  mkdir -m 0700 "$socket_dir"
fi
if [ -e "$socket" ]; then
  test -S "$socket" && test "$(stat -c '%u' "$socket")" = 0
  if curl --unix-socket "$socket" --silent --max-time 2 http://runtime.internal/api/health >/dev/null 2>&1; then
    printf 'relay socket still served by another process\n' >&2
    exit 1
  fi
  rm "$socket"
fi
exec /usr/bin/nsenter -t "$pid" -n "$root/bin/ewp-r2-runtime-relay-linux-amd64" \
  --socket "$socket" --upstream 172.29.77.2:3027
