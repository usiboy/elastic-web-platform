#!/usr/bin/env bash
# Gateway must not demote a healthy runtime merely because systemd Type=simple
# returned before the Unix relay had bound its socket. Still allow static LKG.
set -euo pipefail
socket=/run/ewp-r2-20261007/runtime.sock
ready=false
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 31 32 33 34 35 36 37 38 39 40; do
  if curl --unix-socket "$socket" --noproxy '*' --silent --fail --max-time 2 http://runtime.internal/api/health >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 0.5
done
if [ "$ready" = true ]; then
  printf 'r2_boot_next_runtime=ready\n'
else
  printf 'r2_boot_next_runtime=unavailable; gateway will keep only verified static fallback\n' >&2
fi
