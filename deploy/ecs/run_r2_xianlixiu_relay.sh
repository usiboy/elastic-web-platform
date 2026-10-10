#!/usr/bin/env bash
# Join only the Builder's private netns; never make the synthetic database public.
set -euo pipefail
root=/data/ewp-builder-20261007
socket=/run/ewp-r2-20261007/xianlixiu.sock
test "$(systemctl show ewp-builder-dockerd-20261007.service -p PrivateNetwork --value)" = yes
test "$(systemctl is-active ewp-r2-xianlixiu-synthetic-20261008.service)" = active
pid=$(systemctl show ewp-builder-dockerd-20261007.service -p MainPID --value)
test "$pid" -gt 1 && test "$(stat -Lc %i /proc/1/ns/net)" != "$(stat -Lc %i "/proc/$pid/ns/net")"
test "$(sha256sum "$root/bin/ewp-r2-runtime-relay-xl-linux-amd64" | cut -d' ' -f1)" = 4ee5db3af9a154e61a65e0624388a039b2a92017520dd214083734e665279a6d
test "$(stat -c '%a:%u' /run/ewp-r2-20261007)" = 700:0
if [ -e "$socket" ]; then
  test -S "$socket" && test "$(stat -c '%u' "$socket")" = 0
  if curl --unix-socket "$socket" --silent --max-time 2 http://runtime.internal/api/health >/dev/null 2>&1; then exit 1; fi
  rm "$socket"
fi
exec /usr/bin/nsenter -t "$pid" -n "$root/bin/ewp-r2-runtime-relay-xl-linux-amd64" \
  --socket "$socket" --upstream 172.29.77.4:3067
