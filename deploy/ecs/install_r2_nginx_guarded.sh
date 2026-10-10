#!/usr/bin/env bash
# 只加合成 R2 两个 Host，配置错误即撤回；原 Nginx server 块保持不变。
set -euo pipefail
source=/data/ewp-preview-20261006/state/zz-ewp-r2-synthetic-20261007.conf
target=/etc/nginx/conf.d/zz-ewp-r2-synthetic-20261007.conf
test "$(sha256sum "$source" | cut -d' ' -f1)" = a34b263e97ce5012664b941a34fce3e9dd7f0aa5653b2926eee11f069381d28a
test ! -e "$target"
installed=false
accepted=false
rollback() {
  if [ "$installed" = true ] && [ "$accepted" = false ]; then
    rm -f "$target"
    nginx -t >/dev/null 2>&1 && systemctl reload nginx || true
  fi
}
trap rollback EXIT
install -m 0644 "$source" "$target"
installed=true
nginx -t
systemctl reload nginx
status=''
for attempt in 1 2 3 4 5 6 7 8 9 10; do
  status=$(curl --noproxy '*' --silent --max-time 3 --cacert /data/ewp-preview-20261006/private/ca.crt \
    --resolve d-fae3b413a77f3334325cef72f332378c.preview.ecp.lingtong.com:443:127.0.0.1 \
    -o /dev/null -w '%{http_code}' https://d-fae3b413a77f3334325cef72f332378c.preview.ecp.lingtong.com/api/hello) || status=''
  if [ "$status" = 401 ]; then break; fi
  sleep 0.2
done
test "$status" = 401
test "$(curl --noproxy '*' --silent --show-error --max-time 3 --cacert /data/ewp-preview-20261006/private/ca.crt \
  --resolve r2-stage.preview.ecp.lingtong.com:443:127.0.0.1 -o /dev/null -w '%{http_code}' \
  https://r2-stage.preview.ecp.lingtong.com/)" = 200
test "$(curl --noproxy '*' --silent --show-error --max-time 3 -o /dev/null -w '%{http_code}' https://114.55.237.62/)" = 401
accepted=true
printf 'r2_nginx_exact_hosts=active anonymous_preview=401 static_baseline=200 legacy=401\n'
